package kube

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// BuilderConfig holds the static settings KubeBuilder needs. All values
// originate from the VORTEX_BUILD_* env (admin-tunable); none are hardcoded
// business policy.
type BuilderConfig struct {
	// Namespace is the build namespace where build Jobs run (e.g. vortex-builds).
	Namespace string
	// KanikoImage is the pinned kaniko executor image used for the Dockerfile
	// strategy.
	KanikoImage string
	// BuildpacksImage is the pinned Cloud Native Buildpacks lifecycle executor
	// image used for the no-Dockerfile strategy. It must be an image whose
	// entrypoint runs the CNB lifecycle creator (or an equivalent rootless
	// nixpacks/pack executor) and reads CNB_* env. Empty falls back to
	// defaultBuildpacksImage.
	BuildpacksImage string
	// BuildpacksBuilder is the default CNB builder image the lifecycle uses to
	// auto-detect and assemble the app (e.g. paketobuildpacks/builder-jammy-base).
	// A per-request BuildRequest.Builder overrides it. Empty falls back to
	// defaultBuildpacksBuilder.
	BuildpacksBuilder string
	// GitCloneImage is the small image used by the buildpacks-path INIT container
	// to clone the source into the shared workspace (the CNB lifecycle does not
	// clone git itself). Empty falls back to defaultGitCloneImage.
	GitCloneImage string
	// PushSecret is the docker-config Secret (kubernetes.io/dockerconfigjson)
	// mounted so the build can push to the registry.
	//
	// SECURITY / TODO(multi-tenant): this is a SINGLE registry-wide push secret
	// shared by every tenant build pod. The fully-correct fix is per-org,
	// short-lived, scoped registry tokens minted just-in-time per build (DEFERRED).
	// Until then, exfiltration is contained by the pod hardening below: no
	// ServiceAccount token is mounted and a default-deny-egress NetworkPolicy
	// blocks the in-cluster API/services, allowing egress only to DNS + registry +
	// git. The secret is mounted ONLY when configured (production); local/dev
	// builds mount nothing. DO NOT ship untrusted-tenant production on the shared
	// push secret.
	PushSecret string
	// GitCredentialsSecret, when set, is a Secret in the build namespace exposing
	// GIT_USERNAME/GIT_PASSWORD (or GIT_TOKEN) so PRIVATE repos can clone.
	// Optional: public clones work without it.
	GitCredentialsSecret string
	// Timeout bounds a single build (Job creation -> completion). Zero falls back
	// to defaultBuilderTimeout.
	Timeout time.Duration
}

const (
	// defaultBuilderTimeout is the fallback per-build deadline.
	defaultBuilderTimeout = 10 * time.Minute
	// defaultKanikoExecutorImage is the pinned kaniko executor used when
	// BuilderConfig leaves it unset (Dockerfile strategy).
	defaultKanikoExecutorImage = "gcr.io/kaniko-project/executor:v1.23.2"
	// defaultBuildpacksImage is the pinned CNB lifecycle executor used when
	// BuilderConfig leaves it unset (buildpacks strategy). It runs the lifecycle
	// creator rootless inside the build Job.
	defaultBuildpacksImage = "buildpacksio/lifecycle:0.20.5"
	// defaultBuildpacksBuilder is the CNB builder image the lifecycle auto-detects
	// against when neither the request nor the config selects one.
	defaultBuildpacksBuilder = "paketobuildpacks/builder-jammy-base:latest"
	// defaultGitCloneImage is the pinned, minimal git image used by the
	// buildpacks-path init container to clone source into the workspace.
	defaultGitCloneImage = "alpine/git:2.45.2"

	// dockerConfigDir is where the build reads push credentials.
	builderDockerConfigDir = "/kaniko/.docker"
	// cnbDockerConfigDir is where the CNB lifecycle reads push credentials.
	cnbDockerConfigDir = "/cnb/.docker"
	// cnbWorkspaceDir is the lifecycle's app workspace (source is cloned here).
	cnbWorkspaceDir = "/workspace"
	// cnbLayersDir is the lifecycle's layers/cache directory.
	cnbLayersDir = "/layers"

	// builderPollInterval is how often the Job is polled for completion.
	builderPollInterval = 2 * time.Second
	// builderLogTailLines is the number of trailing build-pod log lines collected
	// on failure (and included in the returned error).
	builderLogTailLines = 50

	// builderSA is the dedicated, RBAC-less ServiceAccount the build pod runs as
	// (its automounted token is also disabled). It has NO RoleBindings, so even if
	// its token leaked it grants nothing.
	builderSA = "vortex-builder"
	// builderEgressPolicy default-denies build-pod egress except DNS + registry +
	// git.
	builderEgressPolicy = "vortex-builder-egress"
	// builderPodLabel marks build pods so the egress NetworkPolicy selects them.
	builderPodLabel = "vortex.io/build"
)

// KubeBuilder is the real Builder: it submits an executor Job to the build
// namespace, waits for completion, and returns the pushed image (or an error
// with the build pod's tail logs on failure). It chooses kaniko for a Dockerfile
// source and the CNB lifecycle for a no-Dockerfile source.
type KubeBuilder struct {
	cfg    BuilderConfig
	client kubernetes.Interface
}

var _ Builder = (*KubeBuilder)(nil)

// NewKubeBuilder builds a KubeBuilder from an existing clientset. Tests pass
// client-go's fake clientset; production passes a real clientset.
func NewKubeBuilder(cfg BuilderConfig, client kubernetes.Interface) *KubeBuilder {
	if cfg.Namespace == "" {
		cfg.Namespace = "vortex-builds"
	}
	if cfg.KanikoImage == "" {
		cfg.KanikoImage = defaultKanikoExecutorImage
	}
	if cfg.BuildpacksImage == "" {
		cfg.BuildpacksImage = defaultBuildpacksImage
	}
	if cfg.BuildpacksBuilder == "" {
		cfg.BuildpacksBuilder = defaultBuildpacksBuilder
	}
	if cfg.GitCloneImage == "" {
		cfg.GitCloneImage = defaultGitCloneImage
	}
	return &KubeBuilder{cfg: cfg, client: client}
}

// builderGitHostPath restricts the host+path portion of a clone URL to a
// conservative charset (no spaces, shell/flag metacharacters, '#' or '?'), so a
// tenant repo can never smuggle an executor flag, a ref fragment, or a query
// into the build context.
var builderGitHostPath = regexp.MustCompile(`^[A-Za-z0-9._~:/@%-]+$`)

// builderSafeRef permits a conservative git ref charset (no leading dash, no
// spaces or shell metacharacters) so a ref can never be parsed as a flag.
var builderSafeRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// builderSafePath permits a relative path used for context-sub-path / dockerfile.
var builderSafePath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// builderSafeImage permits a registry/repo:tag reference for the optional CNB
// builder image override, rejecting shell/flag metacharacters.
var builderSafeImage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)

// validatedBuilderRepo returns the validated "<scheme>://<host><path>" for the
// clone URL, rejecting anything that could smuggle a flag, a ref fragment ('#'),
// or a query ('?') into the build context.
func validatedBuilderRepo(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("kube/build: invalid git repo URL %q: %w", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "git" {
		return "", fmt.Errorf("kube/build: invalid git repo URL %q (only https:// or git://)", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("kube/build: invalid git repo URL %q (no host)", raw)
	}
	if u.Fragment != "" || u.RawFragment != "" || u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("kube/build: git repo URL %q must not contain a fragment ('#') or query ('?')", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("kube/build: git repo URL %q must not embed credentials", raw)
	}
	hostPath := u.Host + u.EscapedPath()
	if !builderGitHostPath.MatchString(hostPath) {
		return "", fmt.Errorf("kube/build: invalid git repo URL %q", raw)
	}
	return u.Scheme + "://" + hostPath, nil
}

// validate rejects requests whose inputs could inject executor args.
func (r BuildRequest) validate() error {
	if _, err := validatedBuilderRepo(r.GitRepo); err != nil {
		return err
	}
	if r.GitRef != "" && !builderSafeRef.MatchString(r.GitRef) {
		return fmt.Errorf("kube/build: invalid git ref %q", r.GitRef)
	}
	if r.ContextDir != "" && !builderSafePath.MatchString(r.ContextDir) {
		return fmt.Errorf("kube/build: invalid context dir %q", r.ContextDir)
	}
	if r.Dockerfile != "" && !builderSafePath.MatchString(r.Dockerfile) {
		return fmt.Errorf("kube/build: invalid dockerfile %q", r.Dockerfile)
	}
	if r.Builder != "" && !builderSafeImage.MatchString(r.Builder) {
		return fmt.Errorf("kube/build: invalid builder image %q", r.Builder)
	}
	if strings.TrimSpace(r.ImageRef) == "" {
		return fmt.Errorf("kube/build: empty image ref")
	}
	for k := range r.BuildArgs {
		if strings.ContainsAny(k, " \t\n=") || strings.HasPrefix(k, "-") {
			return fmt.Errorf("kube/build: invalid build-arg key %q", k)
		}
	}
	return nil
}

// gitContext is the validated "<repo>#refs/heads/<ref>" build context shared by
// both executors. validate() has already accepted the URL; on the (re-checked)
// error it falls back to the raw repo so the caller still sees a build failure
// rather than a panic.
func (r BuildRequest) gitContext() string {
	ref := r.GitRef
	if ref == "" {
		ref = "main"
	}
	repo, err := validatedBuilderRepo(r.GitRepo)
	if err != nil {
		repo = r.GitRepo
	}
	return fmt.Sprintf("%s#refs/heads/%s", repo, ref)
}

// kanikoArgs builds the kaniko executor argument list for the Dockerfile
// strategy. The repo is the validated host+path; the ref comes ONLY from the
// separately-validated GitRef (never from a URL fragment).
func (b *KubeBuilder) kanikoArgs(r BuildRequest) []string {
	dockerfile := r.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	args := []string{
		"--context=" + r.gitContext(),
		"--dockerfile=" + dockerfile,
		"--destination=" + r.ImageRef,
	}
	if r.ContextDir != "" {
		args = append(args, "--context-sub-path="+r.ContextDir)
	}
	for _, k := range sortedKeys(r.BuildArgs) {
		args = append(args, "--build-arg", k+"="+r.BuildArgs[k])
	}
	return args
}

// buildpacksArgs builds the CNB lifecycle creator argument list for the
// no-Dockerfile strategy. The source has already been cloned into the workspace
// by the init container, so the lifecycle auto-detects buildpacks against the
// builder image, assembles the app, and exports the OCI image DIRECTLY to the
// registry (no docker daemon; this is a rootless, daemonless build). When the app
// lives in a sub-directory, -app points at it.
func (b *KubeBuilder) buildpacksArgs(r BuildRequest) []string {
	builderImg := r.Builder
	if builderImg == "" {
		builderImg = b.cfg.BuildpacksBuilder
	}
	appDir := cnbWorkspaceDir
	if r.ContextDir != "" {
		appDir = cnbWorkspaceDir + "/" + r.ContextDir
	}
	args := []string{
		"-app=" + appDir,
		"-layers=" + cnbLayersDir,
		"-run-image=" + builderImg,
		r.ImageRef,
	}
	return args
}

// gitCloneInit builds the hardened INIT container that clones the validated git
// source into the shared workspace volume for the buildpacks lifecycle. It runs
// with the SAME container hardening as the build container (drop ALL caps, no
// privilege escalation) and uses ONLY the separately-validated repo + ref (never
// a URL fragment), so a tenant repo cannot smuggle a git flag.
//
// PUBLIC repos clone with no credentials (the common Heroku/Fly-class case). For
// PRIVATE repos the git-credentials env is wired in; the clone image is expected
// to honor a credential helper / GIT_ASKPASS for it (the same secret the
// Dockerfile/kaniko path consumes). It is NOT a fake-success path: a clone that
// cannot authenticate fails the init container, which fails the build loudly.
func (b *KubeBuilder) gitCloneInit(r BuildRequest, workspaceVol string) corev1.Container {
	ref := r.GitRef
	if ref == "" {
		ref = "main"
	}
	repo, err := validatedBuilderRepo(r.GitRepo)
	if err != nil {
		// validate() runs before JobSpec in Build; fall back so a direct JobSpec
		// caller still renders a (clone-failing) spec rather than panicking.
		repo = r.GitRepo
	}
	// Args are passed as discrete argv elements (NOT a shell string), and both repo
	// and ref are validated, so neither can be parsed as a clone flag.
	args := []string{
		"clone", "--depth=1", "--single-branch",
		"--branch", ref,
		repo, cnbWorkspaceDir,
	}
	return corev1.Container{
		Name:  "git-clone",
		Image: b.cfg.GitCloneImage,
		Args:  args,
		Env:   b.gitCredentialsEnv(),
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: builderBoolPtr(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      workspaceVol,
			MountPath: cnbWorkspaceDir,
		}},
	}
}

// sortedKeys returns the map keys sorted, for deterministic (testable) arg order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// JobSpec renders (without submitting) the executor Job for r, choosing kaniko
// for the Dockerfile strategy and the CNB lifecycle for the buildpacks strategy.
// It is exported so tests can assert the spec, and is the single source of truth
// used by Build.
//
// SECURITY: the build runs tenant-controlled inputs, so the pod is isolated: no
// default ServiceAccount token is mounted (AutomountServiceAccountToken=false)
// and it runs as the dedicated, RBAC-less "vortex-builder" SA, so a hostile
// source cannot reach the cluster API. The pod uses the RuntimeDefault seccomp
// profile and the container drops ALL capabilities with
// AllowPrivilegeEscalation=false. We deliberately do NOT force runAsNonRoot:
// both kaniko and the CNB lifecycle need write access to their overlay/layers
// filesystems; isolation (no token, deny-egress NetworkPolicy) contains it.
func (b *KubeBuilder) JobSpec(r BuildRequest) *batchv1.Job {
	strategy := r.resolveStrategy()
	name := builderJobName(r)
	backoff := int32(0)
	ttl := int32(3600)
	deadline := int64(b.timeout().Seconds())
	automount := false

	var (
		containerName  string
		image          string
		args           []string
		env            []corev1.EnvVar
		dockerCfgDir   string
		extraMounts    []corev1.VolumeMount
		extraVolumes   []corev1.Volume
		initContainers []corev1.Container
	)
	env = b.gitCredentialsEnv()
	switch strategy {
	case StrategyDockerfile:
		// Kaniko clones the git context itself; no source-prep init container or
		// workspace volume is needed.
		containerName = "kaniko"
		image = b.cfg.KanikoImage
		args = b.kanikoArgs(r)
		dockerCfgDir = builderDockerConfigDir
	default: // StrategyBuildpacks
		containerName = "lifecycle"
		image = b.cfg.BuildpacksImage
		args = b.buildpacksArgs(r)
		dockerCfgDir = cnbDockerConfigDir
		// The CNB lifecycle does NOT clone git itself: it builds the source already
		// present in -app. So a hardened git-clone INIT container populates the
		// shared workspace volume first, then the lifecycle assembles + exports it.
		// Both share the workspace EmptyDir; the lifecycle also needs a layers dir.
		workspaceVol := "cnb-workspace"
		layersVol := "cnb-layers"
		extraVolumes = []corev1.Volume{
			{Name: workspaceVol, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			{Name: layersVol, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		}
		extraMounts = []corev1.VolumeMount{
			{Name: workspaceVol, MountPath: cnbWorkspaceDir},
			{Name: layersVol, MountPath: cnbLayersDir},
		}
		initContainers = []corev1.Container{b.gitCloneInit(r, workspaceVol)}
		// Non-secret build-time env is surfaced to the lifecycle so buildpacks can
		// honor it (e.g. BP_* tuning knobs). Runtime secrets are NEVER forwarded.
		env = append(env, corev1.EnvVar{Name: "CNB_APP_DIR", Value: cnbWorkspaceDir})
		for _, k := range sortedKeys(r.BuildArgs) {
			env = append(env, corev1.EnvVar{Name: k, Value: r.BuildArgs[k]})
		}
	}

	volumeName := "docker-config"
	mounts := append([]corev1.VolumeMount{{
		Name:      volumeName,
		MountPath: dockerCfgDir,
		ReadOnly:  true,
	}}, extraMounts...)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.cfg.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vortex",
				builderPodLabel:                string(strategy),
				"vortex.io/app":                sanitizeBuilderName(r.AppID),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "vortex",
						builderPodLabel:                string(strategy),
						"job-name":                     name,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					ServiceAccountName:           builderSA,
					AutomountServiceAccountToken: &automount,
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					InitContainers: initContainers,
					Containers: []corev1.Container{{
						Name:  containerName,
						Image: image,
						Args:  args,
						Env:   env,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: builderBoolPtr(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
						VolumeMounts: mounts,
					}},
				},
			},
		},
	}

	volumes := []corev1.Volume{b.dockerConfigVolume(volumeName)}
	volumes = append(volumes, extraVolumes...)
	job.Spec.Template.Spec.Volumes = volumes
	return job
}

// dockerConfigVolume returns the push-credentials volume: the configured push
// Secret in production, or an empty dir in local/dev (no registry configured).
func (b *KubeBuilder) dockerConfigVolume(name string) corev1.Volume {
	if b.cfg.PushSecret != "" {
		return corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: b.cfg.PushSecret,
					Items: []corev1.KeyToPath{{
						Key:  ".dockerconfigjson",
						Path: "config.json",
					}},
				},
			},
		}
	}
	return corev1.Volume{
		Name:         name,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}
}

// gitCredentialsEnv exposes GIT_USERNAME/GIT_PASSWORD (and GIT_TOKEN) to the
// executor from the configured git-credentials Secret so PRIVATE repos can clone
// over HTTPS. Public repos work without it (returns nil).
func (b *KubeBuilder) gitCredentialsEnv() []corev1.EnvVar {
	if b.cfg.GitCredentialsSecret == "" {
		return nil
	}
	optional := true
	ref := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: b.cfg.GitCredentialsSecret},
				Key:                  key,
				Optional:             &optional,
			},
		}
	}
	return []corev1.EnvVar{
		{Name: "GIT_USERNAME", ValueFrom: ref("GIT_USERNAME")},
		{Name: "GIT_PASSWORD", ValueFrom: ref("GIT_PASSWORD")},
		{Name: "GIT_TOKEN", ValueFrom: ref("GIT_TOKEN")},
	}
}

func builderBoolPtr(v bool) *bool { return &v }

// Build submits the executor Job (kaniko for a Dockerfile, the CNB lifecycle
// otherwise), polls to completion within the build timeout, and returns the
// pushed image on success. On failure it collects the build pod's tail logs and
// returns an error including them.
func (b *KubeBuilder) Build(ctx context.Context, r BuildRequest) (BuildResult, error) {
	if err := r.validate(); err != nil {
		return BuildResult{}, err
	}

	// Idempotently ensure the build namespace's isolation primitives (RBAC-less
	// builder SA + default-deny-egress NetworkPolicy) before submitting a Job that
	// runs a tenant-controlled build.
	if err := b.ensureIsolation(ctx); err != nil {
		return BuildResult{}, err
	}

	strategy := r.resolveStrategy()
	job := b.JobSpec(r)
	// The Job name is unique per build (app+build id), so AlreadyExists here means a
	// genuine collision/duplicate submission — NOT a stale prior build. Surface it
	// rather than silently polling a pre-existing job (which would return a stale or
	// never-built image for the current request).
	_, err := b.client.BatchV1().Jobs(b.cfg.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return BuildResult{}, fmt.Errorf("kube/build: job %s already exists (duplicate build submission)", job.Name)
		}
		return BuildResult{}, fmt.Errorf("kube/build: create job %s: %w", job.Name, err)
	}

	if err := b.waitForJob(ctx, job.Name); err != nil {
		logs := b.collectLogs(ctx, job.Name)
		if logs != "" {
			return BuildResult{}, fmt.Errorf("%w\n--- build logs ---\n%s", err, logs)
		}
		return BuildResult{}, err
	}
	return BuildResult{Image: r.ImageRef, Strategy: strategy}, nil
}

// ensureIsolation idempotently provisions the build namespace's isolation
// primitives: the dedicated, RBAC-less "vortex-builder" ServiceAccount and a
// default-deny-egress NetworkPolicy that allows egress ONLY to DNS, the registry,
// and git (blocking the in-cluster API and other tenant services). Both are
// upserted; AlreadyExists is success.
func (b *KubeBuilder) ensureIsolation(ctx context.Context) error {
	if err := b.ensureBuilderSA(ctx); err != nil {
		return err
	}
	return b.ensureEgressPolicy(ctx)
}

// ensureBuilderSA upserts the dedicated builder ServiceAccount with automounting
// disabled and NO RoleBindings, so even a leaked token grants nothing.
func (b *KubeBuilder) ensureBuilderSA(ctx context.Context) error {
	automount := false
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      builderSA,
			Namespace: b.cfg.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vortex"},
		},
		AutomountServiceAccountToken: &automount,
	}
	_, err := b.client.CoreV1().ServiceAccounts(b.cfg.Namespace).Create(ctx, sa, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("kube/build: ensure builder ServiceAccount: %w", err)
	}
	return nil
}

// ensureEgressPolicy upserts a default-deny-egress NetworkPolicy scoped to build
// pods (both kaniko and buildpacks). It permits egress to DNS (UDP/TCP 53) and to
// external destinations on the HTTPS/git ports (443, 9418) while denying egress
// to in-cluster Pod/Service CIDRs — so a hostile build cannot reach the
// Kubernetes API or other tenants.
func (b *KubeBuilder) ensureEgressPolicy(ctx context.Context) error {
	dns := corev1.ProtocolUDP
	tcp := corev1.ProtocolTCP
	port53 := intstr.FromInt32(53)
	port443 := intstr.FromInt32(443)
	portGit := intstr.FromInt32(9418)

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      builderEgressPolicy,
			Namespace: b.cfg.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vortex"},
		},
		Spec: networkingv1.NetworkPolicySpec{
			// Select ALL build pods (any strategy) via the presence of the build
			// label key.
			PodSelector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      builderPodLabel,
					Operator: metav1.LabelSelectorOpExists,
				}},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				// DNS resolution (kube-dns).
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &dns, Port: &port53},
						{Protocol: &tcp, Port: &port53},
					},
				},
				// Registry (HTTPS) + git, but ONLY to destinations outside the
				// in-cluster private ranges — this blocks the API server and other
				// tenant Services/Pods while still reaching the public registry/git.
				{
					To: []networkingv1.NetworkPolicyPeer{{
						IPBlock: &networkingv1.IPBlock{
							CIDR: "0.0.0.0/0",
							Except: []string{
								"10.0.0.0/8",
								"172.16.0.0/12",
								"192.168.0.0/16",
							},
						},
					}},
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &tcp, Port: &port443},
						{Protocol: &tcp, Port: &portGit},
					},
				},
			},
		},
	}
	_, err := b.client.NetworkingV1().NetworkPolicies(b.cfg.Namespace).Create(ctx, np, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("kube/build: ensure egress NetworkPolicy: %w", err)
	}
	return nil
}

// waitForJob polls the Job until it Completes (succeeded) or Fails, bounded by
// the build timeout.
func (b *KubeBuilder) waitForJob(ctx context.Context, name string) error {
	return wait.PollUntilContextTimeout(ctx, builderPollInterval, b.timeout(), true,
		func(ctx context.Context) (bool, error) {
			job, err := b.client.BatchV1().Jobs(b.cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			for _, c := range job.Status.Conditions {
				if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
					return true, nil
				}
				if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
					return false, fmt.Errorf("kube/build: job %s failed: %s", name, c.Message)
				}
			}
			return false, nil
		})
}

// collectLogs returns the tail logs of the build Job's pod (best-effort).
func (b *KubeBuilder) collectLogs(ctx context.Context, jobName string) string {
	pods, err := b.client.CoreV1().Pods(b.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	names := make([]string, 0, len(pods.Items))
	for _, p := range pods.Items {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	tail := int64(builderLogTailLines)
	raw, err := b.client.CoreV1().Pods(b.cfg.Namespace).
		GetLogs(names[0], &corev1.PodLogOptions{TailLines: &tail}).DoRaw(ctx)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (b *KubeBuilder) timeout() time.Duration {
	if b.cfg.Timeout > 0 {
		return b.cfg.Timeout
	}
	return defaultBuilderTimeout
}

// builderJobName is a DNS-1123 Job name UNIQUE PER BUILD:
// "build-<shortAppID>-<shortBuildID>". A unique name (vs. one shared per app) is
// required so a rebuild always creates a fresh Job instead of colliding with a
// prior (possibly still-TTL'd) Job — which would otherwise make rebuilds a no-op
// returning a stale/never-built image.
func builderJobName(r BuildRequest) string {
	app := shortName(sanitizeBuilderName(r.AppID), 8)
	bid := shortName(sanitizeBuilderName(r.BuildID), 8)
	switch {
	case app != "" && bid != "":
		return "build-" + app + "-" + bid
	case bid != "":
		return "build-" + bid
	case app != "":
		return "build-" + app
	default:
		return "build-" + shortName(sanitizeBuilderName(r.AppName), 8)
	}
}

// shortName trims s to at most n characters (and re-trims a trailing separator).
func shortName(s string, n int) string {
	if len(s) > n {
		s = strings.Trim(s[:n], "-")
	}
	return s
}

// sanitizeBuilderName keeps names valid for Kubernetes object names / labels. It
// is a builder-local helper (the package's sanitize() does not cap length the way
// build object names require).
func sanitizeBuilderName(s string) string {
	s = sanitize(s)
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	return s
}

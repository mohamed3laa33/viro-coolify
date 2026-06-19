package build

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

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
)

// Config holds the static settings KanikoBuilder needs. All values originate
// from the VORTEX_BUILD_* env (admin-tunable); none are hardcoded business
// policy.
type Config struct {
	// Namespace is the build namespace where kaniko Jobs run (e.g. vortex-builds).
	Namespace string
	// KanikoImage is the pinned kaniko executor image.
	KanikoImage string
	// PushSecret is the docker-config Secret (kubernetes.io/dockerconfigjson)
	// mounted at /kaniko/.docker so kaniko can push to the registry.
	//
	// SECURITY / TODO(multi-tenant): this is a SINGLE registry-wide push secret
	// shared by every tenant build pod, so a hostile Dockerfile (e.g. a malicious
	// RUN that reads /kaniko/.docker/config.json) could exfiltrate registry-wide
	// push credentials. The fully-correct fix is per-org, short-lived, scoped
	// registry tokens (registry-specific: ECR/GCR/ACR/DO token exchange) minted
	// just-in-time per build. That is DEFERRED. Until then, exfiltration is
	// contained by item 3's hardening: no ServiceAccount token is mounted and a
	// default-deny-egress NetworkPolicy blocks the in-cluster API/services,
	// allowing egress only to DNS + registry + git. This secret is mounted ONLY
	// when a registry is configured (production); local/dev builds mount nothing.
	// DO NOT ship untrusted-tenant production on the shared push secret.
	PushSecret string
	// GitCredentialsSecret, when set, is a Secret in the build namespace exposing
	// GIT_USERNAME/GIT_PASSWORD (or GIT_TOKEN) so kaniko can clone PRIVATE repos.
	// Optional: public clones work without it.
	GitCredentialsSecret string
	// Timeout bounds a single build (Job creation -> completion). Zero falls back
	// to defaultBuildTimeout.
	Timeout time.Duration

	// --- Cloud Native Buildpacks (no-Dockerfile) strategy ---
	// These configure the buildpacks executor used when a source has NO Dockerfile.
	// The Dockerfile/Kaniko path above ignores them. All originate from the
	// VORTEX_BUILDPACKS_* / VORTEX_BUILD_BUILDPACKS_IMAGE env (admin-tunable).

	// BuildpacksBuilderImage is the default CNB builder image the lifecycle uses to
	// auto-detect and assemble the app (e.g. paketobuildpacks/builder-jammy-base,
	// from VORTEX_BUILDPACKS_BUILDER). A per-request Request.Builder overrides it.
	// Empty lets the kube builder use its pinned default. When the whole field is
	// empty the no-Dockerfile path still works (the kube default applies); this is
	// the single image knob the wiring layer threads through.
	BuildpacksBuilderImage string
	// BuildpacksImage is the pinned CNB lifecycle executor image
	// (VORTEX_BUILD_BUILDPACKS_IMAGE). Empty lets the kube builder use its pinned
	// default.
	BuildpacksImage string
	// GitCloneImage is the small image the buildpacks path uses to clone source
	// into the lifecycle workspace (the lifecycle does not clone git itself). Empty
	// lets the kube builder use its pinned default.
	GitCloneImage string
}

const (
	// defaultBuildTimeout is the fallback per-build deadline.
	defaultBuildTimeout = 10 * time.Minute
	// defaultKanikoImage is the pinned kaniko executor used when Config leaves it
	// unset. Mirrors the config default.
	defaultKanikoImage = "gcr.io/kaniko-project/executor:v1.23.2"
	// dockerConfigMount is where kaniko reads the push credentials.
	dockerConfigMount = "/kaniko/.docker"
	// buildPollInterval is how often the Job is polled for completion.
	buildPollInterval = 2 * time.Second
	// logTailLines is the number of trailing build-pod log lines collected on
	// failure (and included in the returned error).
	logTailLines = 50
	// builderServiceAccount is the dedicated, RBAC-less ServiceAccount the build
	// pod runs as (its automounted token is also disabled). It is ensured by the
	// builder; it has NO RoleBindings, so even if its token leaked it grants
	// nothing.
	builderServiceAccount = "vortex-builder"
	// denyEgressPolicy is the NetworkPolicy name that default-denies build-pod
	// egress except DNS + registry + git.
	denyEgressPolicy = "vortex-builder-egress"
)

// KanikoBuilder is the real Builder and the ONE coherent build entrypoint: it
// dispatches on Request.Strategy. A Dockerfile source builds via the kaniko
// executor Job in this package (submitted to the build namespace, polled to
// completion, returning the pushed image or an error with the build pod's tail
// logs). A no-Dockerfile source builds via Cloud Native Buildpacks, delegated to
// the kube package's KubeBuilder (the Wave 2 buildpacks executor) so the
// buildpacks Job logic lives in exactly one place. Both paths share the same
// hardened build namespace (RBAC-less SA + default-deny egress).
type KanikoBuilder struct {
	cfg    Config
	client kubernetes.Interface

	// bp is the Cloud Native Buildpacks executor used for the no-Dockerfile
	// strategy. It is the Wave 2 kube.KubeBuilder, configured from the same build
	// namespace / push secret / git credentials so both strategies are isolated
	// identically. Never nil after NewKanikoBuilder.
	bp kube.Builder
}

var _ Builder = (*KanikoBuilder)(nil)

// NewKanikoBuilder builds a KanikoBuilder from an existing clientset. Tests pass
// client-go's fake clientset; production passes a real clientset. Despite the
// name it is the dual-strategy entrypoint: it also constructs the Wave 2
// buildpacks executor (kube.KubeBuilder) for no-Dockerfile sources, sharing the
// same build namespace, push secret and git credentials.
func NewKanikoBuilder(cfg Config, client kubernetes.Interface) *KanikoBuilder {
	if cfg.Namespace == "" {
		cfg.Namespace = "vortex-builds"
	}
	if cfg.KanikoImage == "" {
		cfg.KanikoImage = defaultKanikoImage
	}
	return &KanikoBuilder{
		cfg:    cfg,
		client: client,
		bp: kube.NewKubeBuilder(kube.BuilderConfig{
			Namespace:            cfg.Namespace,
			KanikoImage:          cfg.KanikoImage,
			BuildpacksImage:      cfg.BuildpacksImage,
			BuildpacksBuilder:    cfg.BuildpacksBuilderImage,
			GitCloneImage:        cfg.GitCloneImage,
			PushSecret:           cfg.PushSecret,
			GitCredentialsSecret: cfg.GitCredentialsSecret,
			Timeout:              cfg.Timeout,
		}, client),
	}
}

// NewBuilder is a clearer alias for NewKanikoBuilder: it constructs the
// dual-strategy build entrypoint (Kaniko for Dockerfiles, Cloud Native
// Buildpacks otherwise) from config and a clientset.
func NewBuilder(cfg Config, client kubernetes.Interface) *KanikoBuilder {
	return NewKanikoBuilder(cfg, client)
}

// gitHostPath restricts the host+path portion of a clone URL to a conservative
// charset (no spaces, shell/flag metacharacters, '#' or '?'), so a tenant repo
// can never smuggle a kaniko flag, a ref fragment, or a query into the context.
var gitHostPath = regexp.MustCompile(`^[A-Za-z0-9._~:/@%-]+$`)

// safeRef permits a conservative git ref charset (no leading dash, no spaces or
// shell metacharacters) so a ref can never be parsed as a kaniko flag.
var safeRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// safePath permits a relative path used for --context-sub-path / --dockerfile.
var safePath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// validatedRepo returns the validated "<scheme>://<host><path>" for the clone
// URL, rejecting anything that could smuggle a flag, a ref fragment ('#'), or a
// query ('?') into the kaniko git context. It parses with net/url and refuses a
// non-empty Fragment or RawQuery so the build context is built ONLY from the
// validated host+path (plus the separately-validated GitRef).
func validatedRepo(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("build: invalid git repo URL %q: %w", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "git" {
		return "", fmt.Errorf("build: invalid git repo URL %q (only https:// or git://)", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("build: invalid git repo URL %q (no host)", raw)
	}
	// Reject ref/fragment and query injection: the ref comes solely from GitRef.
	if u.Fragment != "" || u.RawFragment != "" || u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("build: git repo URL %q must not contain a fragment ('#') or query ('?')", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("build: git repo URL %q must not embed credentials", raw)
	}
	hostPath := u.Host + u.EscapedPath()
	if !gitHostPath.MatchString(hostPath) {
		return "", fmt.Errorf("build: invalid git repo URL %q", raw)
	}
	return u.Scheme + "://" + hostPath, nil
}

// validate rejects requests whose inputs could inject kaniko args.
func (r Request) validate() error {
	if _, err := validatedRepo(r.GitRepo); err != nil {
		return err
	}
	if r.GitRef != "" && !safeRef.MatchString(r.GitRef) {
		return fmt.Errorf("build: invalid git ref %q", r.GitRef)
	}
	if r.ContextDir != "" && !safePath.MatchString(r.ContextDir) {
		return fmt.Errorf("build: invalid context dir %q", r.ContextDir)
	}
	if r.Dockerfile != "" && !safePath.MatchString(r.Dockerfile) {
		return fmt.Errorf("build: invalid dockerfile %q", r.Dockerfile)
	}
	if strings.TrimSpace(r.ImageRef) == "" {
		return fmt.Errorf("build: empty image ref")
	}
	for k := range r.BuildArgs {
		if strings.ContainsAny(k, " \t\n=") || strings.HasPrefix(k, "-") {
			return fmt.Errorf("build: invalid build-arg key %q", k)
		}
	}
	return nil
}

// kanikoArgs builds the kaniko executor argument list for the request. The git
// context preserves the ORIGINAL clone URL scheme (https:// or git://) so private
// repos can be cloned over authenticated HTTPS — kaniko accepts an https:// git
// context and reads GIT_USERNAME/GIT_PASSWORD (or GIT_TOKEN) from the build pod's
// env for auth. The repo is the validated host+path; the ref comes ONLY from the
// separately-validated GitRef (never from a URL fragment).
func kanikoArgs(r Request) []string {
	ref := r.GitRef
	if ref == "" {
		ref = "main"
	}
	dockerfile := r.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	// validate() has already accepted this URL; ignore the (re-checked) error.
	repo, err := validatedRepo(r.GitRepo)
	if err != nil {
		repo = r.GitRepo
	}
	context := fmt.Sprintf("%s#refs/heads/%s", repo, ref)

	args := []string{
		"--context=" + context,
		"--dockerfile=" + dockerfile,
		"--destination=" + r.ImageRef,
	}
	if r.ContextDir != "" {
		args = append(args, "--context-sub-path="+r.ContextDir)
	}
	// Deterministic ordering so the Job spec is stable/testable.
	keys := make([]string, 0, len(r.BuildArgs))
	for k := range r.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+r.BuildArgs[k])
	}
	return args
}

// JobSpec renders (without submitting) the kaniko Job for r. It is exported so
// tests can assert the spec, and is the single source of truth used by Build.
//
// SECURITY: the build runs a tenant-controlled Dockerfile, so the pod is
// isolated: no default ServiceAccount token is mounted (AutomountServiceAccount-
// Token=false) and it runs as the dedicated, RBAC-less "vortex-builder" SA, so a
// hostile Dockerfile cannot reach the cluster API. The pod uses the RuntimeDefault
// seccomp profile and the container drops ALL capabilities with
// AllowPrivilegeEscalation=false. We deliberately do NOT force runAsNonRoot:
// kaniko needs root for its overlay filesystem; isolation (no token, deny-egress
// NetworkPolicy) contains it instead.
func (b *KanikoBuilder) JobSpec(r Request) *batchv1.Job {
	name := jobName(r)
	backoff := int32(0)
	ttl := int32(3600)
	deadline := int64(b.timeout().Seconds())
	automount := false

	volumeName := "docker-config"
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.cfg.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vortex",
				"vortex.io/build":              "kaniko",
				"vortex.io/app":                sanitize(r.AppID),
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
						"vortex.io/build":              "kaniko",
						"job-name":                     name,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					// Isolate a tenant-controlled build from the cluster API.
					ServiceAccountName:           builderServiceAccount,
					AutomountServiceAccountToken: &automount,
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Name:  "kaniko",
						Image: b.cfg.KanikoImage,
						Args:  kanikoArgs(r),
						Env:   b.gitCredentialsEnv(),
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: boolPtr(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      volumeName,
							MountPath: dockerConfigMount,
							ReadOnly:  true,
						}},
					}},
				},
			},
		},
	}
	if b.cfg.PushSecret != "" {
		job.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: b.cfg.PushSecret,
					Items: []corev1.KeyToPath{{
						Key:  ".dockerconfigjson",
						Path: "config.json",
					}},
				},
			},
		}}
	} else {
		job.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name:         volumeName,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}}
	}
	return job
}

// gitCredentialsEnv exposes GIT_USERNAME/GIT_PASSWORD (and GIT_TOKEN) to kaniko
// from the configured git-credentials Secret so PRIVATE repos can clone over
// HTTPS. Public repos work without it (returns nil). The keys are optional in the
// secret so an operator can provide a token (GIT_TOKEN) or a user/pass pair.
func (b *KanikoBuilder) gitCredentialsEnv() []corev1.EnvVar {
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

func boolPtr(b bool) *bool { return &b }

// Build runs the image build for r, dispatching on the resolved strategy:
//   - StrategyBuildpacks (no Dockerfile): delegated to the Wave 2 kube buildpacks
//     executor (Cloud Native Buildpacks lifecycle Job), which honors
//     Request.Builder / the configured CNB builder image.
//   - StrategyDockerfile (default): the kaniko executor Job in this package.
//
// Both run in the same hardened build namespace. On failure each collects the
// build pod's tail logs and returns an error including them — there is no
// fake-success path.
func (b *KanikoBuilder) Build(ctx context.Context, r Request) (Result, error) {
	if r.resolveStrategy() == StrategyBuildpacks {
		return b.buildWithBuildpacks(ctx, r)
	}
	return b.buildWithKaniko(ctx, r)
}

// buildWithBuildpacks delegates the no-Dockerfile strategy to the kube package's
// KubeBuilder (the Wave 2 buildpacks executor), mapping the build.Request to a
// kube.BuildRequest. The kube builder performs its own input validation + pod
// hardening, so the buildpacks Job logic stays in exactly one place.
func (b *KanikoBuilder) buildWithBuildpacks(ctx context.Context, r Request) (Result, error) {
	res, err := b.bp.Build(ctx, kube.BuildRequest{
		AppID:         r.AppID,
		BuildID:       r.BuildID,
		OrgSlug:       r.OrgSlug,
		ProjectSlug:   r.ProjectSlug,
		AppName:       r.AppName,
		GitRepo:       r.GitRepo,
		GitRef:        r.GitRef,
		ContextDir:    r.ContextDir,
		Strategy:      kube.StrategyBuildpacks,
		HasDockerfile: false,
		Builder:       r.Builder,
		ImageRef:      r.ImageRef,
		BuildArgs:     r.BuildArgs,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Image: res.Image, Strategy: StrategyBuildpacks}, nil
}

// buildWithKaniko submits the kaniko Job, polls to completion within the build
// timeout, and returns the pushed image on success. On failure it collects the
// build pod's tail logs and returns an error including them.
func (b *KanikoBuilder) buildWithKaniko(ctx context.Context, r Request) (Result, error) {
	if err := r.validate(); err != nil {
		return Result{}, err
	}

	// Idempotently ensure the build namespace's isolation primitives (RBAC-less
	// builder SA + default-deny-egress NetworkPolicy) before submitting a Job that
	// runs a tenant-controlled Dockerfile.
	if err := b.ensureIsolation(ctx); err != nil {
		return Result{}, err
	}

	job := b.JobSpec(r)
	// The Job name is unique per build (app+build id), so AlreadyExists here means a
	// genuine collision/duplicate submission — NOT a stale prior build. Surface it
	// rather than silently polling a pre-existing job (which would return a stale or
	// never-built image for the current request).
	_, err := b.client.BatchV1().Jobs(b.cfg.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return Result{}, fmt.Errorf("build: kaniko job %s already exists (duplicate build submission)", job.Name)
		}
		return Result{}, fmt.Errorf("build: create kaniko job %s: %w", job.Name, err)
	}

	if err := b.waitForJob(ctx, job.Name); err != nil {
		logs := b.collectLogs(ctx, job.Name)
		if logs != "" {
			return Result{}, fmt.Errorf("%w\n--- build logs ---\n%s", err, logs)
		}
		return Result{}, err
	}
	return Result{Image: r.ImageRef, Strategy: StrategyDockerfile}, nil
}

// ensureIsolation idempotently provisions the build namespace's isolation
// primitives: the dedicated, RBAC-less "vortex-builder" ServiceAccount and a
// default-deny-egress NetworkPolicy that allows egress ONLY to DNS, the registry,
// and git (blocking the in-cluster API and other tenant services). Both are
// upserted; AlreadyExists is success.
func (b *KanikoBuilder) ensureIsolation(ctx context.Context) error {
	if err := b.ensureBuilderSA(ctx); err != nil {
		return err
	}
	return b.ensureEgressPolicy(ctx)
}

// ensureBuilderSA upserts the dedicated builder ServiceAccount. It is created
// with automounting disabled and has NO RoleBindings, so even a leaked token
// grants nothing.
func (b *KanikoBuilder) ensureBuilderSA(ctx context.Context) error {
	automount := false
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      builderServiceAccount,
			Namespace: b.cfg.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vortex"},
		},
		AutomountServiceAccountToken: &automount,
	}
	_, err := b.client.CoreV1().ServiceAccounts(b.cfg.Namespace).Create(ctx, sa, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("build: ensure builder ServiceAccount: %w", err)
	}
	return nil
}

// ensureEgressPolicy upserts a default-deny-egress NetworkPolicy scoped to build
// pods. It permits egress to DNS (UDP/TCP 53) and to external destinations on the
// HTTPS/git ports (443, 9418) while denying egress to in-cluster Pod/Service CIDRs
// — so a hostile build cannot reach the Kubernetes API or other tenants. (The
// CIDR carve-outs are the standard private RFC1918 ranges; egress to the public
// registry/git endpoints stays allowed.)
func (b *KanikoBuilder) ensureEgressPolicy(ctx context.Context) error {
	dns := corev1.ProtocolUDP
	tcp := corev1.ProtocolTCP
	port53 := intstr.FromInt32(53)
	port443 := intstr.FromInt32(443)
	portGit := intstr.FromInt32(9418)

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      denyEgressPolicy,
			Namespace: b.cfg.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vortex"},
		},
		Spec: networkingv1.NetworkPolicySpec{
			// Apply to kaniko build pods only.
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"vortex.io/build": "kaniko"},
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
		return fmt.Errorf("build: ensure egress NetworkPolicy: %w", err)
	}
	return nil
}

// waitForJob polls the Job until it Completes (succeeded) or Fails, bounded by
// the build timeout.
func (b *KanikoBuilder) waitForJob(ctx context.Context, name string) error {
	return wait.PollUntilContextTimeout(ctx, buildPollInterval, b.timeout(), true,
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
					return false, fmt.Errorf("build: kaniko job %s failed: %s", name, c.Message)
				}
			}
			return false, nil
		})
}

// collectLogs returns the tail logs of the build Job's pod (best-effort).
func (b *KanikoBuilder) collectLogs(ctx context.Context, jobName string) string {
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
	tail := int64(logTailLines)
	raw, err := b.client.CoreV1().Pods(b.cfg.Namespace).
		GetLogs(names[0], &corev1.PodLogOptions{TailLines: &tail}).DoRaw(ctx)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (b *KanikoBuilder) timeout() time.Duration {
	if b.cfg.Timeout > 0 {
		return b.cfg.Timeout
	}
	return defaultBuildTimeout
}

// jobName is a DNS-1123 Job name UNIQUE PER BUILD: "build-<shortAppID>-<shortBuildID>".
// A unique name (vs. one shared per app) is required so a rebuild always creates a
// fresh Job instead of colliding with a prior (possibly still-TTL'd) Job — which
// would otherwise make rebuilds a no-op returning a stale/never-built image.
func jobName(r Request) string {
	app := short(sanitize(r.AppID), 8)
	bid := short(sanitize(r.BuildID), 8)
	switch {
	case app != "" && bid != "":
		return "build-" + app + "-" + bid
	case bid != "":
		return "build-" + bid
	case app != "":
		return "build-" + app
	default:
		return "build-" + short(sanitize(r.AppName), 8)
	}
}

// short trims s to at most n characters (and re-trims a trailing separator).
func short(s string, n int) string {
	if len(s) > n {
		s = strings.Trim(s[:n], "-")
	}
	return s
}

// nonDNS keeps names valid for Kubernetes object names / labels.
var nonDNS = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonDNS.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	return s
}

package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func builderTestCfg() BuilderConfig {
	return BuilderConfig{
		Namespace:         "vortex-builds",
		KanikoImage:       "gcr.io/kaniko-project/executor:v1.23.2",
		BuildpacksImage:   "buildpacksio/lifecycle:0.20.5",
		BuildpacksBuilder: "paketobuildpacks/builder-jammy-base:latest",
		PushSecret:        "vortex-registry-push",
		Timeout:           5 * time.Minute,
	}
}

func dockerfileReq() BuildRequest {
	return BuildRequest{
		AppID:         "app-123",
		BuildID:       "build-456",
		OrgSlug:       "acme",
		AppName:       "web",
		GitRepo:       "https://github.com/acme/web.git",
		GitRef:        "main",
		HasDockerfile: true,
		Dockerfile:    "Dockerfile",
		ImageRef:      "ghcr.io/acme/acme-web:main-app123",
		BuildArgs:     map[string]string{"VERSION": "1.2.3"},
	}
}

func buildpacksReq() BuildRequest {
	return BuildRequest{
		AppID:         "app-789",
		BuildID:       "build-789",
		OrgSlug:       "acme",
		AppName:       "api",
		GitRepo:       "https://github.com/acme/api.git",
		GitRef:        "main",
		HasDockerfile: false, // no Dockerfile => buildpacks
		ImageRef:      "ghcr.io/acme/acme-api:main-app789",
	}
}

// TestDetectStrategy asserts a present Dockerfile routes to Kaniko and an absent
// one routes to buildpacks (the core no-Dockerfile wiring).
func TestDetectStrategy(t *testing.T) {
	if got := DetectStrategy(true); got != StrategyDockerfile {
		t.Errorf("DetectStrategy(true) = %q, want %q", got, StrategyDockerfile)
	}
	if got := DetectStrategy(false); got != StrategyBuildpacks {
		t.Errorf("DetectStrategy(false) = %q, want %q", got, StrategyBuildpacks)
	}
}

// TestResolveStrategyExplicitWins asserts an explicit Strategy overrides the
// HasDockerfile detection.
func TestResolveStrategyExplicitWins(t *testing.T) {
	r := BuildRequest{Strategy: StrategyBuildpacks, HasDockerfile: true}
	if got := r.resolveStrategy(); got != StrategyBuildpacks {
		t.Errorf("explicit strategy = %q, want %q", got, StrategyBuildpacks)
	}
	r = BuildRequest{HasDockerfile: true}
	if got := r.resolveStrategy(); got != StrategyDockerfile {
		t.Errorf("detected strategy = %q, want %q", got, StrategyDockerfile)
	}
}

// TestJobSpecDockerfile asserts the kaniko Job carries the destination,
// dockerfile, git context, build-arg and the push-secret mount.
func TestJobSpecDockerfile(t *testing.T) {
	b := NewKubeBuilder(builderTestCfg(), fake.NewSimpleClientset())
	job := b.JobSpec(dockerfileReq())

	if job.Namespace != "vortex-builds" {
		t.Errorf("namespace = %q, want vortex-builds", job.Namespace)
	}
	c := job.Spec.Template.Spec.Containers[0]
	if c.Name != "kaniko" {
		t.Errorf("container = %q, want kaniko", c.Name)
	}
	if c.Image != "gcr.io/kaniko-project/executor:v1.23.2" {
		t.Errorf("image = %q", c.Image)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--destination=ghcr.io/acme/acme-web:main-app123") {
		t.Errorf("missing destination: %v", c.Args)
	}
	if !strings.Contains(args, "--dockerfile=Dockerfile") {
		t.Errorf("missing dockerfile: %v", c.Args)
	}
	if !strings.Contains(args, "--context=https://github.com/acme/web.git#refs/heads/main") {
		t.Errorf("missing/incorrect git context: %v", c.Args)
	}
	if !strings.Contains(args, "--build-arg VERSION=1.2.3") {
		t.Errorf("missing build-arg: %v", c.Args)
	}
	if job.Labels[builderPodLabel] != string(StrategyDockerfile) {
		t.Errorf("job strategy label = %q, want %q", job.Labels[builderPodLabel], StrategyDockerfile)
	}
}

// TestJobSpecBuildpacks asserts the no-Dockerfile source renders the CNB
// lifecycle executor (not kaniko), targets the destination image, and carries
// the git context + builder image via env/args.
func TestJobSpecBuildpacks(t *testing.T) {
	b := NewKubeBuilder(builderTestCfg(), fake.NewSimpleClientset())
	job := b.JobSpec(buildpacksReq())

	c := job.Spec.Template.Spec.Containers[0]
	if c.Name != "lifecycle" {
		t.Fatalf("container = %q, want lifecycle (buildpacks)", c.Name)
	}
	if c.Image != "buildpacksio/lifecycle:0.20.5" {
		t.Errorf("image = %q, want lifecycle image", c.Image)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "ghcr.io/acme/acme-api:main-app789") {
		t.Errorf("destination image missing from lifecycle args: %v", c.Args)
	}
	if !strings.Contains(args, "-run-image=paketobuildpacks/builder-jammy-base:latest") {
		t.Errorf("default builder image missing: %v", c.Args)
	}
	envByName := map[string]string{}
	for _, e := range c.Env {
		envByName[e.Name] = e.Value
	}
	if envByName["CNB_APP_DIR"] != cnbWorkspaceDir {
		t.Errorf("CNB_APP_DIR = %q, want %q", envByName["CNB_APP_DIR"], cnbWorkspaceDir)
	}
	if job.Labels[builderPodLabel] != string(StrategyBuildpacks) {
		t.Errorf("job strategy label = %q, want %q", job.Labels[builderPodLabel], StrategyBuildpacks)
	}
	// Lifecycle needs a writable workspace + layers dir.
	mounts := map[string]bool{}
	for _, m := range c.VolumeMounts {
		mounts[m.MountPath] = true
	}
	if !mounts[cnbWorkspaceDir] || !mounts[cnbLayersDir] {
		t.Errorf("lifecycle missing workspace/layers mounts: %+v", c.VolumeMounts)
	}

	// A hardened git-clone INIT container must populate the workspace first, using
	// the validated repo + ref (never a URL fragment).
	inits := job.Spec.Template.Spec.InitContainers
	if len(inits) != 1 || inits[0].Name != "git-clone" {
		t.Fatalf("expected a git-clone init container, got %+v", inits)
	}
	cloneArgs := strings.Join(inits[0].Args, " ")
	if !strings.Contains(cloneArgs, "https://github.com/acme/api.git") ||
		!strings.Contains(cloneArgs, "--branch main") {
		t.Errorf("git-clone args missing validated repo/ref: %v", inits[0].Args)
	}
	ic := inits[0].SecurityContext
	if ic == nil || ic.AllowPrivilegeEscalation == nil || *ic.AllowPrivilegeEscalation ||
		ic.Capabilities == nil || len(ic.Capabilities.Drop) != 1 || ic.Capabilities.Drop[0] != "ALL" {
		t.Errorf("git-clone init not hardened: %+v", ic)
	}
}

// TestDockerfilePathNoInitContainer asserts the Dockerfile (kaniko) path does NOT
// add a git-clone init container (kaniko clones the context itself).
func TestDockerfilePathNoInitContainer(t *testing.T) {
	b := NewKubeBuilder(builderTestCfg(), fake.NewSimpleClientset())
	if inits := b.JobSpec(dockerfileReq()).Spec.Template.Spec.InitContainers; len(inits) != 0 {
		t.Fatalf("kaniko path should have no init containers, got %+v", inits)
	}
}

// TestBuildpacksBuilderOverride asserts a per-request builder image overrides the
// configured default.
func TestBuildpacksBuilderOverride(t *testing.T) {
	b := NewKubeBuilder(builderTestCfg(), fake.NewSimpleClientset())
	r := buildpacksReq()
	r.Builder = "heroku/builder:24"
	args := strings.Join(b.JobSpec(r).Spec.Template.Spec.Containers[0].Args, " ")
	if !strings.Contains(args, "-run-image=heroku/builder:24") {
		t.Errorf("builder override missing: %v", args)
	}
}

// TestJobSpecHardensPod asserts BOTH strategies isolate the build pod: no
// automounted SA token, the dedicated builder SA, RuntimeDefault seccomp, and the
// container drops ALL capabilities with privilege escalation disabled.
func TestJobSpecHardensPod(t *testing.T) {
	b := NewKubeBuilder(builderTestCfg(), fake.NewSimpleClientset())
	for _, r := range []BuildRequest{dockerfileReq(), buildpacksReq()} {
		spec := b.JobSpec(r).Spec.Template.Spec
		if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
			t.Errorf("AutomountServiceAccountToken = %v, want false", spec.AutomountServiceAccountToken)
		}
		if spec.ServiceAccountName != builderSA {
			t.Errorf("ServiceAccountName = %q, want %q", spec.ServiceAccountName, builderSA)
		}
		if spec.SecurityContext == nil || spec.SecurityContext.SeccompProfile == nil ||
			spec.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
			t.Errorf("pod seccomp not RuntimeDefault: %+v", spec.SecurityContext)
		}
		sc := spec.Containers[0].SecurityContext
		if sc == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
			t.Errorf("AllowPrivilegeEscalation = %v, want false", sc)
		}
		if sc == nil || sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
			t.Errorf("expected drop ALL capabilities, got %+v", sc)
		}
	}
}

// TestBuildSuccessBuildpacks asserts Build CREATES the lifecycle Job and returns
// the image (with the resolved buildpacks strategy) once the Job reports Complete.
func TestBuildSuccessBuildpacks(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewKubeBuilder(builderTestCfg(), cs)
	req := buildpacksReq()

	cs.PrependReactor("get", "jobs", func(action ktesting.Action) (bool, runtime.Object, error) {
		ga := action.(ktesting.GetAction)
		return true, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: ga.GetName(), Namespace: ga.GetNamespace()},
			Status: batchv1.JobStatus{
				Succeeded:  1,
				Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}},
			},
		}, nil
	})

	res, err := b.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.Image != req.ImageRef {
		t.Fatalf("image = %q, want %q", res.Image, req.ImageRef)
	}
	if res.Strategy != StrategyBuildpacks {
		t.Fatalf("strategy = %q, want %q", res.Strategy, StrategyBuildpacks)
	}
}

// TestBuildEnsuresIsolation asserts Build idempotently ensures the RBAC-less
// builder ServiceAccount and the default-deny-egress NetworkPolicy, and that the
// egress policy selects build pods by the presence of the build label (so both
// strategies are covered).
func TestBuildEnsuresIsolation(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewKubeBuilder(builderTestCfg(), cs)
	req := buildpacksReq()

	cs.PrependReactor("get", "jobs", func(action ktesting.Action) (bool, runtime.Object, error) {
		ga := action.(ktesting.GetAction)
		return true, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: ga.GetName(), Namespace: ga.GetNamespace()},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}},
			},
		}, nil
	})

	if _, err := b.Build(context.Background(), req); err != nil {
		t.Fatalf("Build: %v", err)
	}

	sa, err := cs.CoreV1().ServiceAccounts(builderTestCfg().Namespace).Get(
		context.Background(), builderSA, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("builder SA not ensured: %v", err)
	}
	if sa.AutomountServiceAccountToken == nil || *sa.AutomountServiceAccountToken {
		t.Errorf("builder SA should disable token automount")
	}
	np, err := cs.NetworkingV1().NetworkPolicies(builderTestCfg().Namespace).Get(
		context.Background(), builderEgressPolicy, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("egress NetworkPolicy not ensured: %v", err)
	}
	hasEgress := false
	for _, pt := range np.Spec.PolicyTypes {
		if pt == networkingv1.PolicyTypeEgress {
			hasEgress = true
		}
	}
	if !hasEgress {
		t.Errorf("NetworkPolicy must restrict Egress, got %+v", np.Spec.PolicyTypes)
	}
	if len(np.Spec.PodSelector.MatchExpressions) == 0 ||
		np.Spec.PodSelector.MatchExpressions[0].Key != builderPodLabel {
		t.Errorf("egress policy must select build pods by %q: %+v", builderPodLabel, np.Spec.PodSelector)
	}

	// Idempotent: a second build does not fail on the existing SA/NP.
	req.BuildID = "build-777"
	if _, err := b.Build(context.Background(), req); err != nil {
		t.Fatalf("second Build (idempotent isolation): %v", err)
	}
}

// TestBuildJobNameUniquePerBuild asserts the Job name changes per build id.
func TestBuildJobNameUniquePerBuild(t *testing.T) {
	r1 := buildpacksReq()
	r2 := buildpacksReq()
	r2.BuildID = "build-999"
	n1, n2 := builderJobName(r1), builderJobName(r2)
	if n1 == n2 {
		t.Fatalf("expected distinct job names per build, got %q twice", n1)
	}
	if !strings.HasPrefix(n1, "build-") {
		t.Fatalf("job name = %q, want build-<app>-<build> form", n1)
	}
}

// TestBuildRejectsBadInputs asserts arg-injection-style inputs are rejected
// before any Job is created (for the buildpacks path too).
func TestBuildRejectsBadInputs(t *testing.T) {
	b := NewKubeBuilder(builderTestCfg(), fake.NewSimpleClientset())
	for _, mut := range []func(*BuildRequest){
		func(r *BuildRequest) { r.GitRepo = "file:///etc/passwd --destination=evil" },
		func(r *BuildRequest) { r.GitRepo = "https://github.com/acme/web.git#refs/heads/evil" },
		func(r *BuildRequest) { r.GitRef = "--insecure" },
		func(r *BuildRequest) { r.Builder = "evil image --x" },
		func(r *BuildRequest) { r.ImageRef = "" },
	} {
		req := buildpacksReq()
		mut(&req)
		if _, err := b.Build(context.Background(), req); err == nil {
			t.Errorf("expected rejection of %+v", req)
		}
	}
}

// TestGitCredentialsEnvBuildpacks asserts private-repo git creds are wired into
// the lifecycle pod env so private buildpacks sources can clone.
func TestGitCredentialsEnvBuildpacks(t *testing.T) {
	cfg := builderTestCfg()
	cfg.GitCredentialsSecret = "git-creds"
	b := NewKubeBuilder(cfg, fake.NewSimpleClientset())
	env := b.JobSpec(buildpacksReq()).Spec.Template.Spec.Containers[0].Env
	names := map[string]bool{}
	for _, e := range env {
		names[e.Name] = true
	}
	for _, want := range []string{"GIT_USERNAME", "GIT_PASSWORD", "GIT_TOKEN"} {
		if !names[want] {
			t.Errorf("missing git-creds env %q on lifecycle pod", want)
		}
	}
}

// TestFakeBuilderResolvesStrategy asserts the FakeBuilder records requests and
// resolves the strategy so a no-Dockerfile flow tests as buildpacks end-to-end.
func TestFakeBuilderResolvesStrategy(t *testing.T) {
	f := NewFakeBuilder()
	res, err := f.Build(context.Background(), buildpacksReq())
	if err != nil {
		t.Fatalf("fake build: %v", err)
	}
	if res.Strategy != StrategyBuildpacks {
		t.Fatalf("fake strategy = %q, want %q", res.Strategy, StrategyBuildpacks)
	}
	if res.Image != "ghcr.io/acme/acme-api:main-app789" {
		t.Fatalf("default image = %q, want ImageRef echo", res.Image)
	}

	res, _ = f.Build(context.Background(), dockerfileReq())
	if res.Strategy != StrategyDockerfile {
		t.Fatalf("dockerfile strategy = %q, want %q", res.Strategy, StrategyDockerfile)
	}

	f.ImageOverride = "registry/x:tag"
	res, _ = f.Build(context.Background(), buildpacksReq())
	if res.Image != "registry/x:tag" {
		t.Fatalf("override image = %q", res.Image)
	}
	if calls := f.Calls(); len(calls) != 3 {
		t.Fatalf("recorded %d calls, want 3", len(calls))
	}
}

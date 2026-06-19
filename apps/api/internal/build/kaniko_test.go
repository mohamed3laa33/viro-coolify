package build

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

func testCfg() Config {
	return Config{
		Namespace:   "vortex-builds",
		KanikoImage: "gcr.io/kaniko-project/executor:v1.23.2",
		PushSecret:  "vortex-registry-push",
		Timeout:     5 * time.Minute,
	}
}

func sampleReq() Request {
	return Request{
		AppID:   "app-123",
		BuildID: "build-456",
		OrgSlug: "acme",
		AppName: "web",
		GitRepo: "https://github.com/acme/web.git",
		GitRef:  "main",
		// This is a Dockerfile build: pin the strategy so Build() routes to the
		// kaniko path (an empty strategy with HasDockerfile=false would resolve to
		// buildpacks).
		Strategy:      StrategyDockerfile,
		HasDockerfile: true,
		Dockerfile:    "Dockerfile",
		ImageRef:      "ghcr.io/acme/acme-web:main-app123",
		BuildArgs:     map[string]string{"VERSION": "1.2.3"},
	}
}

// TestJobSpec asserts the kaniko Job spec carries the destination, dockerfile,
// git context and the push-secret mount.
func TestJobSpec(t *testing.T) {
	b := NewKanikoBuilder(testCfg(), fake.NewSimpleClientset())
	job := b.JobSpec(sampleReq())

	if job.Namespace != "vortex-builds" {
		t.Errorf("namespace = %q, want vortex-builds", job.Namespace)
	}
	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(job.Spec.Template.Spec.Containers))
	}
	c := job.Spec.Template.Spec.Containers[0]
	if c.Image != "gcr.io/kaniko-project/executor:v1.23.2" {
		t.Errorf("kaniko image = %q", c.Image)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--destination=ghcr.io/acme/acme-web:main-app123") {
		t.Errorf("missing destination in args: %v", c.Args)
	}
	if !strings.Contains(args, "--dockerfile=Dockerfile") {
		t.Errorf("missing dockerfile in args: %v", c.Args)
	}
	// The git context preserves the ORIGINAL https:// scheme (so private repos can
	// clone over authenticated HTTPS) and takes the ref ONLY from GitRef.
	if !strings.Contains(args, "--context=https://github.com/acme/web.git#refs/heads/main") {
		t.Errorf("missing/incorrect git context in args: %v", c.Args)
	}
	if !strings.Contains(args, "--build-arg VERSION=1.2.3") {
		t.Errorf("missing build-arg in args: %v", c.Args)
	}

	// Push secret mounted at /kaniko/.docker.
	if len(c.VolumeMounts) != 1 || c.VolumeMounts[0].MountPath != dockerConfigMount {
		t.Fatalf("expected docker-config mount at %s, got %+v", dockerConfigMount, c.VolumeMounts)
	}
	vols := job.Spec.Template.Spec.Volumes
	if len(vols) != 1 || vols[0].Secret == nil || vols[0].Secret.SecretName != "vortex-registry-push" {
		t.Fatalf("expected push-secret volume, got %+v", vols)
	}
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
}

// TestBuildSuccess asserts Build CREATES the Job (it must not already exist) and
// returns the image once the Job reports Complete. A "get jobs" reactor makes the
// poll see a Complete job without pre-creating it (which would now be an error).
func TestBuildSuccess(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewKanikoBuilder(testCfg(), cs)
	req := sampleReq()

	// On every Get of the Job, return it with a Complete condition so waitForJob
	// succeeds — without seeding the Job (Create must succeed, not AlreadyExists).
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

	// Build created the Job under the unique per-build name (visible in the
	// tracker's recorded actions, not via the reactored Get).
	created := false
	for _, a := range cs.Actions() {
		if a.GetVerb() == "create" && a.GetResource().Resource == "jobs" {
			created = true
		}
	}
	if !created {
		t.Fatalf("Build did not create a kaniko Job")
	}
}

// TestBuildJobNameUniquePerBuild asserts the Job name changes per build id, so a
// rebuild creates a fresh Job rather than colliding with a prior one.
func TestBuildJobNameUniquePerBuild(t *testing.T) {
	r1 := sampleReq()
	r2 := sampleReq()
	r2.BuildID = "build-999"
	n1, n2 := jobName(r1), jobName(r2)
	if n1 == n2 {
		t.Fatalf("expected distinct job names per build, got %q twice", n1)
	}
	if !strings.HasPrefix(n1, "build-") {
		t.Fatalf("job name = %q, want build-<app>-<build> form", n1)
	}
}

// TestBuildAlreadyExistsErrors asserts that a pre-existing Job (same name) makes
// Build return an error instead of polling the stale job.
func TestBuildAlreadyExistsErrors(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewKanikoBuilder(testCfg(), cs)
	req := sampleReq()

	// Seed the exact Job the build would create.
	if _, err := cs.BatchV1().Jobs(testCfg().Namespace).Create(
		context.Background(), b.JobSpec(req), metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	_, err := b.Build(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
}

// TestBuildHardensPod asserts the kaniko pod is isolated from the cluster API: no
// automounted SA token, the dedicated builder SA, RuntimeDefault seccomp, and the
// container drops ALL capabilities with privilege escalation disabled.
func TestBuildHardensPod(t *testing.T) {
	b := NewKanikoBuilder(testCfg(), fake.NewSimpleClientset())
	spec := b.JobSpec(sampleReq()).Spec.Template.Spec

	if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
		t.Errorf("AutomountServiceAccountToken = %v, want false", spec.AutomountServiceAccountToken)
	}
	if spec.ServiceAccountName != builderServiceAccount {
		t.Errorf("ServiceAccountName = %q, want %q", spec.ServiceAccountName, builderServiceAccount)
	}
	if spec.SecurityContext == nil || spec.SecurityContext.SeccompProfile == nil ||
		spec.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("pod seccomp profile not RuntimeDefault: %+v", spec.SecurityContext)
	}
	sc := spec.Containers[0].SecurityContext
	if sc == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Errorf("AllowPrivilegeEscalation = %v, want false", sc)
	}
	if sc == nil || sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("expected drop ALL capabilities, got %+v", sc)
	}
}

// TestBuildEnsuresIsolation asserts Build idempotently ensures the RBAC-less
// builder ServiceAccount and the default-deny-egress NetworkPolicy.
func TestBuildEnsuresIsolation(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewKanikoBuilder(testCfg(), cs)
	req := sampleReq()

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

	sa, err := cs.CoreV1().ServiceAccounts(testCfg().Namespace).Get(
		context.Background(), builderServiceAccount, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("builder SA not ensured: %v", err)
	}
	if sa.AutomountServiceAccountToken == nil || *sa.AutomountServiceAccountToken {
		t.Errorf("builder SA should disable token automount")
	}
	np, err := cs.NetworkingV1().NetworkPolicies(testCfg().Namespace).Get(
		context.Background(), denyEgressPolicy, metav1.GetOptions{})
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

	// Idempotent: a second build does not fail on the existing SA/NP.
	req.BuildID = "build-777"
	if _, err := b.Build(context.Background(), req); err != nil {
		t.Fatalf("second Build (idempotent isolation): %v", err)
	}
}

// TestKanikoArgsNoBuildArgsWhenEmpty asserts no --build-arg is emitted when the
// request carries no build args (the platform never forwards runtime env here).
func TestKanikoArgsNoBuildArgs(t *testing.T) {
	r := sampleReq()
	r.BuildArgs = nil
	args := strings.Join(kanikoArgs(r), " ")
	if strings.Contains(args, "--build-arg") {
		t.Fatalf("expected no --build-arg when BuildArgs is nil, got: %s", args)
	}
}

// TestBuildRejectsFragmentAndQuery asserts a git URL with a '#' fragment or '?'
// query is rejected (ref/query injection into the kaniko context).
func TestBuildRejectsFragmentAndQuery(t *testing.T) {
	b := NewKanikoBuilder(testCfg(), fake.NewSimpleClientset())
	for _, bad := range []string{
		"https://github.com/acme/web.git#refs/heads/evil",
		"https://github.com/acme/web.git?ref=evil",
		"https://user:pass@github.com/acme/web.git",
	} {
		req := sampleReq()
		req.GitRepo = bad
		if _, err := b.Build(context.Background(), req); err == nil {
			t.Errorf("expected rejection of %q", bad)
		}
	}
}

// TestGitCredentialsEnv asserts the configured git-creds secret is wired into the
// build pod env so private repos can clone.
func TestGitCredentialsEnv(t *testing.T) {
	cfg := testCfg()
	cfg.GitCredentialsSecret = "git-creds"
	b := NewKanikoBuilder(cfg, fake.NewSimpleClientset())
	env := b.JobSpec(sampleReq()).Spec.Template.Spec.Containers[0].Env
	names := map[string]bool{}
	for _, e := range env {
		names[e.Name] = true
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil ||
			e.ValueFrom.SecretKeyRef.Name != "git-creds" {
			t.Errorf("env %q not sourced from git-creds secret: %+v", e.Name, e)
		}
	}
	for _, want := range []string{"GIT_USERNAME", "GIT_PASSWORD", "GIT_TOKEN"} {
		if !names[want] {
			t.Errorf("missing git-creds env %q", want)
		}
	}
}

// TestBuildFailureReturnsError asserts a failed Job yields an error.
func TestBuildFailureReturnsError(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewKanikoBuilder(testCfg(), cs)
	req := sampleReq()

	job := b.JobSpec(req)
	if _, err := cs.BatchV1().Jobs(testCfg().Namespace).Create(context.Background(), job, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	job.Status.Conditions = []batchv1.JobCondition{{
		Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Message: "kaniko: build failed",
	}}
	if _, err := cs.BatchV1().Jobs(testCfg().Namespace).UpdateStatus(context.Background(), job, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("seed job status: %v", err)
	}

	if _, err := b.Build(context.Background(), req); err == nil {
		t.Fatalf("expected build error, got nil")
	}
}

// TestBuildRejectsBadRepo asserts arg-injection-style inputs are rejected before
// any Job is created.
func TestBuildRejectsBadRepo(t *testing.T) {
	b := NewKanikoBuilder(testCfg(), fake.NewSimpleClientset())
	req := sampleReq()
	req.GitRepo = "file:///etc/passwd --destination=evil"
	if _, err := b.Build(context.Background(), req); err == nil {
		t.Fatalf("expected rejection of non-https/git repo URL")
	}

	req = sampleReq()
	req.GitRef = "--insecure"
	if _, err := b.Build(context.Background(), req); err == nil {
		t.Fatalf("expected rejection of flag-like git ref")
	}
}

// TestDetectStrategy asserts the single detection point routes a Dockerfile
// source to the Kaniko strategy and a no-Dockerfile source to buildpacks.
func TestDetectStrategy(t *testing.T) {
	if got := DetectStrategy(true); got != StrategyDockerfile {
		t.Errorf("DetectStrategy(true) = %q, want %q", got, StrategyDockerfile)
	}
	if got := DetectStrategy(false); got != StrategyBuildpacks {
		t.Errorf("DetectStrategy(false) = %q, want %q", got, StrategyBuildpacks)
	}
}

// TestResolveStrategy asserts an explicit Strategy wins, and an empty Strategy
// falls back to HasDockerfile-based detection.
func TestResolveStrategy(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want Strategy
	}{
		{"explicit dockerfile", Request{Strategy: StrategyDockerfile}, StrategyDockerfile},
		{"explicit buildpacks", Request{Strategy: StrategyBuildpacks, HasDockerfile: true}, StrategyBuildpacks},
		{"detect dockerfile", Request{HasDockerfile: true}, StrategyDockerfile},
		{"detect buildpacks", Request{HasDockerfile: false}, StrategyBuildpacks},
	}
	for _, c := range cases {
		if got := c.req.resolveStrategy(); got != c.want {
			t.Errorf("%s: resolveStrategy() = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestBuildBuildpacksStrategyRoutesToLifecycle asserts a no-Dockerfile build is
// dispatched to the Cloud Native Buildpacks lifecycle Job (delegated to the kube
// builder) rather than the kaniko executor, and honors the configured builder
// image. This is the end-to-end no-Dockerfile wiring.
func TestBuildBuildpacksStrategyRoutesToLifecycle(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cfg := testCfg()
	cfg.BuildpacksBuilderImage = "paketobuildpacks/builder-jammy-base:latest"
	b := NewKanikoBuilder(cfg, cs)

	req := sampleReq()
	req.Strategy = StrategyBuildpacks
	req.HasDockerfile = false
	req.Builder = "paketobuildpacks/builder-jammy-tiny:latest"

	// Report the build Job complete on every Get so the delegated builder returns.
	cs.PrependReactor("get", "jobs", func(action ktesting.Action) (bool, runtime.Object, error) {
		ga := action.(ktesting.GetAction)
		return true, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: ga.GetName(), Namespace: ga.GetNamespace()},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}},
			},
		}, nil
	})

	res, err := b.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("Build (buildpacks): %v", err)
	}
	if res.Strategy != StrategyBuildpacks {
		t.Fatalf("result strategy = %q, want %q", res.Strategy, StrategyBuildpacks)
	}
	if res.Image != req.ImageRef {
		t.Fatalf("image = %q, want %q", res.Image, req.ImageRef)
	}

	// The created Job must be the buildpacks lifecycle (a git-clone init container +
	// a "lifecycle" build container), NOT kaniko.
	jobs, err := cs.BatchV1().Jobs(cfg.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("want exactly 1 build job, got %d", len(jobs.Items))
	}
	spec := jobs.Items[0].Spec.Template.Spec
	if len(spec.Containers) != 1 || spec.Containers[0].Name != "lifecycle" {
		t.Fatalf("expected a single 'lifecycle' container, got %+v", spec.Containers)
	}
	if len(spec.InitContainers) != 1 || spec.InitContainers[0].Name != "git-clone" {
		t.Fatalf("expected a 'git-clone' init container, got %+v", spec.InitContainers)
	}
	args := strings.Join(spec.Containers[0].Args, " ")
	if !strings.Contains(args, "paketobuildpacks/builder-jammy-tiny:latest") {
		t.Fatalf("buildpacks args missing per-request builder image: %v", spec.Containers[0].Args)
	}
}

// TestBuildDefaultsToDockerfileStrategy asserts an unset strategy WITH a detected
// Dockerfile routes to the kaniko path (back-compat for callers that set
// HasDockerfile from a probe but leave Strategy empty).
func TestBuildDefaultsToDockerfileStrategy(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewKanikoBuilder(testCfg(), cs)
	req := sampleReq()
	req.Strategy = "" // rely on detection
	req.HasDockerfile = true

	cs.PrependReactor("get", "jobs", func(action ktesting.Action) (bool, runtime.Object, error) {
		ga := action.(ktesting.GetAction)
		return true, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: ga.GetName(), Namespace: ga.GetNamespace()},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}},
			},
		}, nil
	})

	res, err := b.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.Strategy != StrategyDockerfile {
		t.Fatalf("result strategy = %q, want %q", res.Strategy, StrategyDockerfile)
	}
	jobs, _ := cs.BatchV1().Jobs(testCfg().Namespace).List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) != 1 || jobs.Items[0].Spec.Template.Spec.Containers[0].Name != "kaniko" {
		t.Fatalf("expected a single kaniko Job, got %+v", jobs.Items)
	}
}

// TestFakeBuilderRecordsAndReturns asserts the FakeBuilder double records
// requests and honors override/error.
func TestFakeBuilderRecordsAndReturns(t *testing.T) {
	f := NewFakeBuilder()
	req := sampleReq()
	res, err := f.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("fake build: %v", err)
	}
	if res.Image != req.ImageRef {
		t.Fatalf("default image = %q, want ImageRef echo", res.Image)
	}
	if res.Strategy != StrategyDockerfile {
		t.Fatalf("fake result strategy = %q, want %q", res.Strategy, StrategyDockerfile)
	}
	if calls := f.Calls(); len(calls) != 1 || calls[0].AppID != "app-123" {
		t.Fatalf("unexpected recorded calls: %+v", calls)
	}

	// A no-Dockerfile request resolves to buildpacks even through the fake.
	bp := sampleReq()
	bp.Strategy = ""
	bp.HasDockerfile = false
	if res, _ := f.Build(context.Background(), bp); res.Strategy != StrategyBuildpacks {
		t.Fatalf("fake buildpacks strategy = %q, want %q", res.Strategy, StrategyBuildpacks)
	}

	f.ImageOverride = "registry/x:tag"
	res, _ = f.Build(context.Background(), req)
	if res.Image != "registry/x:tag" {
		t.Fatalf("override image = %q", res.Image)
	}
}

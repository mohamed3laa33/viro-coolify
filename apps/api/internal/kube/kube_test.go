package kube

import (
	"context"
	"os"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"sigs.k8s.io/yaml"
)

// mockHelm captures helm invocations and the rendered values file contents.
type mockHelm struct {
	calls      [][]string // each call's args
	lastValues map[string]any
}

func (m *mockHelm) Run(_ context.Context, args ...string) (string, error) {
	m.calls = append(m.calls, args)
	// Capture the -f values file if present.
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-f" {
			data, err := os.ReadFile(args[i+1])
			if err != nil {
				return "", err
			}
			var v map[string]any
			if err := yaml.Unmarshal(data, &v); err != nil {
				return "", err
			}
			m.lastValues = v
		}
	}
	return "", nil
}

func (m *mockHelm) last() []string {
	if len(m.calls) == 0 {
		return nil
	}
	return m.calls[len(m.calls)-1]
}

func testConfig() Config {
	return Config{
		BaseDomain:             "vortex.v60ai.com",
		ChartPath:              "deploy/charts/common-chart",
		GatewayName:            "vortex",
		GatewayNamespace:       "vortex-system",
		CPUOvercommitFactor:    0.2,
		MemoryOvercommitFactor: 0.35,
	}
}

func TestEnsureTenantCreatesNamespaceQuotaLimitRange(t *testing.T) {
	cs := fake.NewSimpleClientset()
	// Pre-create the namespace in Active phase so polling succeeds immediately.
	ns := namespaceName("acme", "web")
	_, _ = cs.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}, metav1.CreateOptions{})

	b := NewWithClient(testConfig(), cs, &mockHelm{})
	got, err := b.EnsureTenant(context.Background(), "acme", "web", Quota{MaxCPU: 8, MaxMemoryMB: 16384, MaxApps: 10})
	if err != nil {
		t.Fatalf("EnsureTenant: %v", err)
	}
	if got != "vortex-acme-web" {
		t.Fatalf("namespace = %q, want vortex-acme-web", got)
	}

	rq, err := cs.CoreV1().ResourceQuotas(ns).Get(context.Background(), "vortex-quota", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get resourcequota: %v", err)
	}
	if v := rq.Spec.Hard[corev1.ResourceLimitsCPU]; v.MilliValue() != 8000 {
		t.Errorf("quota limits.cpu = %q (%d milli), want 8 cores", v.String(), v.MilliValue())
	}
	if v := rq.Spec.Hard[corev1.ResourceLimitsMemory]; v.Value() != 16384*1024*1024 {
		t.Errorf("quota limits.memory = %q, want 16Gi", v.String())
	}
	if v := rq.Spec.Hard[corev1.ResourcePods]; v.Value() != 10 {
		t.Errorf("quota pods = %d, want 10", v.Value())
	}

	if _, err := cs.CoreV1().LimitRanges(ns).Get(context.Background(), "vortex-limits", metav1.GetOptions{}); err != nil {
		t.Fatalf("get limitrange: %v", err)
	}
}

func TestEnsureTenantIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ns := namespaceName("acme", "web")
	_, _ = cs.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}, metav1.CreateOptions{})
	b := NewWithClient(testConfig(), cs, &mockHelm{})
	q := Quota{MaxCPU: 4, MaxMemoryMB: 8192, MaxApps: 5}
	if _, err := b.EnsureTenant(context.Background(), "acme", "web", q); err != nil {
		t.Fatalf("first EnsureTenant: %v", err)
	}
	if _, err := b.EnsureTenant(context.Background(), "acme", "web", q); err != nil {
		t.Fatalf("second EnsureTenant (idempotent): %v", err)
	}
}

func TestApplyOvercommitAndHost(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mh := &mockHelm{}
	b := NewWithClient(testConfig(), cs, mh)

	rel, h, err := b.Apply(context.Background(), Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "ghcr.io/acme/api:1.2.3", CPU: 1.0, MemoryMB: 1024,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rel != "api" {
		t.Errorf("release = %q, want api", rel)
	}
	if h != "api.web.acme.vortex.v60ai.com" {
		t.Errorf("host = %q, want api.web.acme.vortex.v60ai.com", h)
	}

	args := mh.last()
	wantPrefix := []string{"upgrade", "--install", "api", "deploy/charts/common-chart", "-n", "vortex-acme-web", "--create-namespace"}
	for i, w := range wantPrefix {
		if i >= len(args) || args[i] != w {
			t.Fatalf("helm args = %v, want prefix %v", args, wantPrefix)
		}
	}

	dep := mh.lastValues["deployment"].(map[string]any)
	res := dep["resources"].(map[string]any)
	req := res["requests"].(map[string]any)
	lim := res["limits"].(map[string]any)
	if req["cpu"] != "200m" {
		t.Errorf("requests.cpu = %v, want 200m", req["cpu"])
	}
	if lim["cpu"] != "1000m" {
		t.Errorf("limits.cpu = %v, want 1000m", lim["cpu"])
	}
	if req["memory"] != "358Mi" {
		t.Errorf("requests.memory = %v, want 358Mi", req["memory"])
	}
	if lim["memory"] != "1024Mi" {
		t.Errorf("limits.memory = %v, want 1024Mi", lim["memory"])
	}

	img := dep["image"].(map[string]any)
	if img["repository"] != "ghcr.io/acme/api" || img["tag"] != "1.2.3" {
		t.Errorf("image = %v:%v, want ghcr.io/acme/api:1.2.3", img["repository"], img["tag"])
	}

	gw := mh.lastValues["gateway"].(map[string]any)
	hostnames := gw["hostnames"].([]any)
	if len(hostnames) == 0 || hostnames[0] != "api.web.acme.vortex.v60ai.com" {
		t.Errorf("gateway.hostnames = %v, want [api.web.acme.vortex.v60ai.com]", hostnames)
	}
	parents := gw["parentRefs"].([]any)
	pr := parents[0].(map[string]any)
	if pr["name"] != "vortex" || pr["namespace"] != "vortex-system" {
		t.Errorf("gateway.parentRefs[0] = %v, want vortex/vortex-system", pr)
	}

	keda := mh.lastValues["keda"].(map[string]any)
	if keda["enabled"] != true {
		t.Errorf("keda.enabled = %v, want true", keda["enabled"])
	}
}

func TestApplyWordPress(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mh := &mockHelm{}
	b := NewWithClient(testConfig(), cs, mh)

	_, _, err := b.Apply(context.Background(), Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "blog", Kind: "service",
		ServiceTemplateKey: "wordpress", Image: "ignored:tag", CPU: 1, MemoryMB: 2048,
		Env: map[string]string{
			"WORDPRESS_DB_HOST":     "db:3306",
			"WORDPRESS_DB_NAME":     "blog",
			"WORDPRESS_DB_USER":     "blog",
			"WORDPRESS_DB_PASSWORD": "s3cret",
			"WP_HOME":               "https://blog.web.acme.vortex.v60ai.com",
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	dep := mh.lastValues["deployment"].(map[string]any)
	img := dep["image"].(map[string]any)
	if img["repository"] != "wordpress" || img["tag"] != "6.8-php8.3-apache" {
		t.Errorf("wordpress image = %v:%v, want wordpress:6.8-php8.3-apache", img["repository"], img["tag"])
	}
	env := dep["env"].(map[string]any)
	extra, _ := env["WORDPRESS_CONFIG_EXTRA"].(string)
	if !strings.Contains(extra, "DB_PASSWORD") || !strings.Contains(extra, "s3cret") {
		t.Errorf("WORDPRESS_CONFIG_EXTRA missing forced DB creds: %q", extra)
	}
	if _, ok := dep["livenessProbe"]; !ok {
		t.Error("wordpress should set a (TCP) livenessProbe")
	}
	if _, ok := dep["readinessProbe"]; !ok {
		t.Error("wordpress should set a (static-file) readinessProbe")
	}
}

func TestApplyDatabaseIsStateful(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mh := &mockHelm{}
	b := NewWithClient(testConfig(), cs, mh)
	_, _, err := b.Apply(context.Background(), Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "pg", Kind: "database",
		ServiceTemplateKey: "postgresql", Image: "postgres:16", CPU: 1, MemoryMB: 1024,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	dep := mh.lastValues["deployment"].(map[string]any)
	if dep["stateful"] != true {
		t.Errorf("database deployment.stateful = %v, want true", dep["stateful"])
	}
	keda := mh.lastValues["keda"].(map[string]any)
	if keda["scaleTargetKind"] != "StatefulSet" {
		t.Errorf("keda.scaleTargetKind = %v, want StatefulSet", keda["scaleTargetKind"])
	}
}

func TestStopScalesDeploymentToZero(t *testing.T) {
	one := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "vortex-acme-web"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	}
	cs := fake.NewSimpleClientset(dep)
	b := NewWithClient(testConfig(), cs, &mockHelm{})

	if err := b.Stop(context.Background(), "vortex-acme-web", "api"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, _ := cs.AppsV1().Deployments("vortex-acme-web").Get(context.Background(), "api", metav1.GetOptions{})
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("replicas = %v, want 0", got.Spec.Replicas)
	}

	if err := b.Start(context.Background(), "vortex-acme-web", "api"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, _ = cs.AppsV1().Deployments("vortex-acme-web").Get(context.Background(), "api", metav1.GetOptions{})
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 1 {
		t.Errorf("replicas after Start = %v, want 1", got.Spec.Replicas)
	}
}

func TestStatusFromDeployment(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "vortex-acme-web"},
		Status:     appsv1.DeploymentStatus{Replicas: 3, ReadyReplicas: 3},
	}
	cs := fake.NewSimpleClientset(dep)
	b := NewWithClient(testConfig(), cs, &mockHelm{})
	st, err := b.Status(context.Background(), "vortex-acme-web", "api")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Phase != "Running" || st.Replicas != 3 || st.ReadyReplicas != 3 {
		t.Errorf("status = %+v, want Running 3/3", st)
	}
}

func TestLogsReadsPod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-abc",
			Namespace: "vortex-acme-web",
			Labels:    map[string]string{"app.kubernetes.io/instance": "api"},
		},
	}
	cs := fake.NewSimpleClientset(pod)
	b := NewWithClient(testConfig(), cs, &mockHelm{})
	// The fake clientset returns a canned "fake logs" body for GetLogs.
	out, err := b.Logs(context.Background(), "vortex-acme-web", "api", 100)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty logs from fake clientset")
	}
}

func TestDeleteUninstalls(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mh := &mockHelm{}
	b := NewWithClient(testConfig(), cs, mh)
	if err := b.Delete(context.Background(), "vortex-acme-web", "api"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	args := mh.last()
	want := []string{"uninstall", "api", "-n", "vortex-acme-web"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Errorf("helm args = %v, want %v", args, want)
	}
}

func TestOvercommitMath(t *testing.T) {
	res := overcommitResources(1.0, 1024, 0.2, 0.35)
	req := res["requests"].(map[string]any)
	lim := res["limits"].(map[string]any)
	if req["cpu"] != "200m" || lim["cpu"] != "1000m" {
		t.Errorf("cpu req/lim = %v/%v, want 200m/1000m", req["cpu"], lim["cpu"])
	}
	if req["memory"] != "358Mi" || lim["memory"] != "1024Mi" {
		t.Errorf("mem req/lim = %v/%v, want 358Mi/1024Mi", req["memory"], lim["memory"])
	}
}

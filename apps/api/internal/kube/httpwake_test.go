package kube

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// httpWakeListKinds registers the HTTPScaledObject GVR's List kind for the dynamic
// fake so Create/Get/List target the exact GVR the backend uses.
func httpWakeListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		httpScaledObjectGVR: "HTTPScaledObjectList",
	}
}

func newHTTPWakeDyn() *fake.FakeDynamicClient {
	return fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), httpWakeListKinds())
}

// httpWakeScaling is the resolved Scaling for an HTTP/web app that opts into
// scale-to-zero WAKE: HTTPTrigger on and a zero floor.
func httpWakeScaling() Scaling {
	return Scaling{MinReplicas: 0, MaxReplicas: 6, PollingInterval: 30, CooldownPeriod: 120, CPUUtilization: 70, HTTPTrigger: true}
}

func TestWantsHTTPWake(t *testing.T) {
	cases := []struct {
		name     string
		w        Workload
		stateful bool
		want     bool
	}{
		{"http app scale-to-zero", Workload{Kind: "app", Scaling: httpWakeScaling()}, false, true},
		{"http trigger but non-zero floor", Workload{Kind: "app", Scaling: Scaling{HTTPTrigger: true, MinReplicas: 1, MaxReplicas: 5}}, false, false},
		{"worker (no http trigger)", Workload{Kind: "app", Scaling: Scaling{MinReplicas: 0, MaxReplicas: 5}}, false, false},
		{"database never wakes via http", Workload{Kind: "database", Scaling: httpWakeScaling()}, true, false},
	}
	for _, c := range cases {
		if got := wantsHTTPWake(c.w, c.stateful); got != c.want {
			t.Errorf("%s: wantsHTTPWake = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestBuildValuesHTTPWakeDisablesChartScaledObjectAndRoutesThroughInterceptor
// asserts an HTTP/web app with scale-to-zero disables the chart's CPU ScaledObject
// and points the HTTPRoute backendRef at the interceptor proxy Service.
func TestBuildValuesHTTPWakeDisablesChartScaledObjectAndRoutesThroughInterceptor(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	b := NewWithClient(testConfig(), cs, &mockHelm{})

	vals := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256, Port: 3000,
		Scaling: httpWakeScaling(),
	}, "api.web.acme.vortex.v60ai.com")

	keda := vals["keda"].(map[string]any)
	if keda["enabled"] != false {
		t.Errorf("keda.enabled = %v, want false for an HTTP-wake app (HTTPScaledObject owns it)", keda["enabled"])
	}

	gw := vals["gateway"].(map[string]any)
	rules, ok := gw["rules"].([]map[string]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("gateway.rules = %v, want 1 interceptor rule", gw["rules"])
	}
	backendRefs := rules[0]["backendRefs"].([]map[string]any)
	br := backendRefs[0]
	if br["name"] != defaultHTTPInterceptorService {
		t.Errorf("backendRef.name = %v, want %s", br["name"], defaultHTTPInterceptorService)
	}
	if br["namespace"] != defaultHTTPInterceptorNamespace {
		t.Errorf("backendRef.namespace = %v, want %s", br["namespace"], defaultHTTPInterceptorNamespace)
	}
	if br["port"] != int64(defaultHTTPInterceptorPort) {
		t.Errorf("backendRef.port = %v, want %d", br["port"], defaultHTTPInterceptorPort)
	}
}

// TestBuildValuesNonHTTPKeepsChartScaledObject asserts a worker / non-HTTP app
// keeps the chart's CPU ScaledObject and the default app-Service backendRef.
func TestBuildValuesNonHTTPKeepsChartScaledObject(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	b := NewWithClient(testConfig(), cs, &mockHelm{})

	vals := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "worker", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256,
		Scaling: Scaling{MinReplicas: 0, MaxReplicas: 5}, // no HTTPTrigger
	}, "worker.web.acme.vortex.v60ai.com")

	keda := vals["keda"].(map[string]any)
	if keda["enabled"] != true {
		t.Errorf("keda.enabled = %v, want true for a non-HTTP app (chart ScaledObject)", keda["enabled"])
	}
	gw := vals["gateway"].(map[string]any)
	if _, ok := gw["rules"]; ok {
		t.Errorf("gateway.rules set for a non-HTTP app; want default app-Service backendRef (no rules)")
	}
}

// TestBuildKedaNoLongerEmitsInertHTTPTrigger asserts buildKeda emits only the CPU
// trigger even when HTTPTrigger is set (HTTP wake now flows through the
// HTTPScaledObject, not an inert ScaledObject trigger).
func TestBuildKedaNoLongerEmitsInertHTTPTrigger(t *testing.T) {
	keda := buildKeda(httpWakeScaling(), false)
	triggers := keda["triggers"].([]map[string]any)
	if len(triggers) != 1 {
		t.Fatalf("triggers = %v, want exactly the CPU trigger", triggers)
	}
	if triggers[0]["type"] != "cpu" {
		t.Errorf("trigger[0].type = %v, want cpu", triggers[0]["type"])
	}
}

// TestApplyEnsuresHTTPScaledObjectForWakeApp asserts Apply creates an
// HTTPScaledObject (keyed on the app FQDN, targeting the app Deployment/Service)
// for an HTTP/web app that scales to zero.
func TestApplyEnsuresHTTPScaledObjectForWakeApp(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newHTTPWakeDyn()
	b := NewWithClients(testConfig(), cs, dc, &mockHelm{})

	if _, _, err := b.Apply(context.Background(), Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256, Port: 3000,
		Scaling: httpWakeScaling(),
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := dc.Resource(httpScaledObjectGVR).Namespace("vortex-acme-web").
		Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get HTTPScaledObject: %v", err)
	}
	hosts, _, _ := unstructured.NestedStringSlice(got.Object, "spec", "hosts")
	if len(hosts) != 1 || hosts[0] != "api.web.acme.vortex.v60ai.com" {
		t.Errorf("HTTPScaledObject hosts = %v, want [api.web.acme.vortex.v60ai.com]", hosts)
	}
	target, _, _ := unstructured.NestedString(got.Object, "spec", "scaleTargetRef", "name")
	if target != "api" {
		t.Errorf("scaleTargetRef.name = %q, want api", target)
	}
	port, _, _ := unstructured.NestedInt64(got.Object, "spec", "scaleTargetRef", "port")
	if port != 3000 {
		t.Errorf("scaleTargetRef.port = %d, want 3000", port)
	}
	minR, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas", "min")
	if minR != 0 {
		t.Errorf("replicas.min = %d, want 0 (scale-to-zero)", minR)
	}

	// Idempotent: re-Apply updates rather than errors.
	if _, _, err := b.Apply(context.Background(), Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:2", CPU: 1, MemoryMB: 256, Port: 3000,
		Scaling: httpWakeScaling(),
	}); err != nil {
		t.Fatalf("Apply (repeat): %v", err)
	}
}

// TestApplyRemovesStaleHTTPScaledObjectWhenWakeDisabled asserts that switching an
// app OFF HTTP wake (e.g. a non-zero floor) removes its HTTPScaledObject so it is
// no longer routed through the interceptor / managed by the add-on.
func TestApplyRemovesStaleHTTPScaledObjectWhenWakeDisabled(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newHTTPWakeDyn()
	b := NewWithClients(testConfig(), cs, dc, &mockHelm{})
	ctx := context.Background()

	// First deploy with HTTP wake -> HTTPScaledObject exists.
	if _, _, err := b.Apply(ctx, Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256, Scaling: httpWakeScaling(),
	}); err != nil {
		t.Fatalf("Apply (wake): %v", err)
	}
	if _, err := dc.Resource(httpScaledObjectGVR).Namespace("vortex-acme-web").
		Get(ctx, "api", metav1.GetOptions{}); err != nil {
		t.Fatalf("expected HTTPScaledObject after wake Apply: %v", err)
	}

	// Re-deploy with a non-zero floor (wake off) -> HTTPScaledObject removed.
	off := httpWakeScaling()
	off.MinReplicas = 2
	if _, _, err := b.Apply(ctx, Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256, Scaling: off,
	}); err != nil {
		t.Fatalf("Apply (wake off): %v", err)
	}
	if _, err := dc.Resource(httpScaledObjectGVR).Namespace("vortex-acme-web").
		Get(ctx, "api", metav1.GetOptions{}); err == nil {
		t.Errorf("HTTPScaledObject should be removed when HTTP wake is disabled")
	}
}

// TestApplyNoHTTPScaledObjectWithoutDynamicClient asserts the wake wiring no-ops
// (no error) on a backend with no dynamic client (local/dev).
func TestApplyNoHTTPScaledObjectWithoutDynamicClient(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	b := NewWithClient(testConfig(), cs, &mockHelm{}) // no dynamic client
	if _, _, err := b.Apply(context.Background(), Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256, Scaling: httpWakeScaling(),
	}); err != nil {
		t.Fatalf("Apply with no dynamic client should be nil: %v", err)
	}
}

package kube

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// dynScheme builds a runtime scheme that maps the KEDA ScaledObject GVR to an
// unstructured list kind, which the dynamic fake client requires.
func dynScheme() *runtime.Scheme {
	sch := runtime.NewScheme()
	sch.AddKnownTypeWithName(
		scaledObjectGVR.GroupVersion().WithKind("ScaledObject"),
		&unstructured.Unstructured{},
	)
	sch.AddKnownTypeWithName(
		scaledObjectGVR.GroupVersion().WithKind("ScaledObjectList"),
		&unstructured.UnstructuredList{},
	)
	return sch
}

func newScaledObject(ns, name string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(scaledObjectGVR.GroupVersion().WithKind("ScaledObject"))
	o.SetNamespace(ns)
	o.SetName(name)
	return o
}

func TestStopPausesKedaAndScalesToZero(t *testing.T) {
	one := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "vortex-acme-web"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	}
	cs := k8sfake.NewSimpleClientset(dep)
	so := newScaledObject("vortex-acme-web", "api")
	dc := fake.NewSimpleDynamicClient(dynScheme(), so)
	b := NewWithClients(testConfig(), cs, dc, &mockHelm{})

	if err := b.Stop(context.Background(), "vortex-acme-web", "api"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// ScaledObject paused-replicas annotation set to "0".
	got, err := dc.Resource(scaledObjectGVR).Namespace("vortex-acme-web").
		Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get scaledobject: %v", err)
	}
	if v := got.GetAnnotations()[kedaPausedAnnotation]; v != "0" {
		t.Errorf("paused-replicas annotation = %q, want 0", v)
	}

	// Deployment scaled to zero.
	d, _ := cs.AppsV1().Deployments("vortex-acme-web").Get(context.Background(), "api", metav1.GetOptions{})
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 0 {
		t.Errorf("replicas = %v, want 0", d.Spec.Replicas)
	}
}

func TestStartResumesKedaAndScalesToOne(t *testing.T) {
	zero := int32(0)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "vortex-acme-web"},
		Spec:       appsv1.DeploymentSpec{Replicas: &zero},
	}
	cs := k8sfake.NewSimpleClientset(dep)
	so := newScaledObject("vortex-acme-web", "api")
	so.SetAnnotations(map[string]string{kedaPausedAnnotation: "0"})
	dc := fake.NewSimpleDynamicClient(dynScheme(), so)
	b := NewWithClients(testConfig(), cs, dc, &mockHelm{})

	if err := b.Start(context.Background(), "vortex-acme-web", "api"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got, _ := dc.Resource(scaledObjectGVR).Namespace("vortex-acme-web").
		Get(context.Background(), "api", metav1.GetOptions{})
	if _, ok := got.GetAnnotations()[kedaPausedAnnotation]; ok {
		t.Errorf("paused-replicas annotation still present after Start: %v", got.GetAnnotations())
	}
	d, _ := cs.AppsV1().Deployments("vortex-acme-web").Get(context.Background(), "api", metav1.GetOptions{})
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 1 {
		t.Errorf("replicas = %v, want 1", d.Spec.Replicas)
	}
}

func TestStopFallsBackWhenNoScaledObject(t *testing.T) {
	one := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "vortex-acme-web"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	}
	cs := k8sfake.NewSimpleClientset(dep)
	dc := fake.NewSimpleDynamicClient(dynScheme()) // no ScaledObject
	b := NewWithClients(testConfig(), cs, dc, &mockHelm{})

	if err := b.Stop(context.Background(), "vortex-acme-web", "api"); err != nil {
		t.Fatalf("Stop with no ScaledObject should fall back, got: %v", err)
	}
	d, _ := cs.AppsV1().Deployments("vortex-acme-web").Get(context.Background(), "api", metav1.GetOptions{})
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 0 {
		t.Errorf("replicas = %v, want 0", d.Spec.Replicas)
	}
}

func TestApplyUsesWaitAtomicTimeout(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	mh := &mockHelm{}
	cfg := testConfig()
	b := NewWithClient(cfg, cs, mh)
	if _, _, err := b.Apply(context.Background(), Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	args := strings.Join(mh.last(), " ")
	for _, want := range []string{"--wait", "--atomic", "--timeout"} {
		if !strings.Contains(args, want) {
			t.Errorf("helm args %q missing %q", args, want)
		}
	}
	if !strings.Contains(args, defaultHelmTimeout.String()) {
		t.Errorf("helm args %q missing default timeout %q", args, defaultHelmTimeout.String())
	}
}

func TestApplyDefaultProbesForApps(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	mh := &mockHelm{}
	b := NewWithClient(testConfig(), cs, mh)
	if _, _, err := b.Apply(context.Background(), Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	dep := mh.lastValues["deployment"].(map[string]any)
	rp, ok := dep["readinessProbe"].(map[string]any)
	if !ok {
		t.Fatal("expected a default readinessProbe for an app workload")
	}
	if _, ok := rp["tcpSocket"]; !ok {
		t.Errorf("default readinessProbe should be a TCP probe, got %v", rp)
	}
	if _, ok := dep["livenessProbe"]; !ok {
		t.Error("expected a default livenessProbe for an app workload")
	}
}

func TestBuildValuesSetsFullnameOverrideToReleaseName(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	b := NewWithClient(testConfig(), cs, &mockHelm{})

	// The chart's fullname template would otherwise render objects as
	// "<release>-common-chart"; forcing fullnameOverride to the release name makes
	// the bare-name lifecycle Get/Patch calls correct on a real cluster.
	vals := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "Web App", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256,
	}, "web-app.web.acme.vortex.v60ai.com")

	got, ok := vals["fullnameOverride"].(string)
	if !ok {
		t.Fatalf("fullnameOverride missing or not a string: %v", vals["fullnameOverride"])
	}
	if want := releaseName("Web App"); got != want {
		t.Errorf("fullnameOverride = %q, want %q (the release name)", got, want)
	}
}

// errNotFoundHelm is a HelmRunner that fails with a "release: not found" error.
type errNotFoundHelm struct{}

func (errNotFoundHelm) Run(_ context.Context, _ ...string) (string, error) {
	return "", errors.New(`uninstall: release: not found`)
}

func TestDeleteIdempotentOnNotFound(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	b := NewWithClient(testConfig(), cs, errNotFoundHelm{})
	if err := b.Delete(context.Background(), "vortex-acme-web", "gone"); err != nil {
		t.Fatalf("Delete on already-absent release should be nil, got: %v", err)
	}
}

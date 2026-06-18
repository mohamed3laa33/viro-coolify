package kube

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// metricsScheme builds an empty scheme; the dynamic fake is created with an
// explicit GVR->ListKind map (NewSimpleDynamicClientWithCustomListKinds) so the
// metrics.k8s.io "pods" resource lists correctly.
func metricsScheme() *runtime.Scheme {
	return runtime.NewScheme()
}

// metricsListKinds maps the PodMetrics GVR to its List kind for the dynamic fake.
func metricsListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		podMetricsGVR: "PodMetricsList",
	}
}

// seedPodMetrics builds an unstructured metrics.k8s.io/v1beta1 PodMetrics object
// carrying one container's usage.
func seedPodMetrics(ns, name, instance, cpu, mem string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "metrics.k8s.io/v1beta1",
		"kind":       "PodMetrics",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels":    map[string]any{"app.kubernetes.io/instance": instance},
		},
		"containers": []any{
			map[string]any{
				"name":  "app",
				"usage": map[string]any{"cpu": cpu, "memory": mem},
			},
		},
	}}
	o.SetGroupVersionKind(podMetricsGVR.GroupVersion().WithKind("PodMetrics"))
	return o
}

func TestMetricsReadsLivePodMetrics(t *testing.T) {
	ns := "vortex-acme-web"
	cs := k8sfake.NewSimpleClientset()
	dc := dynfake.NewSimpleDynamicClientWithCustomListKinds(metricsScheme(), metricsListKinds())
	// Seed PodMetrics under the EXACT GVR the backend lists ("pods" in
	// metrics.k8s.io); the default GVK->GVR pluralizer would otherwise key them
	// under "podmetricses" and the List on "pods" would find nothing.
	for _, o := range []*unstructured.Unstructured{
		seedPodMetrics(ns, "api-1", "api", "150m", "32Mi"),
		seedPodMetrics(ns, "api-2", "api", "100m", "16Mi"),
		// A different release's pod must NOT be selected.
		seedPodMetrics(ns, "other-1", "other", "999m", "999Mi"),
	} {
		if err := dc.Tracker().Create(podMetricsGVR, o, ns); err != nil {
			t.Fatalf("seed pod metrics: %v", err)
		}
	}
	b := NewWithClients(testConfig(), cs, dc, &mockHelm{})

	m, err := b.Metrics(context.Background(), ns, "api")
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if !m.Available {
		t.Fatalf("expected Available, got %+v", m)
	}
	if len(m.Pods) != 2 {
		t.Fatalf("expected 2 pods, got %d (%+v)", len(m.Pods), m.Pods)
	}
	if m.CPUMillicores != 250 {
		t.Errorf("aggregate cpu = %dm, want 250m", m.CPUMillicores)
	}
	if m.MemoryBytes != (32+16)*1024*1024 {
		t.Errorf("aggregate mem = %dB, want %dB", m.MemoryBytes, (32+16)*1024*1024)
	}
}

// TestMetricsUnavailableWhenNoDynamicClient asserts an HONEST unavailable result
// (never fabricated numbers) when the metrics-server path is not wired.
func TestMetricsUnavailableWhenNoDynamicClient(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	b := NewWithClient(testConfig(), cs, &mockHelm{}) // no dynamic client
	m, err := b.Metrics(context.Background(), "vortex-acme-web", "api")
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if m.Available || m.Unavailable == "" {
		t.Fatalf("expected unavailable snapshot, got %+v", m)
	}
}

// TestParsePodMetricSumsContainers asserts multi-container usage is summed.
func TestParsePodMetricSumsContainers(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "p"},
		"containers": []any{
			map[string]any{"usage": map[string]any{"cpu": "100m", "memory": "10Mi"}},
			map[string]any{"usage": map[string]any{"cpu": "250m", "memory": "20Mi"}},
		},
	}}
	pm := parsePodMetric(u)
	if pm.CPUMillicores != 350 {
		t.Errorf("cpu = %dm, want 350m", pm.CPUMillicores)
	}
	if pm.MemoryBytes != 30*1024*1024 {
		t.Errorf("mem = %dB, want %dB", pm.MemoryBytes, 30*1024*1024)
	}
}

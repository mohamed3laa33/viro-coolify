package kube

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// podMetricsGVR is the metrics-server PodMetrics resource. We read it via the
// existing dynamic client so the control plane takes NO dependency on the
// k8s.io/metrics module (constraint: stdlib + existing deps only).
var podMetricsGVR = schema.GroupVersionResource{
	Group:    "metrics.k8s.io",
	Version:  "v1beta1",
	Resource: "pods",
}

// Metrics reads the live per-pod CPU/memory usage for a release from the
// metrics-server, selecting the workload pods by the same instance label the rest
// of the backend uses. The data is REAL (no synthetic fallback): when the
// metrics-server API is not installed/served, it returns
// WorkloadMetrics{Available:false, Unavailable:<reason>} so the caller surfaces an
// honest "unavailable" rather than fabricating numbers.
func (b *KubeBackend) Metrics(ctx context.Context, namespace, release string) (WorkloadMetrics, error) {
	if b.dynamic == nil {
		return WorkloadMetrics{Available: false, Unavailable: "metrics-server not configured"}, nil
	}
	list, err := b.dynamic.Resource(podMetricsGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + release,
	})
	if err != nil {
		// A missing metrics.k8s.io API (metrics-server not installed) is NOT an
		// error condition for the caller — report it honestly as unavailable.
		if errors.IsNotFound(err) || isNoMatchError(err) {
			return WorkloadMetrics{Available: false, Unavailable: "metrics-server unavailable"}, nil
		}
		return WorkloadMetrics{}, fmt.Errorf("kube: list pod metrics for %s/%s: %w", namespace, release, err)
	}

	out := WorkloadMetrics{Available: true, Pods: make([]PodMetric, 0, len(list.Items))}
	for i := range list.Items {
		pm := parsePodMetric(&list.Items[i])
		out.Pods = append(out.Pods, pm)
		out.CPUMillicores += pm.CPUMillicores
		out.MemoryBytes += pm.MemoryBytes
	}
	return out, nil
}

// parsePodMetric extracts the summed container CPU (millicores) and memory
// (bytes) from one unstructured metrics.k8s.io/v1beta1 PodMetrics object. The
// schema is: metadata.name + containers[].usage.{cpu,memory} as Quantity strings.
func parsePodMetric(u *unstructured.Unstructured) PodMetric {
	pm := PodMetric{Pod: u.GetName()}
	containers, found, err := unstructured.NestedSlice(u.Object, "containers")
	if err != nil || !found {
		return pm
	}
	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		usage, ok := cm["usage"].(map[string]any)
		if !ok {
			continue
		}
		if cpu, ok := usage["cpu"].(string); ok {
			if q, qErr := resource.ParseQuantity(cpu); qErr == nil {
				pm.CPUMillicores += q.MilliValue()
			}
		}
		if mem, ok := usage["memory"].(string); ok {
			if q, qErr := resource.ParseQuantity(mem); qErr == nil {
				pm.MemoryBytes += q.Value()
			}
		}
	}
	return pm
}

// isNoMatchError reports whether err is a REST "no matches for kind" error, which
// the dynamic client returns when the metrics.k8s.io API group is not served
// (metrics-server not installed). It is matched by message because the dynamic
// client surfaces it as a generic *meta.NoResourceMatchError / *meta.NoKindMatchError
// without a typed sentinel we can errors.Is against cheaply.
func isNoMatchError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no matches for kind") ||
		strings.Contains(msg, "could not find the requested resource") ||
		strings.Contains(msg, "the server could not find the requested resource")
}

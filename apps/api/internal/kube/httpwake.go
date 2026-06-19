package kube

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// KEDA HTTP add-on (scale-to-zero WAKE on request)
// ---------------------------------------------------------------------------
//
// The core `keda` release ships only the CPU/memory scalers, so a ScaledObject's
// `http` trigger is INERT: nothing routes the inbound request through KEDA, so a
// workload that has scaled to zero can never be WOKEN by traffic. The
// keda-add-ons-http release (deploy/helmfile.yaml) supplies the missing pieces:
//
//   - an INTERCEPTOR proxy Service that buffers an inbound request while the
//     target scales 0->1 and replays it once a replica is Ready, and
//   - the EXTERNAL SCALER KEDA consults for the pending-request count.
//
// The wiring (no manual kubectl — invariant #5): for an HTTP/web app that opts
// into scale-to-zero we render, per app, an HTTPScaledObject (the CRD owned by the
// add-on) keyed on the app's FQDN(s). The add-on creates the underlying KEDA
// ScaledObject with the working `http` external trigger, so we DISABLE the chart's
// own CPU ScaledObject for that app (two ScaledObjects fighting over one target is
// invalid) and point the tenant HTTPRoute backendRef at the interceptor proxy
// Service instead of the app's own Service. Traffic then flows
// Gateway -> interceptor -> (wake 0->1) -> app.
//
// Non-HTTP / worker apps (and HTTP apps that do NOT scale to zero) keep the plain
// CPU-based ScaledObject the chart renders from buildKeda — they are scaled by CPU
// utilization and never need the request-path interceptor.

// httpScaledObjectGVR is the keda-add-ons-http HTTPScaledObject CRD, driven via the
// dynamic client (it is not in the typed clientset and the common-chart has no
// template for it, so the backend renders it directly, idempotently — same pattern
// as the cert-manager Certificate / Gateway listener wiring).
var httpScaledObjectGVR = schema.GroupVersionResource{
	Group:    "http.keda.sh",
	Version:  "v1alpha1",
	Resource: "httpscaledobjects",
}

// keda-add-ons-http interceptor defaults. These are the fixed Service coordinates
// the keda-add-ons-http chart installs (deploy/helmfile.yaml installs it into the
// `keda` namespace); they are infrastructure names from that chart, not
// admin/business values, so they are package constants overridable via Config only
// to track a chart/version rename.
const (
	defaultHTTPInterceptorService   = "keda-add-ons-http-interceptor-proxy"
	defaultHTTPInterceptorNamespace = "keda"
	defaultHTTPInterceptorPort      = 8080
)

// interceptorService / interceptorNamespace / interceptorPort resolve the
// interceptor proxy coordinates from Config, falling back to the keda-add-ons-http
// chart defaults.
func (b *KubeBackend) interceptorService() string {
	if s := strings.TrimSpace(b.cfg.HTTPInterceptorService); s != "" {
		return s
	}
	return defaultHTTPInterceptorService
}

func (b *KubeBackend) interceptorNamespace() string {
	if s := strings.TrimSpace(b.cfg.HTTPInterceptorNamespace); s != "" {
		return s
	}
	return defaultHTTPInterceptorNamespace
}

func (b *KubeBackend) interceptorPort() int {
	if b.cfg.HTTPInterceptorPort > 0 {
		return b.cfg.HTTPInterceptorPort
	}
	return defaultHTTPInterceptorPort
}

// wantsHTTPWake reports whether a workload should be wired for HTTP scale-to-zero
// WAKE (an HTTPScaledObject + interceptor-routed HTTPRoute) rather than the plain
// CPU ScaledObject. It is true only for an HTTP/web app that:
//
//   - opts into the HTTP trigger (Scaling.HTTPTrigger, admin/DB-driven), AND
//   - actually scales to zero (resolved MinReplicas <= 0), AND
//   - is a routable, non-database workload (a public HTTPRoute exists to point at
//     the interceptor; a database has no route and never scales to zero).
//
// A worker / non-HTTP app (no HTTPTrigger) or one with a non-zero floor keeps the
// CPU ScaledObject — there is no request path to wake it through and no point
// inserting the interceptor.
func wantsHTTPWake(w Workload, stateful bool) bool {
	return w.Scaling.HTTPTrigger && !stateful && resolvedMinReplicas(w.Scaling, stateful) <= 0
}

// resolvedMinReplicas mirrors buildKeda's floor logic so the wake decision matches
// the rendered ScaledObject: a stateful workload is floored to 1, and a negative
// min is clamped to 0.
func resolvedMinReplicas(sc Scaling, stateful bool) int {
	m := sc.MinReplicas
	if m < 0 {
		m = 0
	}
	if stateful && m < 1 {
		m = 1
	}
	return m
}

// httpScaledObjectName is the HTTPScaledObject object name for a release (one per
// app, named exactly the release so lifecycle/cleanup can find it deterministically).
func httpScaledObjectName(release string) string { return release }

// buildHTTPScaledObject renders the keda-add-ons-http HTTPScaledObject for an
// HTTP/web app: it keys on the app's FQDN(s) (hosts), targets the app's
// Deployment + Service, and carries the scale bounds + scaledown window from the
// resolved Scaling so the add-on's generated ScaledObject takes the app to zero on
// idle and back to 1 on the first request. It is rendered directly (no chart
// template) and applied via the dynamic client in Apply.
func (b *KubeBackend) buildHTTPScaledObject(ns, release string, port int, hosts []string, sc Scaling) *unstructured.Unstructured {
	maxReplicas := sc.MaxReplicas
	if maxReplicas <= 0 {
		maxReplicas = 5
	}
	cooldown := sc.CooldownPeriod
	if cooldown <= 0 {
		cooldown = 300
	}

	hostList := make([]any, 0, len(hosts))
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hostList = append(hostList, h)
		}
	}

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "http.keda.sh/v1alpha1",
		"kind":       "HTTPScaledObject",
		"metadata": map[string]any{
			"name":      httpScaledObjectName(release),
			"namespace": ns,
			"labels":    map[string]any{"app.kubernetes.io/managed-by": "vortex"},
		},
		"spec": map[string]any{
			// hosts the interceptor matches on (the app's public FQDN(s)). The
			// interceptor wakes the target when a request for one of these arrives.
			"hosts": hostList,
			"scaleTargetRef": map[string]any{
				"name":       release,
				"kind":       "Deployment",
				"apiVersion": "apps/v1",
				"service":    release,
				"port":       int64(port),
			},
			"replicas": map[string]any{
				"min": int64(0),
				"max": int64(maxReplicas),
			},
			// Return the workload to zero after this idle window (mirrors the CPU
			// ScaledObject's cooldownPeriod so behavior is consistent across paths).
			"scaledownPeriod": int64(cooldown),
		},
	}}
	obj.SetGroupVersionKind(httpScaledObjectGVR.GroupVersion().WithKind("HTTPScaledObject"))
	return obj
}

// ensureHTTPScaledObject upserts the app's HTTPScaledObject (idempotent). With no
// dynamic client (local/dev) it no-ops so non-cluster flows keep working.
func (b *KubeBackend) ensureHTTPScaledObject(ctx context.Context, ns, release string, port int, hosts []string, sc Scaling) error {
	if b.dynamic == nil {
		return nil
	}
	obj := b.buildHTTPScaledObject(ns, release, port, hosts, sc)
	name := httpScaledObjectName(release)
	return upsert(ctx,
		func() error {
			_, err := b.dynamic.Resource(httpScaledObjectGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.dynamic.Resource(httpScaledObjectGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if err := unstructured.SetNestedMap(cur.Object, obj.Object["spec"].(map[string]any), "spec"); err != nil {
				return err
			}
			_, err = b.dynamic.Resource(httpScaledObjectGVR).Namespace(ns).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

// removeHTTPScaledObject deletes the app's HTTPScaledObject (idempotent). It is
// called when an app no longer wants HTTP wake (e.g. HTTPTrigger turned off, or a
// non-zero floor set) so a stale HTTPScaledObject does not keep routing the app
// through the interceptor. A missing object / no dynamic client is not an error.
func (b *KubeBackend) removeHTTPScaledObject(ctx context.Context, ns, release string) error {
	if b.dynamic == nil {
		return nil
	}
	err := b.dynamic.Resource(httpScaledObjectGVR).Namespace(ns).
		Delete(ctx, httpScaledObjectName(release), metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("kube: delete httpscaledobject %s/%s: %w", ns, httpScaledObjectName(release), err)
	}
	return nil
}

// interceptorBackendRefs builds the HTTPRoute rules that point the app's route at
// the keda interceptor proxy Service (in its own namespace) instead of the app's
// Service, so an inbound request flows through the interceptor and WAKES the
// scaled-to-zero app. The chart's httproute.yaml renders gateway.rules verbatim
// when set, taking precedence over the default app-Service backendRef.
//
// The interceptor lives in a DIFFERENT namespace from the tenant app, so the
// backendRef carries an explicit namespace; cross-namespace Gateway API routing
// additionally needs a ReferenceGrant in the interceptor namespace (provisioned by
// the keda-http-add-on values / bootstrap, not here).
func (b *KubeBackend) interceptorRouteRules() []map[string]any {
	return []map[string]any{{
		"matches": []map[string]any{{
			"path": map[string]any{"type": "PathPrefix", "value": "/"},
		}},
		"backendRefs": []map[string]any{{
			"name":      b.interceptorService(),
			"namespace": b.interceptorNamespace(),
			"port":      int64(b.interceptorPort()),
		}},
	}}
}

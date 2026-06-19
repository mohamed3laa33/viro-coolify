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

// domainListKinds maps the Gateway + Certificate GVRs to their List kinds for the
// dynamic fake, so Get/List target the EXACT GVRs the backend uses (the default
// pluralizer would key Certificate under "certificates" but seed it elsewhere).
func domainListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		gatewayGVR:     "GatewayList",
		certificateGVR: "CertificateList",
	}
}

// sharedGateway builds a Gateway with the wildcard HTTPS listener already present
// (the listener every other tenant shares), so listener-merge tests can assert it
// is preserved.
func sharedGateway(ns, name string) *unstructured.Unstructured {
	gw := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"gatewayClassName": "vortex",
			"listeners": []any{
				map[string]any{
					"name":     "https",
					"protocol": "HTTPS",
					"port":     int64(443),
					"hostname": "*.vortex.v60ai.com",
				},
			},
		},
	}}
	gw.SetGroupVersionKind(gatewayGVR.GroupVersion().WithKind("Gateway"))
	return gw
}

func domainTestConfig() Config {
	c := testConfig()
	c.ClusterIssuer = "vortex-letsencrypt"
	return c
}

// newDomainDyn builds a dynamic fake client (with the Gateway/Certificate list
// kinds registered) and seeds the given Gateway via the Tracker under the EXACT
// gatewayGVR, so Get/Update target it correctly (an empty scheme can't pluralize).
func newDomainDyn(t *testing.T, gw *unstructured.Unstructured) *fake.FakeDynamicClient {
	t.Helper()
	dc := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), domainListKinds())
	if gw != nil {
		if err := dc.Tracker().Create(gatewayGVR, gw, "vortex-system"); err != nil {
			t.Fatalf("seed gateway: %v", err)
		}
	}
	return dc
}

func TestEnsureDomainCertificateCreatesCert(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), domainListKinds())
	b := NewWithClients(domainTestConfig(), cs, dc, &mockHelm{})

	host := "shop.acme.io"
	if err := b.EnsureDomainCertificate(context.Background(), host); err != nil {
		t.Fatalf("EnsureDomainCertificate: %v", err)
	}
	got, err := dc.Resource(certificateGVR).Namespace("vortex-system").
		Get(context.Background(), DomainCertName(host), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get certificate: %v", err)
	}
	dnsNames, _, _ := unstructured.NestedStringSlice(got.Object, "spec", "dnsNames")
	if len(dnsNames) != 1 || dnsNames[0] != host {
		t.Errorf("cert dnsNames = %v, want [%s]", dnsNames, host)
	}
	secret, _, _ := unstructured.NestedString(got.Object, "spec", "secretName")
	if secret != DomainCertSecret(host) {
		t.Errorf("cert secretName = %q, want %q", secret, DomainCertSecret(host))
	}
	issuer, _, _ := unstructured.NestedString(got.Object, "spec", "issuerRef", "name")
	if issuer != "vortex-letsencrypt" {
		t.Errorf("cert issuerRef = %q, want vortex-letsencrypt", issuer)
	}

	// Idempotent: a second call must not error.
	if err := b.EnsureDomainCertificate(context.Background(), host); err != nil {
		t.Fatalf("EnsureDomainCertificate (repeat): %v", err)
	}
}

func TestEnsureDomainCertificateNoOpWithoutIssuer(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), domainListKinds())
	cfg := testConfig() // no ClusterIssuer
	b := NewWithClients(cfg, cs, dc, &mockHelm{})
	if err := b.EnsureDomainCertificate(context.Background(), "x.example.com"); err != nil {
		t.Fatalf("no-op EnsureDomainCertificate should be nil: %v", err)
	}
	list, _ := dc.Resource(certificateGVR).Namespace("vortex-system").List(context.Background(), metav1.ListOptions{})
	if len(list.Items) != 0 {
		t.Errorf("expected no certificates issued without a ClusterIssuer, got %d", len(list.Items))
	}
}

func TestEnsureGatewayListenerMergesAndPreservesWildcard(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newDomainDyn(t, sharedGateway("vortex-system", "vortex"))
	b := NewWithClients(domainTestConfig(), cs, dc, &mockHelm{})

	host := "shop.acme.io"
	if err := b.EnsureGatewayListener(context.Background(), host, DomainCertSecret(host)); err != nil {
		t.Fatalf("EnsureGatewayListener: %v", err)
	}
	gw, _ := dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex", metav1.GetOptions{})
	listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	if len(listeners) != 2 {
		t.Fatalf("expected 2 listeners (wildcard + custom), got %d: %v", len(listeners), listeners)
	}
	names := map[string]bool{}
	var customHostname, certName string
	for _, l := range listeners {
		m := l.(map[string]any)
		names[m["name"].(string)] = true
		if m["name"].(string) == DomainListenerName(host) {
			customHostname, _ = m["hostname"].(string)
			tls := m["tls"].(map[string]any)
			refs := tls["certificateRefs"].([]any)
			certName, _ = refs[0].(map[string]any)["name"].(string)
		}
	}
	if !names["https"] {
		t.Error("wildcard 'https' listener was clobbered")
	}
	if !names[DomainListenerName(host)] {
		t.Error("custom-domain listener not added")
	}
	if customHostname != host {
		t.Errorf("custom listener hostname = %q, want %q", customHostname, host)
	}
	if certName != DomainCertSecret(host) {
		t.Errorf("custom listener cert = %q, want %q", certName, DomainCertSecret(host))
	}

	// Idempotent: re-adding the same host must not duplicate the listener.
	if err := b.EnsureGatewayListener(context.Background(), host, DomainCertSecret(host)); err != nil {
		t.Fatalf("EnsureGatewayListener (repeat): %v", err)
	}
	gw, _ = dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex", metav1.GetOptions{})
	listeners, _, _ = unstructured.NestedSlice(gw.Object, "spec", "listeners")
	if len(listeners) != 2 {
		t.Fatalf("repeat EnsureGatewayListener duplicated a listener: %d", len(listeners))
	}
}

func TestEnsureGatewayListenerSecondTenantPreservesFirst(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newDomainDyn(t, sharedGateway("vortex-system", "vortex"))
	b := NewWithClients(domainTestConfig(), cs, dc, &mockHelm{})

	hostA := "a.tenant-one.io"
	hostB := "b.tenant-two.io"
	if err := b.EnsureGatewayListener(context.Background(), hostA, DomainCertSecret(hostA)); err != nil {
		t.Fatalf("tenant A EnsureGatewayListener: %v", err)
	}
	// A SECOND tenant merging its listener must not clobber the wildcard or tenant A.
	if err := b.EnsureGatewayListener(context.Background(), hostB, DomainCertSecret(hostB)); err != nil {
		t.Fatalf("tenant B EnsureGatewayListener: %v", err)
	}

	gw, _ := dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex", metav1.GetOptions{})
	listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	if len(listeners) != 3 {
		t.Fatalf("expected 3 listeners (wildcard + A + B), got %d: %v", len(listeners), listeners)
	}
	byName := map[string]map[string]any{}
	for _, l := range listeners {
		m := l.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if _, ok := byName["https"]; !ok {
		t.Error("wildcard 'https' listener was clobbered by the second tenant")
	}
	// Tenant A's listener must survive intact, still pointing at A's cert + host.
	la, ok := byName[DomainListenerName(hostA)]
	if !ok {
		t.Fatal("tenant A's listener was removed when tenant B attached")
	}
	if hn, _ := la["hostname"].(string); hn != hostA {
		t.Errorf("tenant A listener hostname = %q, want %q", hn, hostA)
	}
	refsA := la["tls"].(map[string]any)["certificateRefs"].([]any)
	if cn, _ := refsA[0].(map[string]any)["name"].(string); cn != DomainCertSecret(hostA) {
		t.Errorf("tenant A listener cert = %q, want %q (B clobbered A's cert)", cn, DomainCertSecret(hostA))
	}
	// Tenant B's listener references B's own cert (not A's).
	lb := byName[DomainListenerName(hostB)]
	if lb == nil {
		t.Fatal("tenant B's listener was not added")
	}
	refsB := lb["tls"].(map[string]any)["certificateRefs"].([]any)
	if cn, _ := refsB[0].(map[string]any)["name"].(string); cn != DomainCertSecret(hostB) {
		t.Errorf("tenant B listener cert = %q, want %q", cn, DomainCertSecret(hostB))
	}

	// Removing tenant B leaves the wildcard + tenant A intact.
	if err := b.RemoveGatewayListener(context.Background(), hostB); err != nil {
		t.Fatalf("RemoveGatewayListener(B): %v", err)
	}
	gw, _ = dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex", metav1.GetOptions{})
	listeners, _, _ = unstructured.NestedSlice(gw.Object, "spec", "listeners")
	if len(listeners) != 2 {
		t.Fatalf("expected wildcard + tenant A after removing B, got %d", len(listeners))
	}
	remaining := map[string]bool{}
	for _, l := range listeners {
		remaining[l.(map[string]any)["name"].(string)] = true
	}
	if !remaining["https"] || !remaining[DomainListenerName(hostA)] {
		t.Errorf("removing tenant B disturbed the wildcard or tenant A: %v", remaining)
	}
	if remaining[DomainListenerName(hostB)] {
		t.Error("tenant B's listener was not actually removed")
	}
}

func TestRemoveGatewayListenerKeepsOthers(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newDomainDyn(t, sharedGateway("vortex-system", "vortex"))
	b := NewWithClients(domainTestConfig(), cs, dc, &mockHelm{})
	host := "shop.acme.io"
	_ = b.EnsureGatewayListener(context.Background(), host, "")

	if err := b.RemoveGatewayListener(context.Background(), host); err != nil {
		t.Fatalf("RemoveGatewayListener: %v", err)
	}
	gw, _ := dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex", metav1.GetOptions{})
	listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	if len(listeners) != 1 {
		t.Fatalf("expected only the wildcard listener to remain, got %d", len(listeners))
	}
	if listeners[0].(map[string]any)["name"].(string) != "https" {
		t.Errorf("wrong listener remained: %v", listeners[0])
	}
	// Idempotent: removing an absent listener is a no-op.
	if err := b.RemoveGatewayListener(context.Background(), host); err != nil {
		t.Fatalf("RemoveGatewayListener (repeat): %v", err)
	}
}

// TestEnsureGatewayListenerShardsPastLimit replaces the old "refuses at the
// 64-listener limit" behavior: instead of erroring and asking the operator to move
// a tenant by hand, a full primary Gateway now OVERFLOWS to an auto-created shard
// Gateway (gateway_shard.go). This asserts the host lands on "vortex-shard-1" and
// that the primary is left untouched.
func TestEnsureGatewayListenerShardsPastLimit(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	gw := sharedGateway("vortex-system", "vortex")
	// Fill the primary Gateway to its custom-domain capacity (the 64-listener hard
	// ceiling; the wildcard listener already present counts toward it).
	listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	for len(listeners) < maxGatewayListeners {
		listeners = append(listeners, map[string]any{
			"name":     "filler-" + string(rune('a'+len(listeners)%26)) + string(rune('a'+len(listeners)/26)),
			"protocol": "HTTP", "port": int64(80),
		})
	}
	_ = unstructured.SetNestedSlice(gw.Object, listeners, "spec", "listeners")
	dc := newDomainDyn(t, gw)
	b := NewWithClients(domainTestConfig(), cs, dc, &mockHelm{})

	host := "over.acme.io"
	if err := b.EnsureGatewayListener(context.Background(), host, ""); err != nil {
		t.Fatalf("EnsureGatewayListener should auto-shard past the limit, got error: %v", err)
	}

	// The primary Gateway must be UNCHANGED (still at the ceiling, no new listener).
	primary, _ := dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex", metav1.GetOptions{})
	pl, _, _ := unstructured.NestedSlice(primary.Object, "spec", "listeners")
	if len(pl) != maxGatewayListeners {
		t.Fatalf("primary gateway listener count changed: got %d, want %d", len(pl), maxGatewayListeners)
	}

	// A shard Gateway "vortex-shard-1" must now exist holding the new listener.
	shard, err := dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex-shard-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected shard gateway vortex-shard-1 to be created: %v", err)
	}
	sl, _, _ := unstructured.NestedSlice(shard.Object, "spec", "listeners")
	if len(sl) != 1 {
		t.Fatalf("shard gateway should hold exactly the overflow listener, got %d", len(sl))
	}
	if n, _ := sl[0].(map[string]any)["name"].(string); n != DomainListenerName(host) {
		t.Errorf("shard listener name = %q, want %q", n, DomainListenerName(host))
	}

	// shardForHost must resolve the host to the shard so the HTTPRoute parentRef can
	// be pointed at it.
	gwName, found, err := b.shardForHost(context.Background(), host)
	if err != nil || !found {
		t.Fatalf("shardForHost(%q) = (%q, %v, %v), want it found on a shard", host, gwName, found, err)
	}
	if gwName != "vortex-shard-1" {
		t.Errorf("shardForHost = %q, want vortex-shard-1", gwName)
	}

	// Removing the listener empties the overflow shard, which is then GC'd.
	if err := b.RemoveGatewayListener(context.Background(), host); err != nil {
		t.Fatalf("RemoveGatewayListener: %v", err)
	}
	if _, err := dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex-shard-1", metav1.GetOptions{}); err == nil {
		t.Error("expected emptied overflow shard vortex-shard-1 to be garbage-collected")
	}
}

func TestRemoveDomainCertificateIdempotent(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), domainListKinds())
	b := NewWithClients(domainTestConfig(), cs, dc, &mockHelm{})
	// Removing an absent cert is a no-op.
	if err := b.RemoveDomainCertificate(context.Background(), "gone.acme.io"); err != nil {
		t.Fatalf("RemoveDomainCertificate on absent cert should be nil: %v", err)
	}
	host := "shop.acme.io"
	_ = b.EnsureDomainCertificate(context.Background(), host)
	if err := b.RemoveDomainCertificate(context.Background(), host); err != nil {
		t.Fatalf("RemoveDomainCertificate: %v", err)
	}
	if _, err := dc.Resource(certificateGVR).Namespace("vortex-system").
		Get(context.Background(), DomainCertName(host), metav1.GetOptions{}); err == nil {
		t.Error("expected certificate to be deleted")
	}
}

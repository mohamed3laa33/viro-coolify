package kube

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// externalDNSConfig is testConfig with external-dns turned on for the platform
// BaseDomain plus one extra managed custom-domain zone.
func externalDNSConfig() Config {
	c := testConfig()
	c.ExternalDNSEnabled = true
	c.ExternalDNSZones = []string{"vortex.v60ai.com", "acme.io"}
	c.DNSRecordTTL = 60
	return c
}

func TestHostInManagedZone(t *testing.T) {
	zones := []string{"vortex.v60ai.com", "acme.io"}
	cases := []struct {
		host string
		want bool
	}{
		{"api.web.acme.vortex.v60ai.com", true},
		{"vortex.v60ai.com", true},
		{"shop.acme.io", true},
		{"acme.io", true},
		{"shop.example.com", false}, // unmanaged zone
		{"notacme.io", false},       // not a subdomain of acme.io
		{"", false},
	}
	for _, c := range cases {
		if got := hostInManagedZone(c.host, zones); got != c.want {
			t.Errorf("hostInManagedZone(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestManagedZonesFallsBackToBaseDomain(t *testing.T) {
	c := testConfig()
	c.ExternalDNSEnabled = true // no zones configured
	b := NewWithClient(c, k8sfake.NewSimpleClientset(), &mockHelm{})
	zones := b.managedZones()
	if len(zones) != 1 || zones[0] != "vortex.v60ai.com" {
		t.Fatalf("managedZones with no config = %v, want [vortex.v60ai.com] (BaseDomain)", zones)
	}
}

// TestBuildValuesStampsExternalDNSOnTenantHost asserts the tenant host (under the
// platform zone) gets external-dns hostname + ttl annotations on the HTTPRoute and
// Service when external-dns is enabled.
func TestBuildValuesStampsExternalDNSOnTenantHost(t *testing.T) {
	b := NewWithClient(externalDNSConfig(), k8sfake.NewSimpleClientset(), &mockHelm{})
	vals := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256,
	}, "api.web.acme.vortex.v60ai.com")

	gw := vals["gateway"].(map[string]any)
	ann := gw["annotations"].(map[string]any)
	if ann[externalDNSHostnameAnnotation] != "api.web.acme.vortex.v60ai.com" {
		t.Errorf("HTTPRoute hostname annotation = %v, want the tenant FQDN", ann[externalDNSHostnameAnnotation])
	}
	if ann[externalDNSTTLAnnotation] != "60" {
		t.Errorf("HTTPRoute ttl annotation = %v, want 60", ann[externalDNSTTLAnnotation])
	}
	svc := vals["service"].(map[string]any)
	sann := svc["annotations"].(map[string]any)
	if sann[externalDNSHostnameAnnotation] != "api.web.acme.vortex.v60ai.com" {
		t.Errorf("Service hostname annotation = %v, want the tenant FQDN", sann[externalDNSHostnameAnnotation])
	}
}

// TestBuildValuesSkipsUnmanagedCustomDomain asserts a custom domain whose zone the
// platform does NOT manage is excluded from the external-dns annotation (the tenant
// points DNS at the LB themselves), while a managed custom domain is included.
func TestBuildValuesSkipsUnmanagedCustomDomain(t *testing.T) {
	b := NewWithClient(externalDNSConfig(), k8sfake.NewSimpleClientset(), &mockHelm{})
	vals := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256,
		Domains: []string{"shop.acme.io", "store.example.com"}, // 2nd zone unmanaged
	}, "api.web.acme.vortex.v60ai.com")

	gw := vals["gateway"].(map[string]any)
	ann := gw["annotations"].(map[string]any)
	got := ann[externalDNSHostnameAnnotation].(string)
	// Both managed hosts present, the unmanaged one absent.
	if want := "api.web.acme.vortex.v60ai.com,shop.acme.io"; got != want {
		t.Errorf("hostname annotation = %q, want %q (unmanaged store.example.com excluded)", got, want)
	}
}

// TestBuildValuesNoExternalDNSWhenDisabled asserts no annotations are stamped when
// external-dns is off (current default behavior unchanged).
func TestBuildValuesNoExternalDNSWhenDisabled(t *testing.T) {
	b := NewWithClient(testConfig(), k8sfake.NewSimpleClientset(), &mockHelm{}) // disabled
	vals := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256,
	}, "api.web.acme.vortex.v60ai.com")
	gw := vals["gateway"].(map[string]any)
	if _, ok := gw["annotations"]; ok {
		t.Errorf("gateway annotations set with external-dns disabled: %v", gw["annotations"])
	}
}

// TestEnsureGatewayListenerStampsExternalDNSForManagedCustomDomain asserts that
// attaching a verified custom-domain listener under a managed zone accumulates the
// host on the Gateway's external-dns annotation, so the record publishes.
func TestEnsureGatewayListenerStampsExternalDNSForManagedCustomDomain(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newDomainDyn(t, sharedGateway("vortex-system", "vortex"))
	cfg := domainTestConfig()
	cfg.ExternalDNSEnabled = true
	cfg.ExternalDNSZones = []string{"acme.io"}
	cfg.DNSRecordTTL = 120
	b := NewWithClients(cfg, cs, dc, &mockHelm{})

	host := "shop.acme.io"
	if err := b.EnsureGatewayListener(context.Background(), host, DomainCertSecret(host)); err != nil {
		t.Fatalf("EnsureGatewayListener: %v", err)
	}
	gw, _ := dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex", metav1.GetOptions{})
	ann := gw.GetAnnotations()
	if ann[externalDNSHostnameAnnotation] != host {
		t.Errorf("gateway hostname annotation = %q, want %q", ann[externalDNSHostnameAnnotation], host)
	}
	if ann[externalDNSTTLAnnotation] != "120" {
		t.Errorf("gateway ttl annotation = %q, want 120", ann[externalDNSTTLAnnotation])
	}
}

// TestEnsureGatewayListenerNoExternalDNSForUnmanagedZone asserts a verified custom
// domain whose zone the platform does NOT manage gets no Gateway DNS annotation
// (the listener is still attached for TLS; DNS is the tenant's responsibility).
func TestEnsureGatewayListenerNoExternalDNSForUnmanagedZone(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newDomainDyn(t, sharedGateway("vortex-system", "vortex"))
	cfg := domainTestConfig()
	cfg.ExternalDNSEnabled = true
	cfg.ExternalDNSZones = []string{"acme.io"} // only acme.io managed
	b := NewWithClients(cfg, cs, dc, &mockHelm{})

	host := "shop.example.com" // unmanaged
	if err := b.EnsureGatewayListener(context.Background(), host, DomainCertSecret(host)); err != nil {
		t.Fatalf("EnsureGatewayListener: %v", err)
	}
	gw, _ := dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), "vortex", metav1.GetOptions{})
	if _, ok := gw.GetAnnotations()[externalDNSHostnameAnnotation]; ok {
		t.Errorf("gateway got an external-dns annotation for an unmanaged zone: %v", gw.GetAnnotations())
	}
	// The listener is still attached (TLS still terminates regardless of DNS).
	listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	if len(listeners) != 2 {
		t.Errorf("expected wildcard + custom listener (2), got %d", len(listeners))
	}
}

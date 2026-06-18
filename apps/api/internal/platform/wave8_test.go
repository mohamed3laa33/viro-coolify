package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// fakeResolver is an injectable DNS resolver returning canned TXT records keyed by
// the looked-up name, so VerifyDomain can be exercised without real DNS.
type fakeResolver struct {
	txt map[string][]string
	err error
}

func (f *fakeResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.txt[name], nil
}

func newDomainSvc(t *testing.T, res Resolver) (*Service, *kube.FakeBackend) {
	t.Helper()
	st := store.NewMemoryStore()
	fb := kube.NewFakeBackend()
	svc := NewService(st, fb, billing.NewService(st, nil),
		WithBaseDomain("vortex.v60ai.com"),
		WithResolver(res),
	)
	return svc, fb
}

func TestAddDomainPendingWithTokenAndInstructions(t *testing.T) {
	svc, _ := newDomainSvc(t, &fakeResolver{})
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})

	res, err := svc.AddDomain(ctx, "org-1", app.ID, "Shop.Acme.IO")
	if err != nil {
		t.Fatalf("AddDomain: %v", err)
	}
	if res.Status != domain.DomainPending || res.Verified {
		t.Fatalf("expected pending+unverified, got %+v", res.Domain)
	}
	if res.Domain.Domain != "shop.acme.io" {
		t.Errorf("domain should be normalized lowercase, got %q", res.Domain.Domain)
	}
	if res.VerificationToken == "" {
		t.Fatal("expected a verification token")
	}
	if res.Instructions.TXTName != "_vortex-challenge.shop.acme.io" {
		t.Errorf("TXTName = %q", res.Instructions.TXTName)
	}
	if res.Instructions.TXTValue != res.VerificationToken {
		t.Errorf("TXTValue %q != token %q", res.Instructions.TXTValue, res.VerificationToken)
	}
	if res.Instructions.TargetType != "CNAME" || res.Instructions.TargetValue == "" {
		t.Errorf("expected a CNAME target hint, got %+v", res.Instructions)
	}
}

func TestAddDomainRejectsPlatformHost(t *testing.T) {
	svc, _ := newDomainSvc(t, &fakeResolver{})
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})

	for _, bad := range []string{
		"vortex.v60ai.com",              // the apex itself
		"web.proj.org.vortex.v60ai.com", // a tenant subdomain of the apex
		"not a domain",                  // invalid FQDN
		"single",                        // single label
	} {
		if _, err := svc.AddDomain(ctx, "org-1", app.ID, bad); !errors.Is(err, ErrInvalidDomain) {
			t.Errorf("AddDomain(%q): expected ErrInvalidDomain, got %v", bad, err)
		}
	}
}

func TestVerifyDomainMatchTriggersCertListenerAndRoute(t *testing.T) {
	res := &fakeResolver{txt: map[string][]string{}}
	svc, fb := newDomainSvc(t, res)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})

	added, _ := svc.AddDomain(ctx, "org-1", app.ID, "shop.acme.io")
	// Publish the matching TXT record.
	res.txt["_vortex-challenge.shop.acme.io"] = []string{"some-other", added.VerificationToken}

	out, err := svc.VerifyDomain(ctx, "org-1", app.ID, added.ID)
	if err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	if out.Status != domain.DomainVerified || !out.Verified {
		t.Fatalf("expected verified, got %+v", out.Domain)
	}
	if out.VerifiedAt.IsZero() {
		t.Error("expected VerifiedAt to be set")
	}
	// Cert + Gateway listener provisioned for the exact host.
	if !fb.DomainCerts["shop.acme.io"] {
		t.Errorf("expected EnsureDomainCertificate for shop.acme.io, got %v", fb.DomainCerts)
	}
	if _, ok := fb.GatewayListeners["shop.acme.io"]; !ok {
		t.Errorf("expected EnsureGatewayListener for shop.acme.io, got %v", fb.GatewayListeners)
	}
	// The re-Apply added the verified host to the workload hostnames.
	k := app.Namespace + "/" + app.Release
	w := fb.Applied[k]
	found := false
	for _, d := range w.Domains {
		if d == "shop.acme.io" {
			found = true
		}
	}
	if !found {
		t.Errorf("verified domain not added to workload hostnames: %v", w.Domains)
	}
}

func TestVerifyDomainNoSpoofMarksFailed(t *testing.T) {
	res := &fakeResolver{txt: map[string][]string{}}
	svc, fb := newDomainSvc(t, res)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})

	added, _ := svc.AddDomain(ctx, "org-1", app.ID, "shop.acme.io")
	// Publish a WRONG token (spoof attempt).
	res.txt["_vortex-challenge.shop.acme.io"] = []string{"attacker-controlled-value"}

	out, err := svc.VerifyDomain(ctx, "org-1", app.ID, added.ID)
	if err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	if out.Status != domain.DomainFailed || out.Verified {
		t.Fatalf("expected failed, got %+v", out.Domain)
	}
	if len(fb.DomainCerts) != 0 || len(fb.GatewayListeners) != 0 {
		t.Errorf("a failed verification must NOT issue cert/listener: certs=%v listeners=%v", fb.DomainCerts, fb.GatewayListeners)
	}
}

func TestPendingDomainNotRoutedVerifiedIs(t *testing.T) {
	res := &fakeResolver{txt: map[string][]string{}}
	svc, fb := newDomainSvc(t, res)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})

	// One pending, one verified.
	pending, _ := svc.AddDomain(ctx, "org-1", app.ID, "pending.acme.io")
	verified, _ := svc.AddDomain(ctx, "org-1", app.ID, "live.acme.io")
	res.txt["_vortex-challenge.live.acme.io"] = []string{verified.VerificationToken}
	if _, err := svc.VerifyDomain(ctx, "org-1", app.ID, verified.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Redeploy and inspect the workload hostnames.
	if _, err := svc.Deploy(ctx, "org-1", app.ID); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	k := app.Namespace + "/" + app.Release
	w := fb.Applied[k]
	for _, d := range w.Domains {
		if d == "pending.acme.io" {
			t.Errorf("PENDING domain must never be routed, got %v", w.Domains)
		}
	}
	hasLive := false
	for _, d := range w.Domains {
		if d == "live.acme.io" {
			hasLive = true
		}
	}
	if !hasLive {
		t.Errorf("verified domain should be routed, got %v", w.Domains)
	}
	_ = pending
}

func TestDeleteVerifiedDomainCleansCertAndListener(t *testing.T) {
	res := &fakeResolver{txt: map[string][]string{}}
	svc, fb := newDomainSvc(t, res)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})

	added, _ := svc.AddDomain(ctx, "org-1", app.ID, "shop.acme.io")
	res.txt["_vortex-challenge.shop.acme.io"] = []string{added.VerificationToken}
	if _, err := svc.VerifyDomain(ctx, "org-1", app.ID, added.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !fb.DomainCerts["shop.acme.io"] {
		t.Fatal("precondition: cert should exist")
	}

	if err := svc.DeleteDomain(ctx, "org-1", app.ID, added.ID); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}
	if fb.DomainCerts["shop.acme.io"] {
		t.Errorf("cert should be removed on delete, got %v", fb.DomainCerts)
	}
	if _, ok := fb.GatewayListeners["shop.acme.io"]; ok {
		t.Errorf("listener should be removed on delete, got %v", fb.GatewayListeners)
	}
	// The re-Apply dropped the host from the workload hostnames.
	k := app.Namespace + "/" + app.Release
	for _, d := range fb.Applied[k].Domains {
		if d == "shop.acme.io" {
			t.Errorf("deleted domain still routed: %v", fb.Applied[k].Domains)
		}
	}
}

func TestVerifyDomainCrossTenantHidden(t *testing.T) {
	res := &fakeResolver{txt: map[string][]string{}}
	svc, _ := newDomainSvc(t, res)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	added, _ := svc.AddDomain(ctx, "org-1", app.ID, "shop.acme.io")

	// A different org must not see/verify/delete the domain.
	if _, err := svc.VerifyDomain(ctx, "org-2", app.ID, added.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant verify: expected ErrNotFound, got %v", err)
	}
	if err := svc.DeleteDomain(ctx, "org-2", app.ID, added.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant delete: expected ErrNotFound, got %v", err)
	}
	if _, err := svc.AddDomain(ctx, "org-2", app.ID, "x.acme.io"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant add: expected ErrNotFound, got %v", err)
	}
}

func TestVerifyDomainGlobalHostUniquenessRejectsHijack(t *testing.T) {
	res := &fakeResolver{txt: map[string][]string{}}
	svc, fb := newDomainSvc(t, res)
	ctx := context.Background()

	// Tenant A (org-1) verifies example.com.
	appA, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	addedA, _ := svc.AddDomain(ctx, "org-1", appA.ID, "example.com")
	res.txt["_vortex-challenge.example.com"] = []string{addedA.VerificationToken}
	if _, err := svc.VerifyDomain(ctx, "org-1", appA.ID, addedA.ID); err != nil {
		t.Fatalf("tenant A verify: %v", err)
	}
	if !fb.DomainCerts["example.com"] {
		t.Fatal("precondition: tenant A should hold the cert")
	}
	aListenerCert := fb.GatewayListeners["example.com"]

	// Tenant B (org-2, its own app) publishes a VALID TXT for the SAME host (e.g.
	// after a DNS transfer / dangling DNS / shared TXT) and tries to verify it.
	appB, _ := svc.CreateApp(ctx, "org-2", CreateAppInput{Name: "site", Image: "nginx:1"})
	addedB, _ := svc.AddDomain(ctx, "org-2", appB.ID, "example.com")
	// Make B's own token also resolve so the TXT match itself succeeds.
	res.txt["_vortex-challenge.example.com"] = []string{addedA.VerificationToken, addedB.VerificationToken}

	_, err := svc.VerifyDomain(ctx, "org-2", appB.ID, addedB.ID)
	if !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("tenant B verify: expected ErrDomainTaken, got %v", err)
	}

	// Tenant A's domain, cert, and listener must be UNTOUCHED.
	if !fb.DomainCerts["example.com"] {
		t.Error("tenant A's cert was torn down by the rejected hijack")
	}
	if fb.GatewayListeners["example.com"] != aListenerCert {
		t.Errorf("tenant A's listener was re-pointed: %q -> %q", aListenerCert, fb.GatewayListeners["example.com"])
	}
	stillA, _ := svc.store.GetVerifiedDomainByHost(ctx, "example.com")
	if stillA == nil || stillA.ID != addedA.ID {
		t.Errorf("verified owner of example.com changed away from tenant A: %+v", stillA)
	}

	// Tenant B's own row must NOT be flipped to verified.
	bRow, _ := svc.store.GetDomain(ctx, addedB.ID)
	if bRow.IsVerified() {
		t.Errorf("rejected hijack still marked tenant B's domain verified: %+v", bRow)
	}
}

func TestVerifyDomainReVerifySameOwnerAllowed(t *testing.T) {
	// Re-verifying the SAME domain (its own already-verified row) must not be
	// rejected as taken — owner.ID == d.ID is the allowed self-match.
	res := &fakeResolver{txt: map[string][]string{}}
	svc, _ := newDomainSvc(t, res)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	added, _ := svc.AddDomain(ctx, "org-1", app.ID, "shop.acme.io")
	res.txt["_vortex-challenge.shop.acme.io"] = []string{added.VerificationToken}
	if _, err := svc.VerifyDomain(ctx, "org-1", app.ID, added.ID); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if _, err := svc.VerifyDomain(ctx, "org-1", app.ID, added.ID); err != nil {
		t.Fatalf("re-verify same owner must be allowed, got %v", err)
	}
}

func TestVerifyDomainGatewayTargetHint(t *testing.T) {
	st := store.NewMemoryStore()
	fb := kube.NewFakeBackend()
	svc := NewService(st, fb, billing.NewService(st, nil),
		WithBaseDomain("vortex.v60ai.com"),
		WithGatewayLBHost("203.0.113.10"),
		WithResolver(&fakeResolver{}),
	)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1"})
	res, _ := svc.AddDomain(ctx, "org-1", app.ID, "shop.acme.io")
	if res.Instructions.TargetType != "A" || res.Instructions.TargetValue != "203.0.113.10" {
		t.Errorf("expected an A target to the LB host, got %+v", res.Instructions)
	}
}

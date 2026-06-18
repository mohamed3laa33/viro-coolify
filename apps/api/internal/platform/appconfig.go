package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// ErrInvalidDomain is returned when a custom domain is not a valid FQDN or is a
// reserved platform host (the BaseDomain or a subdomain of it).
var ErrInvalidDomain = errors.New("platform: invalid custom domain")

// ErrDomainTaken is returned when a host is already VERIFIED by another app/org:
// even with a valid TXT challenge a second tenant may not take ownership of a host
// another tenant already proved (which would re-issue the cert and re-point the
// shared Gateway listener — a cross-tenant hijack). The HTTP layer maps it to 409.
var ErrDomainTaken = errors.New("platform: domain already verified by another owner")

// challengePrefix is the DNS label under which a tenant publishes the ownership
// TXT challenge: TXT _vortex-challenge.<domain> = <verificationToken>.
const challengePrefix = "_vortex-challenge."

// fqdnRe matches an RFC-1035 hostname: dot-separated labels of letters/digits/'-'
// (not leading/trailing '-'), at least two labels, total <=253 chars (checked
// separately). Case-insensitive; the domain is lowercased before matching.
var fqdnRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// EnvVar is a single app environment variable / secret as returned by the API.
// For SECRET entries the Value is MASKED (never the real/decrypted value).
type EnvVar struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// envMask is the placeholder returned in place of a secret value over the API.
const envMask = "***"

// ListEnv returns an app's environment variables (org-scoped). SECRET values are
// MASKED — the API never returns a decrypted secret value. Plain config values
// are returned as-is.
func (s *Service) ListEnv(ctx context.Context, orgID, appID string) ([]EnvVar, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	entries, err := s.store.ListAppEnv(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	out := make([]EnvVar, 0, len(entries))
	for _, e := range entries {
		v := e.Value
		if e.Secret {
			v = envMask
		}
		out = append(out, EnvVar{Key: e.Key, Value: v, Secret: e.Secret})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// SetEnv sets a single environment variable on an app. When secret is true the
// value is ENCRYPTED at rest (AES-256-GCM) before persistence and is delivered to
// the pod via a Kubernetes Secret on deploy; the returned value is masked. Plain
// config is stored as-is. The value reaches the workload on its next deploy.
func (s *Service) SetEnv(ctx context.Context, orgID, appID, key, value string, secret bool) (*EnvVar, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	stored := value
	if secret {
		enc, encErr := s.cipher.Encrypt(value)
		if encErr != nil {
			return nil, fmt.Errorf("platform: encrypt secret: %w", encErr)
		}
		stored = enc
	}
	if err := s.store.SetAppEnv(ctx, app.ID, key, stored, secret); err != nil {
		return nil, err
	}
	out := &EnvVar{Key: key, Value: value, Secret: secret}
	if secret {
		out.Value = envMask
	}
	return out, nil
}

// DeleteEnv removes an environment variable from an app.
func (s *Service) DeleteEnv(ctx context.Context, orgID, appID, key string) error {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return err
	}
	return s.store.DeleteAppEnv(ctx, app.ID, key)
}

// DomainInstructions tells the user exactly what DNS records to publish to
// (a) prove ownership and (b) point their domain at the platform. It rides on the
// AddDomain response (and the verify response) so the flow is self-documenting.
type DomainInstructions struct {
	// VerificationToken is the value to publish as the TXT challenge.
	VerificationToken string `json:"verificationToken"`
	// TXTName / TXTValue are the exact ownership-challenge record to create:
	// TXT <TXTName> = <TXTValue>.
	TXTName  string `json:"txtName"`
	TXTValue string `json:"txtValue"`
	// TargetType is "A" when an explicit Gateway LoadBalancer host/IP is configured
	// (point an A/ALIAS record at TargetValue) or "CNAME" (point a CNAME at the
	// app's generated host).
	TargetType  string `json:"targetType"`
	TargetValue string `json:"targetValue"`
}

// DomainResult bundles a domain record with the DNS instructions the user needs
// to verify ownership and route traffic.
type DomainResult struct {
	*domain.Domain
	Instructions DomainInstructions `json:"instructions"`
}

// ListDomains returns an app's custom domains.
func (s *Service) ListDomains(ctx context.Context, orgID, appID string) ([]domain.Domain, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	return s.store.ListDomainsByApp(ctx, app.ID)
}

// AddDomain attaches a custom domain to an app. The FQDN is validated (RFC-1035,
// and NOT the platform apex or a subdomain of it, so a tenant can never claim a
// platform/other-tenant host). The domain starts PENDING with a random
// crypto/rand verification token and is NOT routed until VerifyDomain proves
// ownership. The response carries the DNS instructions (TXT challenge + A/CNAME
// target) the user must publish.
func (s *Service) AddDomain(ctx context.Context, orgID, appID, fqdn string) (*DomainResult, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	host, err := s.validateCustomDomain(fqdn)
	if err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("platform: generate verification token: %w", err)
	}
	d := &domain.Domain{
		ID:                s.idgen(),
		OrgID:             orgID,
		AppID:             app.ID,
		Domain:            host,
		Verified:          false,
		Status:            domain.DomainPending,
		VerificationToken: token,
		CreatedAt:         s.now(),
	}
	if err := s.store.CreateDomain(ctx, d); err != nil {
		return nil, err
	}
	return &DomainResult{Domain: d, Instructions: s.domainInstructions(ctx, app, d)}, nil
}

// VerifyDomain looks up the DNS TXT challenge for a pending custom domain and
// marks it VERIFIED iff a TXT record at _vortex-challenge.<domain> equals the
// stored token; otherwise FAILED (no spoofing). On success it provisions the
// per-domain TLS certificate + the shared-Gateway HTTPS listener and re-applies
// the app's HTTPRoute so the now-verified host starts routing. Cross-tenant
// access is hidden as ErrNotFound.
func (s *Service) VerifyDomain(ctx context.Context, orgID, appID, domainID string) (*DomainResult, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	d, err := s.ownedDomain(ctx, orgID, appID, domainID)
	if err != nil {
		return nil, err
	}

	matched := false
	txts, lookupErr := s.resolver.LookupTXT(ctx, challengePrefix+d.Domain)
	if lookupErr == nil {
		for _, txt := range txts {
			if strings.TrimSpace(txt) == d.VerificationToken {
				matched = true
				break
			}
		}
	}

	if !matched {
		d.Status = domain.DomainFailed
		d.Verified = false
		if err := s.store.UpdateDomain(ctx, d); err != nil {
			return nil, err
		}
		return &DomainResult{Domain: d, Instructions: s.domainInstructions(ctx, app, d)}, nil
	}

	// Global hostname uniqueness: even with a valid TXT challenge, refuse to verify
	// a host that some OTHER app/org has already verified. Otherwise tenant B could
	// (after a DNS transfer / dangling DNS / shared TXT) re-issue the cert and
	// re-point the shared Gateway listener / HTTPRoute for a host tenant A owns —
	// a cross-tenant hijack / teardown DoS. The DB partial unique index is the
	// belt-and-suspenders guard; this check returns a clean 409 (ErrDomainTaken)
	// and leaves the existing owner's domain/cert/listener untouched.
	owner, err := s.store.GetVerifiedDomainByHost(ctx, d.Domain)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if owner != nil && owner.ID != d.ID {
		return nil, ErrDomainTaken
	}

	d.Status = domain.DomainVerified
	d.Verified = true
	d.VerifiedAt = s.now()
	if err := s.store.UpdateDomain(ctx, d); err != nil {
		return nil, err
	}

	// Provision TLS + Gateway termination for the verified host, then re-apply the
	// app so its HTTPRoute starts advertising the (now verified) custom hostname.
	if err := s.attachVerifiedDomain(ctx, app, d.Domain); err != nil {
		return nil, err
	}
	return &DomainResult{Domain: d, Instructions: s.domainInstructions(ctx, app, d)}, nil
}

// attachVerifiedDomain issues the per-domain cert, adds the shared-Gateway HTTPS
// listener, and re-applies the app's workload so the verified host is added to the
// HTTPRoute hostnames. A re-Apply is skipped for an app with no release yet (it
// will pick the host up on its next deploy).
func (s *Service) attachVerifiedDomain(ctx context.Context, app *domain.App, host string) error {
	if err := s.backend.EnsureDomainCertificate(ctx, host); err != nil {
		return err
	}
	if err := s.backend.EnsureGatewayListener(ctx, host, ""); err != nil {
		return err
	}
	if app.Release == "" || strings.TrimSpace(app.Image) == "" {
		return nil
	}
	orgSlug := s.orgSlug(ctx, app.OrgID)
	projSlug := s.projectSlug(ctx, app.ProjectID)
	if _, _, err := s.applyApp(ctx, app, orgSlug, projSlug); err != nil {
		return err
	}
	return nil
}

// DeleteDomain removes a custom domain from an app (org-scoped). For a verified
// domain it also tears down the per-domain TLS certificate + the shared-Gateway
// listener, then re-applies the app so the HTTPRoute drops the hostname.
func (s *Service) DeleteDomain(ctx context.Context, orgID, appID, domainID string) error {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return err
	}
	d, err := s.ownedDomain(ctx, orgID, appID, domainID)
	if err != nil {
		return err
	}
	wasVerified := d.IsVerified()
	if err := s.store.DeleteDomain(ctx, domainID); err != nil {
		return err
	}
	if wasVerified {
		// Best-effort teardown order: route first (stop advertising the host), then
		// the listener + cert. A re-Apply failure must not leave the record around,
		// so the store delete above is authoritative; cleanup errors surface.
		if app.Release != "" && strings.TrimSpace(app.Image) != "" {
			orgSlug := s.orgSlug(ctx, orgID)
			projSlug := s.projectSlug(ctx, app.ProjectID)
			if _, _, err := s.applyApp(ctx, app, orgSlug, projSlug); err != nil {
				return err
			}
		}
		if err := s.backend.RemoveGatewayListener(ctx, d.Domain); err != nil {
			return err
		}
		if err := s.backend.RemoveDomainCertificate(ctx, d.Domain); err != nil {
			return err
		}
	}
	return nil
}

// ownedDomain loads a domain ensuring it belongs to the org's app, hiding
// cross-tenant existence as ErrNotFound.
func (s *Service) ownedDomain(ctx context.Context, orgID, appID, domainID string) (*domain.Domain, error) {
	d, err := s.store.GetDomain(ctx, domainID)
	if err != nil {
		return nil, ErrNotFound
	}
	if d.AppID != appID || d.OrgID != orgID {
		return nil, ErrNotFound
	}
	return d, nil
}

// validateCustomDomain normalizes and validates a custom FQDN: it must be a valid
// RFC-1035 hostname (<=253 chars, multi-label) and must NOT be the platform apex
// or any subdomain of it (so a tenant cannot hijack a platform/other-tenant host).
func (s *Service) validateCustomDomain(fqdn string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(fqdn))
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 || !fqdnRe.MatchString(host) {
		return "", fmt.Errorf("%w: %q is not a valid hostname", ErrInvalidDomain, fqdn)
	}
	if base := s.baseDomain; base != "" {
		if host == base || strings.HasSuffix(host, "."+base) {
			return "", fmt.Errorf("%w: %q is a reserved platform host", ErrInvalidDomain, fqdn)
		}
	}
	return host, nil
}

// randomToken returns a 32-hex-char (128-bit) crypto/rand verification token.
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// domainInstructions builds the DNS records the user must publish for a domain:
// the TXT ownership challenge and the A/CNAME target (the explicit Gateway LB host
// when configured, else a CNAME to the app's generated host).
func (s *Service) domainInstructions(ctx context.Context, app *domain.App, d *domain.Domain) DomainInstructions {
	ins := DomainInstructions{
		VerificationToken: d.VerificationToken,
		TXTName:           challengePrefix + d.Domain,
		TXTValue:          d.VerificationToken,
	}
	if s.gatewayLBHost != "" {
		ins.TargetType = "A"
		ins.TargetValue = s.gatewayLBHost
		return ins
	}
	ins.TargetType = "CNAME"
	ins.TargetValue = s.generatedHost(ctx, app)
	return ins
}

// generatedHost derives the app's platform-generated hostname
// (<app>.<project>.<org>.<BaseDomain>) used as the CNAME target hint. It mirrors
// the kube host derivation; it falls back to the stored app.Host when set.
func (s *Service) generatedHost(ctx context.Context, app *domain.App) string {
	if app.Host != "" {
		return app.Host
	}
	if s.baseDomain == "" {
		return ""
	}
	orgSlug := s.orgSlug(ctx, app.OrgID)
	projSlug := s.projectSlug(ctx, app.ProjectID)
	return fmt.Sprintf("%s.%s.%s.%s",
		kubeSanitize(app.Name), kubeSanitize(projSlug), kubeSanitize(orgSlug), s.baseDomain)
}

// PodMetric is one workload pod's live resource usage (CPU millicores, memory
// bytes) as read from the cluster metrics-server.
type PodMetric struct {
	Pod           string `json:"pod"`
	CPUMillicores int64  `json:"cpuMillicores"`
	MemoryBytes   int64  `json:"memoryBytes"`
}

// Metrics is a LIVE, point-in-time snapshot of an app's pod resource usage read
// from the cluster metrics-server (metrics.k8s.io). It is never synthesized: when
// the metrics-server is unavailable (or the app is not deployed) Available is
// false and Unavailable explains why, with zeroed usage — the API surfaces an
// honest "no data" rather than fabricated numbers.
type Metrics struct {
	Available     bool        `json:"available"`
	Unavailable   string      `json:"unavailable,omitempty"`
	Pods          []PodMetric `json:"pods"`
	CPUMillicores int64       `json:"cpuMillicores"` // aggregate across pods
	MemoryBytes   int64       `json:"memoryBytes"`   // aggregate across pods
}

// AppMetrics returns the app's LIVE pod CPU/memory usage from the metrics-server.
// There is NO synthetic data: an app that is not deployed (no Release) or whose
// cluster has no metrics-server returns Available=false with an honest reason,
// never fabricated load.
func (s *Service) AppMetrics(ctx context.Context, orgID, appID string) (*Metrics, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	if app.Release == "" {
		return &Metrics{Available: false, Unavailable: "app not deployed", Pods: []PodMetric{}}, nil
	}
	wm, err := s.backend.Metrics(ctx, app.Namespace, app.Release)
	if err != nil {
		return nil, err
	}
	return metricsFromBackend(wm), nil
}

// metricsFromBackend maps the kube backend's WorkloadMetrics to the platform
// Metrics DTO (pure translation; the numbers come straight from metrics-server).
func metricsFromBackend(wm kube.WorkloadMetrics) *Metrics {
	pods := make([]PodMetric, 0, len(wm.Pods))
	for _, p := range wm.Pods {
		pods = append(pods, PodMetric{Pod: p.Pod, CPUMillicores: p.CPUMillicores, MemoryBytes: p.MemoryBytes})
	}
	return &Metrics{
		Available:     wm.Available,
		Unavailable:   wm.Unavailable,
		Pods:          pods,
		CPUMillicores: wm.CPUMillicores,
		MemoryBytes:   wm.MemoryBytes,
	}
}

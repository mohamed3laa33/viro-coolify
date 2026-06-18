package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

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

// ListDomains returns an app's custom domains.
func (s *Service) ListDomains(ctx context.Context, orgID, appID string) ([]domain.Domain, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	return s.store.ListDomainsByApp(ctx, app.ID)
}

// AddDomain attaches a custom domain to an app. The domain is persisted and
// wired onto the workload's HTTPRoute on its next deploy.
func (s *Service) AddDomain(ctx context.Context, orgID, appID, fqdn string) (*domain.Domain, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}
	d := &domain.Domain{
		ID:        s.idgen(),
		OrgID:     orgID,
		AppID:     app.ID,
		Domain:    strings.TrimSpace(fqdn),
		Verified:  false,
		CreatedAt: s.now(),
	}
	if err := s.store.CreateDomain(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// DeleteDomain removes a custom domain from an app (org-scoped).
func (s *Service) DeleteDomain(ctx context.Context, orgID, appID, domainID string) error {
	if _, err := s.ownedApp(ctx, orgID, appID); err != nil {
		return err
	}
	d, err := s.store.GetDomain(ctx, domainID)
	if err != nil {
		return ErrNotFound
	}
	if d.AppID != appID || d.OrgID != orgID {
		return ErrNotFound
	}
	return s.store.DeleteDomain(ctx, domainID)
}

// MetricPoint is a single time-series sample.
type MetricPoint struct {
	T int64   `json:"t"` // unix seconds
	V float64 `json:"v"`
}

// Metrics is a small bundle of derived time-series for an app.
type Metrics struct {
	CPU      []MetricPoint `json:"cpu"`
	Memory   []MetricPoint `json:"memory"`
	Requests []MetricPoint `json:"requests"`
}

const metricsPoints = 24

// AppMetrics returns deterministic, synthetic metrics derived from the app id
// and status (no randomness so output is stable/testable offline).
func (s *Service) AppMetrics(ctx context.Context, orgID, appID string) (*Metrics, error) {
	app, err := s.ownedApp(ctx, orgID, appID)
	if err != nil {
		return nil, err
	}

	// Stable seed from the app id.
	var seed uint32 = 2166136261
	for _, b := range []byte(app.ID) {
		seed = (seed ^ uint32(b)) * 16777619
	}
	// Scale by status: a running/deploying app shows load; stopped apps are flat.
	active := 1.0
	if app.Status == "stopped" {
		active = 0
	}

	now := s.now().Unix()
	m := &Metrics{
		CPU:      make([]MetricPoint, metricsPoints),
		Memory:   make([]MetricPoint, metricsPoints),
		Requests: make([]MetricPoint, metricsPoints),
	}
	for i := 0; i < metricsPoints; i++ {
		// Deterministic pseudo-wave per index seeded by the app id.
		t := now - int64((metricsPoints-1-i))*int64(time.Hour/time.Second)
		x := float64((seed>>uint(i%16))&0xff) / 255.0
		y := float64((seed>>uint((i+5)%16))&0xff) / 255.0
		z := float64((seed>>uint((i+11)%16))&0xff) / 255.0
		cpuPct := active * (10 + x*float64(max(1, int(app.CPU*100)))/100*60)
		memPct := active * (20 + y*50)
		reqs := active * (z * 500)
		m.CPU[i] = MetricPoint{T: t, V: round2(cpuPct)}
		m.Memory[i] = MetricPoint{T: t, V: round2(memPct)}
		m.Requests[i] = MetricPoint{T: t, V: round2(reqs)}
	}
	return m, nil
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}

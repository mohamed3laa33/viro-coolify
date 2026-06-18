package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
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

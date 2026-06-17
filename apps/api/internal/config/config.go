// Package config loads Vortex API runtime configuration from the environment.
// Variables use the VORTEX_ prefix; the legacy VIRO_ prefix is still accepted
// as a fallback so existing deployments keep working during the rename.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// defaultDevJWTSecret is the insecure development fallback; it must never be used in production.
const defaultDevJWTSecret = "dev-insecure-secret-change-me" //nolint:gosec // G101: dev placeholder, rejected in production (see Load)

// Config holds all runtime configuration for the Vortex control-plane API.
type Config struct {
	Env      string
	HTTPAddr string

	// Persistence.
	DatabaseURL string

	// Auth / JWT.
	JWTSecret     string
	JWTAccessTTL  int // minutes
	JWTRefreshTTL int // hours

	// Coolify orchestration backend (legacy/optional; Kubernetes backend is primary).
	CoolifyBaseURL string
	CoolifyToken   string

	// Kubernetes deploy backend (primary runtime).
	BaseDomain       string // platform apex, e.g. "vortex.v60ai.com"
	Kubeconfig       string // path to a kubeconfig (empty => in-cluster / default rules)
	KubeChartPath    string // path to the common-chart used for workload installs
	GatewayName      string // shared Gateway every per-app HTTPRoute attaches to
	GatewayNamespace string // namespace of the shared Gateway

	// Billing (Stripe, test-mode by default).
	StripeSecretKey     string
	StripeWebhookSecret string
	BillingEnabled      bool

	CORSAllowedOrigins []string

	// Super-admin: emails (normalized) that are granted platform-wide admin.
	AdminEmails []string
}

// Load reads configuration from environment variables, applying development defaults.
func Load() (*Config, error) {
	cfg := &Config{
		Env:                 getenv("ENV", "development"),
		HTTPAddr:            getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:         getenv("DATABASE_URL", ""),
		JWTSecret:           getenv("JWT_SECRET", defaultDevJWTSecret),
		JWTAccessTTL:        getenvInt("JWT_ACCESS_TTL_MIN", 15),
		JWTRefreshTTL:       getenvInt("JWT_REFRESH_TTL_HOURS", 24*30),
		CoolifyBaseURL:      getenv("COOLIFY_BASE_URL", "http://localhost:8000"),
		CoolifyToken:        getenv("COOLIFY_TOKEN", ""),
		BaseDomain:          getenv("BASE_DOMAIN", "vortex.v60ai.com"),
		Kubeconfig:          getenv("KUBECONFIG", ""),
		KubeChartPath:       getenv("KUBE_CHART_PATH", "deploy/charts/common-chart"),
		GatewayName:         getenv("GATEWAY_NAME", "vortex"),
		GatewayNamespace:    getenv("GATEWAY_NAMESPACE", "vortex"),
		StripeSecretKey:     getenv("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret: getenv("STRIPE_WEBHOOK_SECRET", ""),
		BillingEnabled:      getenvBool("BILLING_ENABLED", false),
		CORSAllowedOrigins:  splitAndTrim(getenv("CORS_ORIGINS", "http://localhost:3000")),
		AdminEmails:         splitAndTrim(strings.ToLower(getenv("ADMIN_EMAILS", ""))),
	}
	if cfg.IsProduction() && (cfg.JWTSecret == "" || cfg.JWTSecret == defaultDevJWTSecret) {
		return nil, errors.New("VORTEX_JWT_SECRET must be set to a strong value in production")
	}
	return cfg, nil
}

// IsProduction reports whether the API is running in a production environment.
func (c *Config) IsProduction() bool { return c.Env == "production" }

// lookup returns the VORTEX_<suffix> env var, falling back to the legacy
// VIRO_<suffix> var, then ok=false.
func lookup(suffix string) (string, bool) {
	if v, ok := os.LookupEnv("VORTEX_" + suffix); ok && v != "" {
		return v, true
	}
	if v, ok := os.LookupEnv("VIRO_" + suffix); ok && v != "" {
		return v, true
	}
	return "", false
}

func getenv(suffix, fallback string) string {
	if v, ok := lookup(suffix); ok {
		return v
	}
	return fallback
}

func getenvInt(suffix string, fallback int) int {
	if v, ok := lookup(suffix); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}

func getenvBool(suffix string, fallback bool) bool {
	if v, ok := lookup(suffix); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return fallback
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

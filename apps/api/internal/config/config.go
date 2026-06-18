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
	DBMaxConns  int // upper bound on pooled connections
	DBMinConns  int // warm connections kept open

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
	// ClusterIssuer is the cert-manager ClusterIssuer that signs per-tenant
	// custom-domain certificates. Empty disables per-domain TLS issuance.
	ClusterIssuer string
	// GatewayLBHost optionally advertises the shared Gateway LoadBalancer host/IP
	// as the A/ALIAS target in custom-domain DNS instructions. Empty advises a
	// CNAME to the app's generated host instead.
	GatewayLBHost  string
	HelmTimeoutSec int // per-Apply helm deadline (seconds); --wait --atomic
	ReconcileSec   int // status reconciler interval (seconds)

	// DBDefaultStorageGB is the default persistent-volume size (GiB) for a managed
	// database when the create request does not specify one. Admin-tunable.
	DBDefaultStorageGB int
	// DBStorageClass optionally overrides the StorageClass for managed-database
	// data volumes. Empty leaves the cluster/chart default in force.
	DBStorageClass string

	// Git image builder (kaniko Job pipeline). All admin-tunable via VORTEX_BUILD_*.
	BuildRegistry    string // push target host/repo prefix, e.g. ghcr.io/<owner> or registry.digitalocean.com/<reg>
	BuildNamespace   string // namespace where kaniko build Jobs run
	BuildPushSecret  string // docker-config Secret used to push (in the build namespace)
	BuildGitCreds    string // optional Secret (build ns) exposing GIT_USERNAME/GIT_PASSWORD/GIT_TOKEN for private clones
	BuildKanikoImage string // pinned kaniko executor image
	BuildTimeoutSec  int    // per-build deadline (seconds)

	// Registry pull secret: the per-tenant imagePullSecret name attached to built
	// apps, and the control-plane SOURCE secret (+ namespace) copied into each
	// tenant namespace so a private built image can be pulled.
	RegistryPullSecret          string // tenant-namespace imagePullSecret name attached to built apps
	RegistryPullSecretSource    string // control-plane source dockerconfigjson Secret to copy from (empty => no-op in dev)
	RegistryPullSecretNamespace string // namespace of the source secret (default "vortex")

	// Billing (Stripe, test-mode by default).
	StripeSecretKey     string
	StripeWebhookSecret string
	BillingEnabled      bool

	// CORSAllowedOrigins is the exact set of browser Origins permitted to make
	// credentialed (cookie) cross-origin calls. It is driven by VORTEX_CORS_ORIGINS
	// (comma-separated). In production the web app is served from a different
	// subdomain than the API (app.<...>.vortex.v60ai.com vs api.vortex.v60ai.com),
	// so the deploying operator MUST set VORTEX_CORS_ORIGINS to the exact web
	// origin(s), e.g.:
	//
	//	VORTEX_CORS_ORIGINS=https://app.vortex.v60ai.com
	//
	// Each entry is matched exactly (scheme + host + optional port) and echoed back
	// as Access-Control-Allow-Origin; wildcard host patterns are NOT expanded, so
	// list every concrete origin. The default below covers local dev plus the
	// canonical production web origin.
	CORSAllowedOrigins []string

	// Super-admin: emails (normalized) that are granted platform-wide admin.
	AdminEmails []string

	// SecretEncryptionKey is the AES-256-GCM key (base64 or hex, 32 bytes) used to
	// encrypt SECRET app env values at rest. Empty in dev falls back to no
	// encryption (a logged warning) — never a panic.
	SecretEncryptionKey string

	// SMTP / email. When SMTPHost is empty the platform uses a NoopMailer so it
	// runs without a mail server (invitations/welcome/reset emails are silently
	// dropped in dev). Set these to enable real delivery via net/smtp.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string // envelope/From address, e.g. "Vortex <no-reply@…>"
	SMTPStartTLS bool   // upgrade the connection with STARTTLS before auth

	// MetricsToken, when set, is the Bearer token required to scrape GET /metrics
	// (the internal Prometheus endpoint). Empty leaves the endpoint ungated, so it
	// MUST then be bound to a private listen addr / restricted by network policy.
	MetricsToken string
	// MetricsAddr, when set (e.g. "127.0.0.1:9090"), runs the /metrics endpoint on
	// a SEPARATE internal listener instead of the public API addr, so the scrape
	// surface is never exposed alongside the tenant API. Empty serves /metrics on
	// the main router (still token-gated when MetricsToken is set).
	MetricsAddr string

	// InvitationTTLHours bounds how long an invitation can be accepted after it is
	// created (default 7 days). Admin/DB-tunable via VORTEX_INVITATION_TTL_HOURS.
	InvitationTTLHours int
	// PasswordResetTTLMin bounds how long a password-reset token is valid after it
	// is issued (default 60 minutes).
	PasswordResetTTLMin int
}

// Load reads configuration from environment variables, applying development defaults.
func Load() (*Config, error) {
	cfg := &Config{
		Env:              getenv("ENV", "development"),
		HTTPAddr:         getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:      getenv("DATABASE_URL", ""),
		DBMaxConns:       getenvInt("DB_MAX_CONNS", 10),
		DBMinConns:       getenvInt("DB_MIN_CONNS", 2),
		JWTSecret:        getenv("JWT_SECRET", defaultDevJWTSecret),
		JWTAccessTTL:     getenvInt("JWT_ACCESS_TTL_MIN", 15),
		JWTRefreshTTL:    getenvInt("JWT_REFRESH_TTL_HOURS", 24*30),
		CoolifyBaseURL:   getenv("COOLIFY_BASE_URL", "http://localhost:8000"),
		CoolifyToken:     getenv("COOLIFY_TOKEN", ""),
		BaseDomain:       getenv("BASE_DOMAIN", "vortex.v60ai.com"),
		Kubeconfig:       getenv("KUBECONFIG", ""),
		KubeChartPath:    getenv("KUBE_CHART_PATH", "deploy/charts/common-chart"),
		GatewayName:      getenv("GATEWAY_NAME", "vortex"),
		GatewayNamespace: getenv("GATEWAY_NAMESPACE", "vortex"),
		ClusterIssuer:    getenv("CLUSTER_ISSUER", "vortex-letsencrypt"),
		GatewayLBHost:    getenv("GATEWAY_LB_HOST", ""),
		HelmTimeoutSec:   getenvInt("HELM_TIMEOUT_SEC", 300),
		ReconcileSec:     getenvInt("RECONCILE_SEC", 30),

		DBDefaultStorageGB: getenvInt("DB_DEFAULT_STORAGE_GB", 1),
		DBStorageClass:     getenv("DB_STORAGE_CLASS", ""),
		BuildRegistry:      getenv("BUILD_REGISTRY", ""),
		BuildNamespace:     getenv("BUILD_NAMESPACE", "vortex-builds"),
		BuildPushSecret:    getenv("BUILD_PUSH_SECRET", "vortex-registry-push"),
		BuildGitCreds:      getenv("BUILD_GIT_CREDS_SECRET", ""),
		BuildKanikoImage:   getenv("BUILD_KANIKO_IMAGE", "gcr.io/kaniko-project/executor:v1.23.2"),
		BuildTimeoutSec:    getenvInt("BUILD_TIMEOUT_SEC", 600),

		RegistryPullSecret:          getenv("REGISTRY_PULL_SECRET", "vortex-registry-pull"),
		RegistryPullSecretSource:    getenv("REGISTRY_PULL_SECRET_SOURCE", ""),
		RegistryPullSecretNamespace: getenv("REGISTRY_PULL_SECRET_NAMESPACE", "vortex"),
		StripeSecretKey:             getenv("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret:         getenv("STRIPE_WEBHOOK_SECRET", ""),
		BillingEnabled:              getenvBool("BILLING_ENABLED", false),
		CORSAllowedOrigins:          splitAndTrim(getenv("CORS_ORIGINS", "http://localhost:3000,https://app.vortex.v60ai.com")),
		AdminEmails:                 splitAndTrim(strings.ToLower(getenv("ADMIN_EMAILS", ""))),
		SecretEncryptionKey:         getenv("SECRET_ENCRYPTION_KEY", ""),

		SMTPHost:     getenv("SMTP_HOST", ""),
		SMTPPort:     getenvInt("SMTP_PORT", 587),
		SMTPUsername: getenv("SMTP_USERNAME", ""),
		SMTPPassword: getenv("SMTP_PASSWORD", ""),
		SMTPFrom:     getenv("SMTP_FROM", ""),
		SMTPStartTLS: getenvBool("SMTP_STARTTLS", true),

		MetricsToken: getenv("METRICS_TOKEN", ""),
		MetricsAddr:  getenv("METRICS_ADDR", ""),

		InvitationTTLHours:  getenvInt("INVITATION_TTL_HOURS", 24*7),
		PasswordResetTTLMin: getenvInt("PASSWORD_RESET_TTL_MIN", 60),
	}
	if cfg.IsProduction() && (cfg.JWTSecret == "" || cfg.JWTSecret == defaultDevJWTSecret) {
		return nil, errors.New("VORTEX_JWT_SECRET must be set to a strong value in production")
	}
	// In production a missing encryption key would silently fall back to the
	// NoopCipher and store app secrets PLAINTEXT. Hard-fail like the JWT guard so
	// a forgotten key is an explicit startup error, not a silent downgrade. Dev
	// still allows an empty key (NoopCipher with the existing warning).
	if cfg.IsProduction() && strings.TrimSpace(cfg.SecretEncryptionKey) == "" {
		return nil, errors.New("VORTEX_SECRET_ENCRYPTION_KEY must be set in production")
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

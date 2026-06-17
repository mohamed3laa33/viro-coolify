// Package config loads Viro API runtime configuration from the environment.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// defaultDevJWTSecret is the insecure development fallback; it must never be used in production.
const defaultDevJWTSecret = "dev-insecure-secret-change-me"

// Config holds all runtime configuration for the Viro control-plane API.
type Config struct {
	Env      string
	HTTPAddr string

	// Persistence.
	DatabaseURL string

	// Auth / JWT.
	JWTSecret     string
	JWTAccessTTL  int // minutes
	JWTRefreshTTL int // hours

	// Coolify orchestration backend.
	CoolifyBaseURL string
	CoolifyToken   string

	// Billing (Stripe, test-mode by default).
	StripeSecretKey     string
	StripeWebhookSecret string
	BillingEnabled      bool

	CORSAllowedOrigins []string
}

// Load reads configuration from environment variables, applying development defaults.
func Load() (*Config, error) {
	cfg := &Config{
		Env:                 getenv("VIRO_ENV", "development"),
		HTTPAddr:            getenv("VIRO_HTTP_ADDR", ":8080"),
		DatabaseURL:         getenv("VIRO_DATABASE_URL", ""),
		JWTSecret:           getenv("VIRO_JWT_SECRET", defaultDevJWTSecret),
		JWTAccessTTL:        getenvInt("VIRO_JWT_ACCESS_TTL_MIN", 15),
		JWTRefreshTTL:       getenvInt("VIRO_JWT_REFRESH_TTL_HOURS", 24*30),
		CoolifyBaseURL:      getenv("VIRO_COOLIFY_BASE_URL", "http://localhost:8000"),
		CoolifyToken:        getenv("VIRO_COOLIFY_TOKEN", ""),
		StripeSecretKey:     getenv("VIRO_STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret: getenv("VIRO_STRIPE_WEBHOOK_SECRET", ""),
		BillingEnabled:      getenvBool("VIRO_BILLING_ENABLED", false),
		CORSAllowedOrigins:  splitAndTrim(getenv("VIRO_CORS_ORIGINS", "http://localhost:3000")),
	}
	if cfg.IsProduction() && (cfg.JWTSecret == "" || cfg.JWTSecret == defaultDevJWTSecret) {
		return nil, errors.New("VIRO_JWT_SECRET must be set to a strong value in production")
	}
	return cfg, nil
}

// IsProduction reports whether the API is running in a production environment.
func (c *Config) IsProduction() bool { return c.Env == "production" }

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok {
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

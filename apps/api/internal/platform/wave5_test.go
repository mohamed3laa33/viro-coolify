package platform

import (
	"context"
	"strings"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/billing"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/secrets"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

func newSvcWithCipher(t *testing.T) (*Service, *kube.FakeBackend) {
	t.Helper()
	st := store.NewMemoryStore()
	fb := kube.NewFakeBackend()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	c, err := secrets.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return NewService(st, fb, billing.NewService(st, nil), WithCipher(c)), fb
}

func TestSecretEnvEncryptedAtRestAndMasked(t *testing.T) {
	svc, _ := newSvcWithCipher(t)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1.27"})

	// Set a SECRET and a PLAIN env.
	ev, err := svc.SetEnv(ctx, "org-1", app.ID, "API_KEY", "s3cret-value", true)
	if err != nil {
		t.Fatalf("set secret env: %v", err)
	}
	if ev.Value != "***" || !ev.Secret {
		t.Fatalf("SetEnv should mask secret: %+v", ev)
	}
	if _, err := svc.SetEnv(ctx, "org-1", app.ID, "PLAIN", "plain-value", false); err != nil {
		t.Fatalf("set plain env: %v", err)
	}

	// At-rest store value for the secret must be ENCRYPTED (not the plaintext).
	raw, _ := svc.store.GetAppEnv(ctx, app.ID)
	if raw["API_KEY"] == "s3cret-value" {
		t.Fatalf("secret stored in plaintext at rest")
	}
	if !secrets.IsEncrypted(raw["API_KEY"]) {
		t.Fatalf("secret not encrypted-at-rest: %q", raw["API_KEY"])
	}
	if raw["PLAIN"] != "plain-value" {
		t.Fatalf("plain config should not be encrypted: %q", raw["PLAIN"])
	}

	// ListEnv MASKS the secret value but returns plain config as-is.
	list, _ := svc.ListEnv(ctx, "org-1", app.ID)
	for _, e := range list {
		switch e.Key {
		case "API_KEY":
			if e.Value != "***" || !e.Secret {
				t.Fatalf("ListEnv leaked/under-masked secret: %+v", e)
			}
		case "PLAIN":
			if e.Value != "plain-value" || e.Secret {
				t.Fatalf("ListEnv mangled plain config: %+v", e)
			}
		}
	}
}

func TestSecretEnvInjectedViaKubeSecretNotValues(t *testing.T) {
	svc, fb := newSvcWithCipher(t)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1.27"})

	if _, err := svc.SetEnv(ctx, "org-1", app.ID, "API_KEY", "s3cret-value", true); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	if _, err := svc.SetEnv(ctx, "org-1", app.ID, "PLAIN", "plain-value", false); err != nil {
		t.Fatalf("set plain: %v", err)
	}

	// Redeploy applies the env: the secret must reach the k8s Secret (decrypted),
	// and the helm values Env must NOT contain the secret key/value.
	if _, err := svc.Deploy(ctx, "org-1", app.ID); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	secretName := appSecretName(app.ID)
	got, ok := fb.AppSecrets[app.Namespace+"/"+secretName]
	if !ok {
		t.Fatalf("EnsureAppSecret was not called for %s/%s; have %+v", app.Namespace, secretName, fb.AppSecrets)
	}
	if got["API_KEY"] != "s3cret-value" {
		t.Fatalf("k8s secret should carry the DECRYPTED value, got %q", got["API_KEY"])
	}

	// Inspect the applied workload values.
	var applied kube.Workload
	for _, w := range fb.Applied {
		applied = w
	}
	if applied.EnvSecretName != secretName {
		t.Fatalf("workload EnvSecretName = %q, want %q", applied.EnvSecretName, secretName)
	}
	if _, leaked := applied.Env["API_KEY"]; leaked {
		t.Fatalf("secret leaked into plaintext workload Env values")
	}
	if applied.Env["PLAIN"] != "plain-value" {
		t.Fatalf("plain config missing from workload Env: %+v", applied.Env)
	}
	// And the secret VALUE must not appear anywhere in the plaintext Env map.
	for _, v := range applied.Env {
		if strings.Contains(v, "s3cret-value") {
			t.Fatalf("secret value leaked into plaintext Env")
		}
	}
}

func TestDeployFailsOnUndecryptableEncryptedSecret(t *testing.T) {
	// An ENABLED cipher facing a real "v1:"-prefixed encrypted value it cannot
	// Open (wrong/rotated key) must FAIL the deploy rather than silently shipping
	// without the secret.
	svc, _ := newSvcWithCipher(t)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1.27"})

	// Inject a corrupt encrypted value directly (bypassing the cipher) so Decrypt
	// fails under the configured AES-GCM cipher.
	if err := svc.store.SetAppEnv(ctx, app.ID, "API_KEY", "v1:not-valid-base64-or-ciphertext", true); err != nil {
		t.Fatalf("seed corrupt secret: %v", err)
	}

	if _, err := svc.Deploy(ctx, "org-1", app.ID); err == nil {
		t.Fatalf("expected Deploy to fail on an undecryptable encrypted secret, got nil")
	}
}

func TestDeployToleratesLegacyPlaintextDecryptUnderEnabledCipher(t *testing.T) {
	// A NON-"v1:" (legacy plaintext) secret value is passed through by Decrypt and
	// must NOT fail the deploy even under an enabled cipher.
	svc, _ := newSvcWithCipher(t)
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1.27"})

	if err := svc.store.SetAppEnv(ctx, app.ID, "LEGACY", "plain-legacy", true); err != nil {
		t.Fatalf("seed legacy secret: %v", err)
	}
	if _, err := svc.Deploy(ctx, "org-1", app.ID); err != nil {
		t.Fatalf("legacy plaintext secret should not fail deploy: %v", err)
	}
}

func TestNoopCipherStoresSecretWithoutPanic(t *testing.T) {
	// No key configured (dev): SetEnv must not panic and round-trips the value
	// through the no-op cipher.
	st := store.NewMemoryStore()
	svc := NewService(st, kube.NewFakeBackend(), billing.NewService(st, nil))
	ctx := context.Background()
	app, _ := svc.CreateApp(ctx, "org-1", CreateAppInput{Name: "web", Image: "nginx:1.27"})
	if _, err := svc.SetEnv(ctx, "org-1", app.ID, "K", "v", true); err != nil {
		t.Fatalf("set secret (noop cipher): %v", err)
	}
	if _, err := svc.Deploy(ctx, "org-1", app.ID); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	got := svc.backend.(*kube.FakeBackend).AppSecrets[app.Namespace+"/"+appSecretName(app.ID)]
	if got["K"] != "v" {
		t.Fatalf("noop secret round-trip: %+v", got)
	}
}

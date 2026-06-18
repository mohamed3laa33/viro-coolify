package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestBuildValuesRendersImagePullSecret asserts a workload with an ImagePullSecret
// renders deployment.imagePullSecrets so a private built image can be pulled.
func TestBuildValuesRendersImagePullSecret(t *testing.T) {
	b := NewWithClient(testConfig(), fake.NewSimpleClientset(), &mockHelm{})

	vals := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "web", Kind: "app",
		Image: "ghcr.io/acme/o/p/a:tag", CPU: 1, MemoryMB: 256,
		ImagePullSecret: "vortex-registry-pull",
	}, "web.web.acme.vortex.v60ai.com")

	dep, ok := vals["deployment"].(map[string]any)
	if !ok {
		t.Fatalf("deployment values missing: %v", vals)
	}
	ips, ok := dep["imagePullSecrets"].([]map[string]any)
	if !ok || len(ips) != 1 {
		t.Fatalf("imagePullSecrets not rendered: %v", dep["imagePullSecrets"])
	}
	if ips[0]["name"] != "vortex-registry-pull" {
		t.Fatalf("imagePullSecret name = %v, want vortex-registry-pull", ips[0]["name"])
	}
}

// TestBuildValuesOmitsImagePullSecretWhenEmpty asserts public image workloads get
// no imagePullSecrets key.
func TestBuildValuesOmitsImagePullSecretWhenEmpty(t *testing.T) {
	b := NewWithClient(testConfig(), fake.NewSimpleClientset(), &mockHelm{})
	vals := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "web", Kind: "app", Image: "nginx:1",
	}, "web.web.acme.vortex.v60ai.com")
	dep := vals["deployment"].(map[string]any)
	if _, ok := dep["imagePullSecrets"]; ok {
		t.Fatalf("public image workload should not set imagePullSecrets")
	}
}

// TestEnsureImagePullSecretCopiesSource asserts the control-plane source secret is
// copied into the tenant namespace.
func TestEnsureImagePullSecretCopiesSource(t *testing.T) {
	cfg := testConfig()
	cfg.RegistryPullSecret = "src-pull"
	cfg.RegistryPullSecretNamespace = "vortex"
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "src-pull", Namespace: "vortex"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
	})
	b := NewWithClient(cfg, cs, &mockHelm{})

	if err := b.EnsureImagePullSecret(context.Background(), "vortex-acme-web", "tenant-pull"); err != nil {
		t.Fatalf("EnsureImagePullSecret: %v", err)
	}
	got, err := cs.CoreV1().Secrets("vortex-acme-web").Get(context.Background(), "tenant-pull", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("tenant pull secret not created: %v", err)
	}
	if got.Type != corev1.SecretTypeDockerConfigJson || string(got.Data[corev1.DockerConfigJsonKey]) != `{"auths":{}}` {
		t.Fatalf("copied pull secret mismatch: %+v", got)
	}

	// Idempotent: a second ensure updates rather than errors.
	if err := b.EnsureImagePullSecret(context.Background(), "vortex-acme-web", "tenant-pull"); err != nil {
		t.Fatalf("second EnsureImagePullSecret: %v", err)
	}
}

// TestEnsureImagePullSecretNoOpWhenUnconfigured asserts the call is a graceful
// no-op in local/dev (no source secret configured).
func TestEnsureImagePullSecretNoOpWhenUnconfigured(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewWithClient(testConfig(), cs, &mockHelm{}) // RegistryPullSecret empty
	if err := b.EnsureImagePullSecret(context.Background(), "vortex-acme-web", "tenant-pull"); err != nil {
		t.Fatalf("EnsureImagePullSecret no-op should not error: %v", err)
	}
	if _, err := cs.CoreV1().Secrets("vortex-acme-web").Get(context.Background(), "tenant-pull", metav1.GetOptions{}); err == nil {
		t.Fatalf("expected no secret created when unconfigured")
	}
}

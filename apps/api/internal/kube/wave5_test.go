package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// preActiveNS pre-creates an Active namespace so EnsureTenant's poll succeeds.
func preActiveNS(t *testing.T, cs *fake.Clientset, ns string) {
	t.Helper()
	_, _ = cs.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}, metav1.CreateOptions{})
}

func TestEnsureTenantCreatesServiceAccountAndNetworkPolicy(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ns := namespaceName("acme", "web")
	preActiveNS(t, cs, ns)

	b := NewWithClient(testConfig(), cs, &mockHelm{})
	if _, err := b.EnsureTenant(context.Background(), "acme", "web",
		Quota{MaxCPU: 4, MaxMemoryMB: 8192, MaxApps: 5}); err != nil {
		t.Fatalf("EnsureTenant: %v", err)
	}

	// Per-tenant ServiceAccount with token auto-mount disabled.
	sa, err := cs.CoreV1().ServiceAccounts(ns).Get(context.Background(), tenantServiceAccount, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get serviceaccount: %v", err)
	}
	if sa.AutomountServiceAccountToken == nil || *sa.AutomountServiceAccountToken {
		t.Fatalf("expected automountServiceAccountToken=false, got %v", sa.AutomountServiceAccountToken)
	}

	// NetworkPolicy isolating the tenant namespace.
	np, err := cs.NetworkingV1().NetworkPolicies(ns).Get(context.Background(), "vortex-tenant-isolation", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get networkpolicy: %v", err)
	}
	// Default-deny posture: both Ingress and Egress policy types present.
	var hasIngress, hasEgress bool
	for _, pt := range np.Spec.PolicyTypes {
		switch pt {
		case "Ingress":
			hasIngress = true
		case "Egress":
			hasEgress = true
		}
	}
	if !hasIngress || !hasEgress {
		t.Fatalf("policy types = %v, want both Ingress and Egress", np.Spec.PolicyTypes)
	}
	// Egress must carve cluster-internal ranges out of the external allowance so
	// the control-plane Postgres / other tenants are unreachable by IP.
	var foundExcept bool
	for _, rule := range np.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil && len(peer.IPBlock.Except) > 0 {
				foundExcept = true
			}
		}
	}
	if !foundExcept {
		t.Fatalf("expected an egress IPBlock with Except (cluster ranges blocked)")
	}
}

func TestEnsureTenantSAAndNPIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ns := namespaceName("acme", "web")
	preActiveNS(t, cs, ns)
	b := NewWithClient(testConfig(), cs, &mockHelm{})
	q := Quota{MaxCPU: 4, MaxMemoryMB: 8192, MaxApps: 5}
	if _, err := b.EnsureTenant(context.Background(), "acme", "web", q); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := b.EnsureTenant(context.Background(), "acme", "web", q); err != nil {
		t.Fatalf("second (idempotent): %v", err)
	}
}

func TestEnsureAppSecretCreatesUpdatesAndDeletes(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := NewWithClient(testConfig(), cs, &mockHelm{})
	ns := "vortex-acme-web"
	name := "vortex-env-app1"

	if err := b.EnsureAppSecret(context.Background(), ns, name, map[string]string{"API_KEY": "s3cret"}); err != nil {
		t.Fatalf("create app secret: %v", err)
	}
	sec, err := cs.CoreV1().Secrets(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if sec.Type != corev1.SecretTypeOpaque {
		t.Fatalf("secret type = %q", sec.Type)
	}
	if got := sec.StringData["API_KEY"]; got != "s3cret" {
		t.Fatalf("secret data = %q", got)
	}

	// Update: new key replaces old wholesale.
	if err := b.EnsureAppSecret(context.Background(), ns, name, map[string]string{"OTHER": "v"}); err != nil {
		t.Fatalf("update app secret: %v", err)
	}
	sec, _ = cs.CoreV1().Secrets(ns).Get(context.Background(), name, metav1.GetOptions{})
	if _, stale := sec.StringData["API_KEY"]; stale {
		t.Fatalf("removed key lingered after update")
	}

	// Empty data deletes the secret.
	if err := b.EnsureAppSecret(context.Background(), ns, name, nil); err != nil {
		t.Fatalf("delete app secret: %v", err)
	}
	if _, err := cs.CoreV1().Secrets(ns).Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatalf("expected secret deleted")
	}
}

func TestApplyWiresEnvFromAndServiceAccount(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mh := &mockHelm{}
	b := NewWithClient(testConfig(), cs, mh)

	_, _, err := b.Apply(context.Background(), Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:latest", CPU: 1, MemoryMB: 512,
		Env:           map[string]string{"PLAIN": "v"},
		EnvSecretName: "vortex-env-app1",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	deployment, _ := mh.lastValues["deployment"].(map[string]any)
	if deployment == nil {
		t.Fatalf("no deployment values rendered: %+v", mh.lastValues)
	}
	envFrom, ok := deployment["envFrom"].([]any)
	if !ok || len(envFrom) == 0 {
		t.Fatalf("expected deployment.envFrom referencing the secret, got %+v", deployment["envFrom"])
	}
	ref, _ := envFrom[0].(map[string]any)
	secretRef, _ := ref["secretRef"].(map[string]any)
	if secretRef["name"] != "vortex-env-app1" {
		t.Fatalf("envFrom secretRef = %+v, want vortex-env-app1", secretRef)
	}

	// Workloads run under the per-tenant ServiceAccount, not the default SA.
	saVals, _ := mh.lastValues["serviceAccount"].(map[string]any)
	if saVals == nil || saVals["name"] != tenantServiceAccount {
		t.Fatalf("serviceAccount values = %+v, want name=%q", saVals, tenantServiceAccount)
	}
	// The chart must NOT render/own its own ServiceAccount — that would collide
	// with the imperative locked-down SA and re-enable token auto-mount.
	if create, ok := saVals["create"].(bool); !ok || create {
		t.Fatalf("serviceAccount.create = %v, want false (chart must not render a colliding SA)", saVals["create"])
	}
	// Pod-level token auto-mount must be disabled for tenant workloads.
	if am, ok := saVals["automountServiceAccountToken"].(bool); !ok || am {
		t.Fatalf("serviceAccount.automountServiceAccountToken = %v, want false", saVals["automountServiceAccountToken"])
	}
}

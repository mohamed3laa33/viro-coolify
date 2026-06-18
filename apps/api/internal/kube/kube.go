package kube

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/yaml"
)

// Config holds the static deployment settings KubeBackend needs. The overcommit
// factors are admin/DB-configurable and passed in per-call sites (never hardcoded).
type Config struct {
	// BaseDomain is the platform apex, e.g. "vortex.v60ai.com". Workload hosts are
	// <name>.<proj>.<org>.<BaseDomain>.
	BaseDomain string
	// ChartPath is the path to the team's common-chart, e.g. deploy/charts/common-chart.
	ChartPath string
	// GatewayName / GatewayNamespace identify the SHARED Gateway every per-app
	// HTTPRoute attaches to via parentRefs.
	GatewayName      string
	GatewayNamespace string
	// ClusterIssuer is the cert-manager ClusterIssuer name that signs per-tenant
	// custom-domain certificates (e.g. "vortex-letsencrypt"). Empty disables
	// per-domain TLS issuance (EnsureDomainCertificate then no-ops).
	ClusterIssuer string
	// CPUOvercommitFactor / MemoryOvercommitFactor scale requested size down to the
	// scheduler requests (e.g. 0.2 and 0.35). Limits stay at the full requested size.
	CPUOvercommitFactor    float64
	MemoryOvercommitFactor float64
	// HelmTimeout bounds each `helm upgrade` (with --wait --atomic). Zero falls
	// back to defaultHelmTimeout. Sourced from the VORTEX_* env (admin-tunable).
	HelmTimeout time.Duration

	// RegistryPullSecret / RegistryPullSecretNamespace identify the control-plane
	// SOURCE Secret (kubernetes.io/dockerconfigjson) whose .dockerconfigjson is
	// copied into each tenant namespace by EnsureImagePullSecret so a private built
	// image can be pulled. Empty in local/dev: EnsureImagePullSecret then no-ops.
	RegistryPullSecret          string
	RegistryPullSecretNamespace string
}

// defaultHelmTimeout is the fallback per-Apply helm deadline when cfg.HelmTimeout
// is unset.
const defaultHelmTimeout = 5 * time.Minute

// minDBStorageGB is the safe-minimum persistent-volume size (GiB) the backend
// clamps a database to when its StorageGB is 0/unset, so a stateful workload is
// never rendered volume-less (which would lose data on restart/Stop). The
// platform normally supplies a larger admin-configured default before Apply.
const minDBStorageGB = 1

// KEDA ScaledObject GroupVersionResource, used to pause/resume autoscaling so
// Stop/Start are not reverted by the autoscaler.
var scaledObjectGVR = schema.GroupVersionResource{
	Group:    "keda.sh",
	Version:  "v1alpha1",
	Resource: "scaledobjects",
}

// kedaPausedAnnotation, when set on a ScaledObject, pins the workload to the
// annotated replica count (KEDA stops scaling it). Removing it resumes scaling.
const kedaPausedAnnotation = "autoscaling.keda.sh/paused-replicas"

// certificateGVR is the cert-manager Certificate CRD, driven via the dynamic
// client (it is not in the typed clientset).
var certificateGVR = schema.GroupVersionResource{
	Group:    "cert-manager.io",
	Version:  "v1",
	Resource: "certificates",
}

// gatewayGVR is the Gateway API Gateway CRD, driven via the dynamic client.
var gatewayGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gateways",
}

// maxGatewayListeners is the Gateway API hard limit on listeners per Gateway
// (spec: a Gateway may define at most 64 listeners). EnsureGatewayListener
// refuses to add a per-domain HTTPS listener once this ceiling is reached rather
// than producing an invalid Gateway the controller would reject wholesale. At
// that scale the platform should move to a dedicated per-tenant Gateway; this
// guard makes the limit explicit instead of silently corrupting the shared one.
const maxGatewayListeners = 64

// KubeBackend is the real Backend: a typed clientset for namespace/quota/status/logs
// plus a HelmRunner for chart installs.
type KubeBackend struct {
	cfg     Config
	client  kubernetes.Interface
	dynamic dynamic.Interface
	helm    HelmRunner
}

var _ Backend = (*KubeBackend)(nil)

// New builds a KubeBackend from in-cluster config, falling back to the supplied
// kubeconfig path (or the default loading rules when kubeconfigPath is empty).
// Pass helm=nil to use the real `helm` binary.
func New(cfg Config, kubeconfigPath string, helm HelmRunner) (*KubeBackend, error) {
	restCfg, err := restConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kube: build clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kube: build dynamic client: %w", err)
	}
	b := NewWithClient(cfg, cs, helm)
	b.dynamic = dyn
	return b, nil
}

// NewWithClient builds a KubeBackend from an existing clientset (used by tests
// with client-go's fake clientset). Pass helm=nil to use the real `helm` binary.
// The dynamic client (used to pause/resume KEDA) is left nil here; set it via
// NewWithClients when a fake dynamic client is needed.
func NewWithClient(cfg Config, client kubernetes.Interface, helm HelmRunner) *KubeBackend {
	if helm == nil {
		helm = NewExecHelmRunner("")
	}
	return &KubeBackend{cfg: cfg, client: client, helm: helm}
}

// NewWithClients is like NewWithClient but also wires a dynamic client, so tests
// can assert KEDA ScaledObject pause/resume behavior with a fake dynamic client.
func NewWithClients(cfg Config, client kubernetes.Interface, dyn dynamic.Interface, helm HelmRunner) *KubeBackend {
	b := NewWithClient(cfg, client, helm)
	b.dynamic = dyn
	return b
}

// NewClientset builds a typed Kubernetes clientset from in-cluster config,
// falling back to the supplied kubeconfig path (or the default loading rules
// when empty). It is shared by other packages (e.g. the build pipeline) that
// need a clientset without the full KubeBackend.
func NewClientset(kubeconfigPath string) (kubernetes.Interface, error) {
	restCfg, err := restConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kube: build clientset: %w", err)
	}
	return cs, nil
}

// restConfig prefers in-cluster config and falls back to a kubeconfig file.
func restConfig(kubeconfigPath string) (*rest.Config, error) {
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		rules.ExplicitPath = kubeconfigPath
	}
	c, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kube: load kubeconfig: %w", err)
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// EnsureTenant
// ---------------------------------------------------------------------------

// EnsureTenant creates the org-project namespace (idempotent, polled until Active)
// plus a ResourceQuota and LimitRange derived from the plan quota.
func (b *KubeBackend) EnsureTenant(ctx context.Context, orgSlug, projSlug string, q Quota) (string, error) {
	ns := namespaceName(orgSlug, projSlug)

	nsObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vortex",
				"vortex.io/org":                sanitize(orgSlug),
				"vortex.io/project":            sanitize(projSlug),
			},
		},
	}
	if _, err := b.client.CoreV1().Namespaces().Create(ctx, nsObj, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
		return "", fmt.Errorf("kube: create namespace %s: %w", ns, err)
	}

	if err := b.waitNamespaceActive(ctx, ns); err != nil {
		return "", err
	}

	if err := b.applyResourceQuota(ctx, ns, q); err != nil {
		return "", err
	}
	if err := b.applyLimitRange(ctx, ns, q); err != nil {
		return "", err
	}
	// Per-tenant ServiceAccount (token auto-mount disabled) so workloads no longer
	// run under the namespace default SA / its auto-mounted API token.
	if err := b.ensureServiceAccount(ctx, ns); err != nil {
		return "", err
	}
	// Default-deny + scoped-egress NetworkPolicy isolating the tenant namespace
	// from other tenants and the control-plane datastore.
	if err := b.ensureNetworkPolicy(ctx, ns); err != nil {
		return "", err
	}
	return ns, nil
}

// tenantServiceAccount is the per-namespace ServiceAccount every tenant workload
// runs under (instead of the namespace default SA). Its API token is NOT
// auto-mounted, so a compromised tenant pod cannot reach the Kubernetes API with
// ambient credentials.
const tenantServiceAccount = "vortex-workload"

// ensureServiceAccount creates (idempotently) the per-namespace workload
// ServiceAccount with automountServiceAccountToken=false.
func (b *KubeBackend) ensureServiceAccount(ctx context.Context, ns string) error {
	no := false
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tenantServiceAccount,
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vortex"},
		},
		AutomountServiceAccountToken: &no,
	}
	return upsert(ctx,
		func() error {
			_, err := b.client.CoreV1().ServiceAccounts(ns).Create(ctx, sa, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.client.CoreV1().ServiceAccounts(ns).Get(ctx, sa.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			cur.AutomountServiceAccountToken = &no
			_, err = b.client.CoreV1().ServiceAccounts(ns).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

// ensureNetworkPolicy installs a default-deny-ingress + scoped-egress policy on
// the tenant namespace. It (a) denies cross-tenant ingress (only intra-namespace
// + the shared Gateway namespace may reach the workload), (b) allows DNS and
// general external egress, and (c) BLOCKS egress to the control-plane Postgres /
// other tenant namespaces by excluding the cluster-internal RFC1918 ranges from
// the broad egress allowance while still permitting public internet egress.
func (b *KubeBackend) ensureNetworkPolicy(ctx context.Context, ns string) error {
	tcp := corev1.ProtocolTCP
	udp := corev1.ProtocolUDP
	dnsPort := intstrPtr(53)

	// Ingress: intra-namespace (same tenant) + the shared Gateway namespace.
	ingressFrom := []netv1.NetworkPolicyPeer{
		{PodSelector: &metav1.LabelSelector{}}, // same namespace
	}
	if gwNS := b.cfg.GatewayNamespace; gwNS != "" {
		ingressFrom = append(ingressFrom, netv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": gwNS},
			},
		})
	}

	// Egress: DNS to kube-dns, intra-namespace, and external (non-cluster) IPs.
	// The except blocks carve the private/cluster ranges OUT of the external
	// allowance so a tenant pod cannot reach the control-plane Postgres or other
	// tenant pods/services by IP — only the public internet.
	externalEgress := &netv1.IPBlock{
		CIDR: "0.0.0.0/0",
		Except: []string{
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
		},
	}

	np := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vortex-tenant-isolation",
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vortex"},
		},
		Spec: netv1.NetworkPolicySpec{
			// Empty pod selector => applies to ALL pods in the namespace.
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress, netv1.PolicyTypeEgress},
			Ingress: []netv1.NetworkPolicyIngressRule{
				{From: ingressFrom},
			},
			Egress: []netv1.NetworkPolicyEgressRule{
				// DNS resolution (UDP+TCP/53) to anywhere in-cluster.
				{
					Ports: []netv1.NetworkPolicyPort{
						{Protocol: &udp, Port: dnsPort},
						{Protocol: &tcp, Port: dnsPort},
					},
				},
				// Intra-namespace (same tenant: app <-> its own database).
				{To: []netv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}},
				// External internet egress, excluding cluster-internal ranges so the
				// control-plane datastore and other tenants are unreachable by IP.
				{To: []netv1.NetworkPolicyPeer{{IPBlock: externalEgress}}},
			},
		},
	}
	return upsert(ctx,
		func() error {
			_, err := b.client.NetworkingV1().NetworkPolicies(ns).Create(ctx, np, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.client.NetworkingV1().NetworkPolicies(ns).Get(ctx, np.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			cur.Spec = np.Spec
			_, err = b.client.NetworkingV1().NetworkPolicies(ns).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

// EnsureAppSecret creates/updates the per-app Opaque Secret holding the
// workload's SECRET env (already-decrypted plaintext values). When data is empty
// it deletes the Secret so no stale secret material lingers after the last
// secret key is removed. The chart wires it via envFrom secretRef.
func (b *KubeBackend) EnsureAppSecret(ctx context.Context, ns, name string, data map[string]string) error {
	if name == "" {
		return nil
	}
	if len(data) == 0 {
		err := b.client.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("kube: delete app secret %s/%s: %w", ns, name, err)
		}
		return nil
	}
	sd := make(map[string]string, len(data))
	for k, v := range data {
		sd[k] = v
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vortex"},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: sd,
	}
	return upsert(ctx,
		func() error {
			_, err := b.client.CoreV1().Secrets(ns).Create(ctx, sec, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.client.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			cur.Type = corev1.SecretTypeOpaque
			cur.StringData = sd
			cur.Data = nil // replace wholesale so removed keys don't linger
			_, err = b.client.CoreV1().Secrets(ns).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

// intstrPtr returns a pointer to an IntOrString port value.
func intstrPtr(p int32) *intstr.IntOrString {
	v := intstr.FromInt32(p)
	return &v
}

// EnsureImagePullSecret upserts a kubernetes.io/dockerconfigjson Secret named
// "name" in tenant namespace "ns" by copying the dockerconfigjson from the
// configured control-plane source secret (cfg.RegistryPullSecret in
// cfg.RegistryPullSecretNamespace). When no source is configured it no-ops, so
// local/dev (and any non-registry flow) keeps working.
func (b *KubeBackend) EnsureImagePullSecret(ctx context.Context, ns, name string) error {
	if b.cfg.RegistryPullSecret == "" || name == "" {
		return nil
	}
	srcNS := b.cfg.RegistryPullSecretNamespace
	if srcNS == "" {
		srcNS = "vortex"
	}
	src, err := b.client.CoreV1().Secrets(srcNS).Get(ctx, b.cfg.RegistryPullSecret, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("kube: read source pull secret %s/%s: %w", srcNS, b.cfg.RegistryPullSecret, err)
	}
	dockercfg := src.Data[corev1.DockerConfigJsonKey]
	if len(dockercfg) == 0 {
		return fmt.Errorf("kube: source pull secret %s/%s missing %s", srcNS, b.cfg.RegistryPullSecret, corev1.DockerConfigJsonKey)
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "vortex"},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: dockercfg},
	}
	return upsert(ctx,
		func() error {
			_, err := b.client.CoreV1().Secrets(ns).Create(ctx, sec, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.client.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			cur.Type = sec.Type
			cur.Data = sec.Data
			_, err = b.client.CoreV1().Secrets(ns).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

func (b *KubeBackend) waitNamespaceActive(ctx context.Context, ns string) error {
	err := wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			got, err := b.client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return got.Status.Phase == corev1.NamespaceActive, nil
		})
	if err != nil {
		return fmt.Errorf("kube: namespace %s not Active: %w", ns, err)
	}
	return nil
}

// applyResourceQuota caps namespace-wide CPU/memory limits and the workload count.
// The quota bounds the FULL requested sizes (limits), letting overcommit pack the
// underlying nodes via the much smaller requests.
func (b *KubeBackend) applyResourceQuota(ctx context.Context, ns string, q Quota) error {
	rq := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "vortex-quota", Namespace: ns},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceLimitsCPU:    resource.MustParse(milliCPU(q.MaxCPU)),
				corev1.ResourceLimitsMemory: resource.MustParse(mib(q.MaxMemoryMB)),
				corev1.ResourcePods:         *resource.NewQuantity(int64(q.MaxApps), resource.DecimalSI),
			},
		},
	}
	return upsert(ctx,
		func() error {
			_, err := b.client.CoreV1().ResourceQuotas(ns).Create(ctx, rq, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.client.CoreV1().ResourceQuotas(ns).Get(ctx, rq.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			cur.Spec = rq.Spec
			_, err = b.client.CoreV1().ResourceQuotas(ns).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

// applyLimitRange supplies default container requests/limits so pods that omit
// resources still get sane values within the quota. The defaults are the minimal
// workload size from platform settings (admin/DB-driven), with the overcommit
// factor applied to the request — never hardcoded policy.
func (b *KubeBackend) applyLimitRange(ctx context.Context, ns string, q Quota) error {
	// Minimal built-in fallbacks when settings don't supply a default size.
	defCPU := q.DefaultCPU
	if defCPU <= 0 {
		defCPU = 0.1
	}
	defMem := q.DefaultMemoryMB
	if defMem <= 0 {
		defMem = 128
	}
	cpuFactor := q.CPUOvercommitFactor
	if cpuFactor <= 0 {
		cpuFactor = b.cfg.CPUOvercommitFactor
	}
	memFactor := q.MemoryOvercommitFactor
	if memFactor <= 0 {
		memFactor = b.cfg.MemoryOvercommitFactor
	}
	lr := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "vortex-limits", Namespace: ns},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{{
				Type: corev1.LimitTypeContainer,
				Default: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(milliCPU(defCPU)),
					corev1.ResourceMemory: resource.MustParse(mib(defMem)),
				},
				DefaultRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(milliCPU(defCPU * cpuFactor)),
					corev1.ResourceMemory: resource.MustParse(mib(int(float64(defMem) * memFactor))),
				},
			}},
		},
	}
	return upsert(ctx,
		func() error {
			_, err := b.client.CoreV1().LimitRanges(ns).Create(ctx, lr, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.client.CoreV1().LimitRanges(ns).Get(ctx, lr.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			cur.Spec = lr.Spec
			_, err = b.client.CoreV1().LimitRanges(ns).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

// upsert runs create, and on AlreadyExists falls back to update.
func upsert(ctx context.Context, create, update func() error) error {
	err := create()
	if err == nil {
		return nil
	}
	if errors.IsAlreadyExists(err) {
		return update()
	}
	return err
}

// ---------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------

// Apply renders chart values for the workload and runs `helm upgrade --install`.
func (b *KubeBackend) Apply(ctx context.Context, w Workload) (string, string, error) {
	ns := namespaceName(w.OrgSlug, w.ProjectSlug)
	rel := releaseName(w.Name)
	h := host(w.Name, w.ProjectSlug, w.OrgSlug, b.cfg.BaseDomain)

	values := b.buildValues(w, h)
	out, err := yaml.Marshal(values)
	if err != nil {
		return "", "", fmt.Errorf("kube: marshal values: %w", err)
	}

	f, err := os.CreateTemp("", "vortex-values-*.yaml")
	if err != nil {
		return "", "", fmt.Errorf("kube: temp values: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(out); err != nil {
		_ = f.Close()
		return "", "", fmt.Errorf("kube: write values: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", "", fmt.Errorf("kube: close values: %w", err)
	}

	// Run helm on a DETACHED context with its own deadline: a deploy must not be
	// abandoned half-way just because the originating HTTP request was cancelled.
	// --wait --atomic make a failed rollout auto-revert, so the returned status
	// reflects readiness rather than "submitted".
	timeout := b.helmTimeout()
	helmCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := []string{
		"upgrade", "--install", rel, b.cfg.ChartPath,
		"-n", ns, "--create-namespace",
		"-f", f.Name(),
		"--wait", "--atomic", "--timeout", helmDuration(timeout),
	}
	if _, err := b.helm.Run(helmCtx, args...); err != nil {
		return "", "", err
	}
	return rel, h, nil
}

// helmTimeout returns the configured per-Apply helm deadline, or the default.
func (b *KubeBackend) helmTimeout() time.Duration {
	if b.cfg.HelmTimeout > 0 {
		return b.cfg.HelmTimeout
	}
	return defaultHelmTimeout
}

// helmDuration renders a timeout for helm's --timeout flag (e.g. "5m0s").
func helmDuration(d time.Duration) string { return d.String() }

// buildValues maps a Workload to common-chart values.
func (b *KubeBackend) buildValues(w Workload, h string) map[string]any {
	stateful := strings.EqualFold(w.Kind, "database")
	port := w.Port
	if port <= 0 {
		port = defaultPort(w)
	}

	// Image comes from the catalog template / build (DB/admin-driven), not a
	// hardcoded constant. The WordPress recipe (probes, wp-config wiring) keys off
	// ServiceTemplateKey, so it still applies regardless of the image source.
	repo, tag := splitImage(w.Image)
	env := mergeEnv(w)

	// Overcommit factors are live per-Apply from platform settings; fall back to
	// the backend's configured defaults when the caller leaves them unset.
	cpuFactor := w.CPUOvercommitFactor
	if cpuFactor <= 0 {
		cpuFactor = b.cfg.CPUOvercommitFactor
	}
	memFactor := w.MemoryOvercommitFactor
	if memFactor <= 0 {
		memFactor = b.cfg.MemoryOvercommitFactor
	}
	resources := overcommitResources(w.CPU, w.MemoryMB, cpuFactor, memFactor)

	deployment := map[string]any{
		"enabled":      true,
		"stateful":     stateful,
		"replicaCount": 1,
		"image": map[string]any{
			"repository": repo,
			"tag":        tag,
			"pullPolicy": "IfNotPresent",
		},
		"resources": resources,
		"env":       env,
	}

	// Attach the tenant-namespace pull secret for PRIVATE built images so the
	// Deployment/StatefulSet podSpec.imagePullSecrets is populated (the common-chart
	// renders deployment.imagePullSecrets verbatim). Without it a built private
	// image fails with ImagePullBackOff.
	if w.ImagePullSecret != "" {
		deployment["imagePullSecrets"] = []map[string]any{{"name": w.ImagePullSecret}}
	}

	// SECRET env injection: reference the per-app Kubernetes Secret via envFrom so
	// secret values are delivered from the Secret at runtime and are NOT baked into
	// the helm release values. Non-secret config still flows through deployment.env.
	if w.EnvSecretName != "" {
		deployment["envFrom"] = []map[string]any{{
			"secretRef": map[string]any{"name": w.EnvSecretName},
		}}
	}

	// StatefulSet reads container ports from deployment.ports; Deployment reads
	// them from service.ports. Set both so either controller renders correctly.
	deployment["ports"] = []map[string]any{{
		"name":          "http",
		"containerPort": port,
		"protocol":      "TCP",
	}}

	// Persistence: stateful (database) workloads get a PersistentVolume so data
	// survives Stop/scale/restart. The chart renders volumeClaimTemplates on the
	// StatefulSet and a RETAIN PVC retention policy; the data volume is mounted at
	// the engine's on-disk data dir. Stateless app/service workloads never get a
	// volume (no StorageGB rendered) so the apps path is unchanged.
	persistence := b.buildPersistence(w, stateful, deployment)

	// Redis has no password env honored by the official image, so enforce auth via
	// the container command (redis-server --requirepass <pw>). The password comes
	// from the injected REDIS_PASSWORD env (set by the platform on CreateDatabase).
	if stateful {
		if args := redisArgs(w.ServiceTemplateKey, env["REDIS_PASSWORD"]); len(args) > 0 {
			deployment["command"] = args
		}
	}

	// WordPress: DB-independent probes per the volo recipe so import-driven DB
	// credential changes don't trigger restarts.
	if isWordPress(w) {
		deployment["livenessProbe"] = map[string]any{
			"tcpSocket":           map[string]any{"port": port},
			"initialDelaySeconds": 60,
			"periodSeconds":       30,
			"timeoutSeconds":      5,
			"failureThreshold":    5,
		}
		deployment["readinessProbe"] = map[string]any{
			"httpGet":             map[string]any{"path": "/wp-includes/images/blank.gif", "port": port},
			"initialDelaySeconds": 30,
			"periodSeconds":       15,
			"timeoutSeconds":      5,
			"failureThreshold":    6,
		}
	} else {
		// Default conservative TCP probes for every other app/service/database kind
		// so a RollingUpdate doesn't shift traffic to a not-yet-listening pod. These
		// only check that the workload's port accepts a connection (engine-agnostic).
		if _, ok := deployment["readinessProbe"]; !ok {
			deployment["readinessProbe"] = map[string]any{
				"tcpSocket":           map[string]any{"port": port},
				"initialDelaySeconds": 5,
				"periodSeconds":       10,
				"timeoutSeconds":      3,
				"failureThreshold":    3,
			}
		}
		if _, ok := deployment["livenessProbe"]; !ok {
			deployment["livenessProbe"] = map[string]any{
				"tcpSocket":           map[string]any{"port": port},
				"initialDelaySeconds": 30,
				"periodSeconds":       30,
				"timeoutSeconds":      5,
				"failureThreshold":    5,
			}
		}
	}

	service := map[string]any{
		"enabled": true,
		"type":    "ClusterIP",
		"ports": []map[string]any{{
			"name":       "http",
			"port":       port,
			"targetPort": port,
			"protocol":   "TCP",
		}},
	}

	// KEDA: admin/DB-driven autoscaling. The min/max/polling/cooldown/trigger come
	// from the resolved Scaling config (platform settings + per-app overrides), not
	// hardcoded constants. A STATELESS workload may scale to ZERO (min/idle = 0 — the
	// core cost lever); a stateful (database) workload is floored to 1 here so a
	// database never scales to zero. The CPU trigger is always present (it needs no
	// extra add-on); an HTTP-concurrency trigger is added only when explicitly gated
	// on (it requires the keda-http-add-on to actually wake the workload).
	keda := buildKeda(w.Scaling, stateful)

	// HTTPRoute attaching to the SHARED Gateway via parentRefs. Databases are
	// internal-only (no public route); apps/services get the generated host plus
	// any custom domains.
	hostnames := append([]string{h}, sanitizeDomains(w.Domains)...)
	gateway := map[string]any{
		"enabled": !stateful,
		"parentRefs": []map[string]any{{
			"name":      b.cfg.GatewayName,
			"namespace": b.cfg.GatewayNamespace,
		}},
		"hostnames":   hostnames,
		"backendPort": port,
	}

	values := map[string]any{
		// Force the chart to name every rendered object (Deployment, StatefulSet,
		// Service, ScaledObject) EXACTLY the release name. Without this the
		// common-chart's fullname template appends "-common-chart", so the
		// lifecycle Get/Patch calls (scale/Restart/Status/pauseKeda/resumeKeda),
		// which target the bare release name, would 404 on a real cluster. The
		// chart's _helpers.tpl honors fullnameOverride.
		"fullnameOverride": releaseName(w.Name),
		"deployment":       deployment,
		"service":          service,
		"keda":             keda,
		"gateway":          gateway,
		// Bind every workload to the per-tenant ServiceAccount (created
		// imperatively by EnsureTenant with API-token auto-mount disabled) rather
		// than the namespace default SA. create=false so the chart does NOT render
		// its own ServiceAccount of the same name — that would collide with and
		// override the locked-down imperative SA, re-enabling the auto-mounted API
		// token. The pod additionally sets automountServiceAccountToken=false as
		// defense-in-depth so no API token is mounted even if the SA default drifts.
		"serviceAccount": map[string]any{
			"enabled":                      true,
			"create":                       false,
			"name":                         tenantServiceAccount,
			"automountServiceAccountToken": false,
		},
	}
	for k, v := range persistence {
		values[k] = v
	}

	// Multi-region SEAM: stamp the (validated) region onto every rendered object via
	// extraLabels (common-chart.labels), and onto the pods via podLabels + a pod
	// annotation, so a FUTURE multi-cluster router can place/route by region. A single
	// cluster ignores it (no scheduling effect today). Empty region => no label/annotation.
	if region := sanitize(w.Region); region != "" {
		values["extraLabels"] = map[string]any{regionLabelKey: region}
		mergeStringMap(deployment, "podLabels", map[string]any{regionLabelKey: region})
		mergeStringMap(deployment, "additionalPodAnnotations", map[string]any{regionAnnotationKey: w.Region})
	}

	return values
}

// regionLabelKey is the workload label carrying the placement region (the
// multi-region seam). regionAnnotationKey carries the raw (un-sanitized) region
// as a pod annotation for human/router consumption.
const (
	regionLabelKey      = "vortex.v60ai.com/region"
	regionAnnotationKey = "vortex.v60ai.com/region"
)

// mergeStringMap merges add into the map stored at parent[key] (creating it if
// absent), preserving any existing entries. Used to attach region label/annotation
// without clobbering other values already set on the deployment block.
func mergeStringMap(parent map[string]any, key string, add map[string]any) {
	cur, _ := parent[key].(map[string]any)
	if cur == nil {
		cur = map[string]any{}
	}
	for k, v := range add {
		cur[k] = v
	}
	parent[key] = cur
}

// buildPersistence renders the chart's StatefulSet persistence knobs for a
// stateful (database) workload: a volumeClaimTemplate sized from StorageGB and
// mounted at the engine's data dir, plus a RETAIN PVC retention policy so a
// Stop (KEDA pause + scale 0), scale, or restart NEVER deletes the data volume.
// It also appends the data volumeMount to deployment.extraVolumeMounts.
//
// For stateless workloads it returns nil, leaving the apps path and chart
// defaults untouched. For a stateful (database) workload it NEVER returns nil:
// a 0/legacy StorageGB is clamped to a safe minimum so the chart always renders
// a retained volume rather than a volume-less StatefulSet with Delete retention
// (which would wipe data on any restart/Stop).
func (b *KubeBackend) buildPersistence(w Workload, stateful bool, deployment map[string]any) map[string]any {
	if !stateful {
		return nil
	}
	storageGB := w.StorageGB
	if storageGB <= 0 {
		// Defense-in-depth: a database must always be durable. Fall back to the
		// minimum size rather than rendering a volume-less StatefulSet.
		storageGB = minDBStorageGB
	}
	mountPath := dataDir(w.ServiceTemplateKey)
	if mountPath == "" {
		// Fall back to a generic data dir so an unknown engine still gets durable
		// storage rather than silently losing data.
		mountPath = "/data"
	}
	const volName = "data"

	// Mount the volumeClaimTemplate into the container at the engine data dir.
	mount := map[string]any{"name": volName, "mountPath": mountPath}
	if existing, ok := deployment["extraVolumeMounts"].([]map[string]any); ok {
		deployment["extraVolumeMounts"] = append(existing, mount)
	} else {
		deployment["extraVolumeMounts"] = []map[string]any{mount}
	}

	vct := map[string]any{
		"name":        volName,
		"accessModes": []string{"ReadWriteOnce"},
		"resources": map[string]any{
			"requests": map[string]any{"storage": gib(storageGB)},
		},
		"volumeMode": "Filesystem",
	}
	if sc := strings.TrimSpace(w.StorageClass); sc != "" {
		vct["storageClassName"] = sc
	}

	return map[string]any{
		"volumeClaimTemplates": []map[string]any{vct},
		// RETAIN on both axes: Stop scales the StatefulSet to 0 (whenScaled) and a
		// later teardown deletes it (whenDeleted) — neither must wipe the PVC.
		"persistentVolumeClaimRetentionPolicy": map[string]any{
			"whenScaled":  "Retain",
			"whenDeleted": "Retain",
		},
	}
}

func scaleTargetKind(stateful bool) string {
	if stateful {
		return "StatefulSet"
	}
	return "Deployment"
}

// BuildKedaForTest exposes buildKeda for cross-package tests (platform) so they can
// assert the rendered ScaledObject (scale-to-zero for stateless, floor of 1 for
// stateful) without standing up the full helm render path.
func BuildKedaForTest(sc Scaling, stateful bool) map[string]any { return buildKeda(sc, stateful) }

// buildKeda renders the chart's `keda` values block from the resolved Scaling
// config. It is the single place the ScaledObject min/max/polling/cooldown/triggers
// are decided, so the values are fully admin/DB-driven (no hardcoded autoscaling
// constants on the deploy path).
//
//   - A STATELESS workload may scale to ZERO: when MinReplicas<=0 we render
//     minReplicaCount=0 AND idleReplicaCount=0 so KEDA returns the workload to zero
//     during the cooldown. This is the platform's core cost lever.
//   - A STATEFUL (database) workload is floored to a minimum of 1 — a database must
//     never scale to zero (its single replica owns the data volume).
//   - The CPU-utilization trigger is always present (needs no add-on). An
//     HTTP-concurrency trigger is appended only when HTTPTrigger is set; true
//     HTTP-wake additionally requires the keda-http-add-on installed in-cluster.
func buildKeda(sc Scaling, stateful bool) map[string]any {
	minReplicas := sc.MinReplicas
	maxReplicas := sc.MaxReplicas
	polling := sc.PollingInterval
	cooldown := sc.CooldownPeriod
	cpuUtil := sc.CPUUtilization

	// Built-in conservative fallbacks for an unset (zero-value) Scaling, so a caller
	// that does not populate it still produces a valid ScaledObject.
	if maxReplicas <= 0 {
		maxReplicas = 5
	}
	if polling <= 0 {
		polling = 30
	}
	if cooldown <= 0 {
		cooldown = 300
	}
	if cpuUtil <= 0 {
		cpuUtil = 70
	}
	if minReplicas < 0 {
		minReplicas = 0
	}
	if minReplicas > maxReplicas {
		// Keep the bounds coherent: the floor can never exceed the ceiling.
		minReplicas = maxReplicas
	}
	// Databases never scale to zero — floor the minimum at 1.
	if stateful && minReplicas < 1 {
		minReplicas = 1
	}

	triggers := []map[string]any{{
		"type":       "cpu",
		"metricType": "Utilization",
		"metadata":   map[string]any{"value": fmt.Sprintf("%d", cpuUtil)},
	}}
	// HTTP-concurrency trigger (request-rate / true HTTP-wake). Gated behind a
	// setting because it needs the keda-http-add-on; without the add-on the trigger
	// is inert, so it is opt-in.
	if sc.HTTPTrigger && !stateful {
		triggers = append(triggers, map[string]any{
			"type":     "http",
			"metadata": map[string]any{"scaledObjectName": ""},
		})
	}

	keda := map[string]any{
		"enabled":         true,
		"scaleTargetKind": scaleTargetKind(stateful),
		"minReplicaCount": minReplicas,
		"maxReplicaCount": maxReplicas,
		"pollingInterval": polling,
		"cooldownPeriod":  cooldown,
		"triggers":        triggers,
	}
	// Scale-to-zero: KEDA needs idleReplicaCount=0 (with minReplicaCount=0) to take
	// a stateless workload all the way down to zero during the cooldown window.
	if minReplicas == 0 {
		keda["idleReplicaCount"] = 0
	}
	return keda
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// Stop scales the workload to zero. When a KEDA ScaledObject owns the workload,
// scaling the controller directly is reverted within ~30s, so Stop first PAUSES
// KEDA (paused-replicas: "0") and then scales the controller to 0. With no
// ScaledObject it falls back to scaling alone.
func (b *KubeBackend) Stop(ctx context.Context, namespace, release string) error {
	if err := b.pauseKeda(ctx, namespace, release, "0"); err != nil {
		return err
	}
	return b.scale(ctx, namespace, release, 0)
}

// Start resumes the workload: it removes the KEDA pause annotation (handing
// replica control back to the autoscaler) and scales the controller to 1 so the
// workload comes up immediately even before KEDA next reconciles.
func (b *KubeBackend) Start(ctx context.Context, namespace, release string) error {
	if err := b.resumeKeda(ctx, namespace, release); err != nil {
		return err
	}
	return b.scale(ctx, namespace, release, 1)
}

// pauseKeda patches the release's KEDA ScaledObject with the paused-replicas
// annotation. A missing ScaledObject (or no dynamic client) is not an error: the
// caller falls back to plain scaling.
func (b *KubeBackend) pauseKeda(ctx context.Context, namespace, release, replicas string) error {
	if b.dynamic == nil {
		return nil
	}
	patch := []byte(fmt.Sprintf(
		`{"metadata":{"annotations":{%q:%q}}}`, kedaPausedAnnotation, replicas))
	_, err := b.dynamic.Resource(scaledObjectGVR).Namespace(namespace).
		Patch(ctx, release, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("kube: pause keda %s/%s: %w", namespace, release, err)
	}
	return nil
}

// resumeKeda removes the paused-replicas annotation from the ScaledObject. A
// missing ScaledObject (or no dynamic client) is not an error.
func (b *KubeBackend) resumeKeda(ctx context.Context, namespace, release string) error {
	if b.dynamic == nil {
		return nil
	}
	// JSON Merge Patch: setting an annotation to null removes it.
	patch := []byte(fmt.Sprintf(
		`{"metadata":{"annotations":{%q:null}}}`, kedaPausedAnnotation))
	_, err := b.dynamic.Resource(scaledObjectGVR).Namespace(namespace).
		Patch(ctx, release, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("kube: resume keda %s/%s: %w", namespace, release, err)
	}
	return nil
}

func (b *KubeBackend) scale(ctx context.Context, namespace, release string, replicas int32) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if dep, err := b.client.AppsV1().Deployments(namespace).Get(ctx, release, metav1.GetOptions{}); err == nil {
			dep.Spec.Replicas = &replicas
			_, err = b.client.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
			return err
		} else if !errors.IsNotFound(err) {
			return err
		}
		if sts, err := b.client.AppsV1().StatefulSets(namespace).Get(ctx, release, metav1.GetOptions{}); err == nil {
			sts.Spec.Replicas = &replicas
			_, err = b.client.AppsV1().StatefulSets(namespace).Update(ctx, sts, metav1.UpdateOptions{})
			return err
		}
		return fmt.Errorf("kube: no Deployment/StatefulSet %q in %q", release, namespace)
	})
}

// Restart triggers a rollout by stamping a restart annotation on the pod template.
func (b *KubeBackend) Restart(ctx context.Context, namespace, release string) error {
	stamp := time.Now().Format(time.RFC3339)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if dep, err := b.client.AppsV1().Deployments(namespace).Get(ctx, release, metav1.GetOptions{}); err == nil {
			setRestartAnnotation(&dep.Spec.Template.ObjectMeta, stamp)
			_, err = b.client.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
			return err
		} else if !errors.IsNotFound(err) {
			return err
		}
		if sts, err := b.client.AppsV1().StatefulSets(namespace).Get(ctx, release, metav1.GetOptions{}); err == nil {
			setRestartAnnotation(&sts.Spec.Template.ObjectMeta, stamp)
			_, err = b.client.AppsV1().StatefulSets(namespace).Update(ctx, sts, metav1.UpdateOptions{})
			return err
		}
		return fmt.Errorf("kube: no Deployment/StatefulSet %q in %q", release, namespace)
	})
}

func setRestartAnnotation(meta *metav1.ObjectMeta, stamp string) {
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	meta.Annotations["kubectl.kubernetes.io/restartedAt"] = stamp
}

// Delete uninstalls the Helm release. It is idempotent: an already-absent
// release (`helm uninstall` reporting "release: not found") is treated as
// success so callers can safely retry / reconcile.
func (b *KubeBackend) Delete(ctx context.Context, namespace, release string) error {
	_, err := b.helm.Run(ctx, "uninstall", release, "-n", namespace)
	if err != nil && isHelmNotFound(err.Error()) {
		return nil
	}
	return err
}

// isHelmNotFound reports whether a helm error indicates the release is already
// gone. It matches ONLY helm's specific sentinel ("release: not found") so it
// never swallows unrelated failures like "chart ... not found" or
// "namespace ... not found".
func isHelmNotFound(s string) bool {
	return strings.Contains(strings.ToLower(s), "release: not found")
}

// ---------------------------------------------------------------------------
// Logs & Status
// ---------------------------------------------------------------------------

// Logs returns the most recent pod logs for the release. It selects the first
// pod matching the chart's instance label and tails the requested lines.
func (b *KubeBackend) Logs(ctx context.Context, namespace, release string, tailLines int) (string, error) {
	pod, err := b.firstPod(ctx, namespace, release)
	if err != nil {
		return "", err
	}
	opts := &corev1.PodLogOptions{}
	if tailLines > 0 {
		t := int64(tailLines)
		opts.TailLines = &t
	}
	raw, err := b.client.CoreV1().Pods(namespace).GetLogs(pod, opts).DoRaw(ctx)
	if err != nil {
		return "", fmt.Errorf("kube: get logs for %s/%s: %w", namespace, pod, err)
	}
	return string(raw), nil
}

func (b *KubeBackend) firstPod(ctx context.Context, namespace, release string) (string, error) {
	pods, err := b.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + release,
	})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("kube: no pods for release %q in %q", release, namespace)
	}
	names := make([]string, 0, len(pods.Items))
	for _, p := range pods.Items {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names[0], nil
}

// Status reports replica counts from the release's Deployment or StatefulSet.
func (b *KubeBackend) Status(ctx context.Context, namespace, release string) (Status, error) {
	if dep, err := b.client.AppsV1().Deployments(namespace).Get(ctx, release, metav1.GetOptions{}); err == nil {
		return statusFrom(dep.Status.Replicas, dep.Status.ReadyReplicas), nil
	} else if !errors.IsNotFound(err) {
		return Status{}, err
	}
	if sts, err := b.client.AppsV1().StatefulSets(namespace).Get(ctx, release, metav1.GetOptions{}); err == nil {
		return statusFrom(sts.Status.Replicas, sts.Status.ReadyReplicas), nil
	} else if !errors.IsNotFound(err) {
		return Status{}, err
	}
	return Status{Phase: "Unknown"}, fmt.Errorf("kube: no Deployment/StatefulSet %q in %q", release, namespace)
}

func statusFrom(replicas, ready int32) Status {
	phase := "Running"
	switch {
	case replicas == 0:
		phase = "Scaled to zero"
	case ready < replicas:
		phase = "Pending"
	}
	return Status{Phase: phase, Replicas: int(replicas), ReadyReplicas: int(ready)}
}

// ---------------------------------------------------------------------------
// Custom-domain TLS (cert-manager Certificate + shared-Gateway HTTPS listener)
// ---------------------------------------------------------------------------

// EnsureDomainCertificate creates (idempotently) a cert-manager Certificate for
// the exact custom hostname, signed by the configured ClusterIssuer, materialized
// into a TLS Secret in the shared Gateway namespace. The Certificate + Secret are
// named deterministically from the host (DomainCertSecret), so the call is safe
// to repeat when a domain re-verifies. With no ClusterIssuer or no dynamic client
// configured it no-ops (local/dev), so non-cluster flows keep working.
func (b *KubeBackend) EnsureDomainCertificate(ctx context.Context, host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return fmt.Errorf("kube: empty custom-domain host")
	}
	if b.dynamic == nil || b.cfg.ClusterIssuer == "" {
		return nil
	}
	ns := b.cfg.GatewayNamespace
	name := DomainCertName(host)
	secretName := DomainCertSecret(host)

	cert := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels":    map[string]any{"app.kubernetes.io/managed-by": "vortex"},
		},
		"spec": map[string]any{
			"secretName": secretName,
			"dnsNames":   []any{host},
			"issuerRef": map[string]any{
				"name": b.cfg.ClusterIssuer,
				"kind": "ClusterIssuer",
			},
		},
	}}
	cert.SetGroupVersionKind(certificateGVR.GroupVersion().WithKind("Certificate"))

	return upsert(ctx,
		func() error {
			_, err := b.dynamic.Resource(certificateGVR).Namespace(ns).Create(ctx, cert, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.dynamic.Resource(certificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if err := unstructured.SetNestedMap(cur.Object, cert.Object["spec"].(map[string]any), "spec"); err != nil {
				return err
			}
			_, err = b.dynamic.Resource(certificateGVR).Namespace(ns).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

// RemoveDomainCertificate deletes the cert-manager Certificate for the host (its
// TLS Secret is garbage-collected by cert-manager). A missing Certificate, no
// dynamic client, or no ClusterIssuer is not an error (idempotent cleanup).
func (b *KubeBackend) RemoveDomainCertificate(ctx context.Context, host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || b.dynamic == nil || b.cfg.ClusterIssuer == "" {
		return nil
	}
	ns := b.cfg.GatewayNamespace
	err := b.dynamic.Resource(certificateGVR).Namespace(ns).Delete(ctx, DomainCertName(host), metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("kube: delete domain certificate %s/%s: %w", ns, DomainCertName(host), err)
	}
	return nil
}

// EnsureGatewayListener adds (idempotently) a dedicated HTTPS listener to the
// SHARED Gateway terminating TLS for the custom host with the per-domain cert
// Secret. It MERGES into the existing spec.listeners under RetryOnConflict so it
// never clobbers other tenants' listeners (or the wildcard listener), and refuses
// to exceed the Gateway API 64-listener limit (see maxGatewayListeners) — at that
// scale a dedicated per-tenant Gateway is required. With no dynamic client it
// no-ops (local/dev).
func (b *KubeBackend) EnsureGatewayListener(ctx context.Context, host, certSecret string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return fmt.Errorf("kube: empty custom-domain host")
	}
	if b.dynamic == nil {
		return nil
	}
	if certSecret == "" {
		certSecret = DomainCertSecret(host)
	}
	ns := b.cfg.GatewayNamespace
	lname := DomainListenerName(host)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw, err := b.dynamic.Resource(gatewayGVR).Namespace(ns).Get(ctx, b.cfg.GatewayName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("kube: get gateway %s/%s: %w", ns, b.cfg.GatewayName, err)
		}
		listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		// Idempotent: if a listener for this host already exists, nothing to do.
		for _, l := range listeners {
			if m, ok := l.(map[string]any); ok {
				if n, _ := m["name"].(string); n == lname {
					return nil
				}
			}
		}
		if len(listeners) >= maxGatewayListeners {
			return fmt.Errorf("kube: shared gateway %s/%s at the %d-listener limit; cannot attach %s (move tenant to a dedicated Gateway)",
				ns, b.cfg.GatewayName, maxGatewayListeners, host)
		}
		listeners = append(listeners, domainListener(lname, host, certSecret))
		if err := unstructured.SetNestedSlice(gw.Object, listeners, "spec", "listeners"); err != nil {
			return err
		}
		_, err = b.dynamic.Resource(gatewayGVR).Namespace(ns).Update(ctx, gw, metav1.UpdateOptions{})
		return err
	})
}

// RemoveGatewayListener removes the per-domain HTTPS listener from the shared
// Gateway, preserving every other listener. A missing Gateway/listener (or no
// dynamic client) is not an error (idempotent cleanup).
func (b *KubeBackend) RemoveGatewayListener(ctx context.Context, host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || b.dynamic == nil {
		return nil
	}
	ns := b.cfg.GatewayNamespace
	lname := DomainListenerName(host)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw, err := b.dynamic.Resource(gatewayGVR).Namespace(ns).Get(ctx, b.cfg.GatewayName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("kube: get gateway %s/%s: %w", ns, b.cfg.GatewayName, err)
		}
		listeners, found, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		if !found {
			return nil
		}
		out := make([]any, 0, len(listeners))
		removed := false
		for _, l := range listeners {
			if m, ok := l.(map[string]any); ok {
				if n, _ := m["name"].(string); n == lname {
					removed = true
					continue
				}
			}
			out = append(out, l)
		}
		if !removed {
			return nil
		}
		if err := unstructured.SetNestedSlice(gw.Object, out, "spec", "listeners"); err != nil {
			return err
		}
		_, err = b.dynamic.Resource(gatewayGVR).Namespace(ns).Update(ctx, gw, metav1.UpdateOptions{})
		return err
	})
}

// ---------------------------------------------------------------------------
// Per-org wildcard TLS (cert-manager Certificate + shared-Gateway HTTPS listeners)
// ---------------------------------------------------------------------------

// EnsureOrgWildcard provisions (idempotently) the per-org wildcard certificate
// and matching shared-Gateway HTTPS listeners that terminate TLS and route the
// generated tenant host <app>.<project>.<org>.<baseDomain>.
//
// The bootstrap wildcard (*.<baseDomain>) covers only ONE label, so it matches
// neither the project-level host (<project>.<org>.<baseDomain>) nor the app host
// (<app>.<project>.<org>.<baseDomain>). This issues ONE Certificate whose dnsNames
// cover the org subtree — *.<org>.<baseDomain> for project-level hosts and
// *.<project>.<org>.<baseDomain> for each supplied project (a wildcard matches
// exactly one label, so the deeper app host needs a wildcard at the project
// label) — into a TLS Secret in the shared Gateway namespace, then adds an HTTPS
// listener per wildcard to the shared Gateway.
//
// With no ClusterIssuer or no dynamic client configured (local/dev) it no-ops so
// non-cluster flows keep working.
func (b *KubeBackend) EnsureOrgWildcard(ctx context.Context, orgSlug string, projectSlugs []string) error {
	orgSlug = sanitize(orgSlug)
	if orgSlug == "" {
		return fmt.Errorf("kube: empty org slug for wildcard provisioning")
	}
	if b.dynamic == nil || b.cfg.ClusterIssuer == "" {
		return nil
	}
	base := b.cfg.BaseDomain
	if base == "" {
		return fmt.Errorf("kube: empty base domain for org %q wildcard", orgSlug)
	}

	// dnsNames: the org wildcard (project-level hosts) plus a project wildcard per
	// known project (app-level hosts). De-duplicated, deterministically ordered.
	orgWild := orgWildcardHost(orgSlug, base)
	dnsNames := []string{orgWild}
	listeners := []listenerSpec{{name: orgListenerName(orgSlug), hostname: orgWild}}
	seen := map[string]bool{orgWild: true}
	projSlugs := make([]string, 0, len(projectSlugs))
	for _, p := range projectSlugs {
		ps := sanitize(p)
		if ps == "" {
			continue
		}
		projSlugs = append(projSlugs, ps)
	}
	sort.Strings(projSlugs)
	for _, ps := range projSlugs {
		pw := projectWildcardHost(ps, orgSlug, base)
		if seen[pw] {
			continue
		}
		seen[pw] = true
		dnsNames = append(dnsNames, pw)
		listeners = append(listeners, listenerSpec{name: projectListenerName(orgSlug, ps), hostname: pw})
	}

	secretName := OrgCertSecret(orgSlug)
	if err := b.ensureWildcardCertificate(ctx, orgCertName(orgSlug), secretName, dnsNames); err != nil {
		return err
	}
	return b.ensureGatewayListeners(ctx, listeners, secretName)
}

// ensureWildcardCertificate upserts a cert-manager Certificate named "name" in the
// shared Gateway namespace, signed by the configured ClusterIssuer, materialized
// into TLS Secret "secretName" and covering all dnsNames.
func (b *KubeBackend) ensureWildcardCertificate(ctx context.Context, name, secretName string, dnsNames []string) error {
	ns := b.cfg.GatewayNamespace
	names := make([]any, 0, len(dnsNames))
	for _, d := range dnsNames {
		names = append(names, d)
	}
	cert := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels":    map[string]any{"app.kubernetes.io/managed-by": "vortex"},
		},
		"spec": map[string]any{
			"secretName": secretName,
			"dnsNames":   names,
			"issuerRef": map[string]any{
				"name": b.cfg.ClusterIssuer,
				"kind": "ClusterIssuer",
			},
		},
	}}
	cert.SetGroupVersionKind(certificateGVR.GroupVersion().WithKind("Certificate"))

	return upsert(ctx,
		func() error {
			_, err := b.dynamic.Resource(certificateGVR).Namespace(ns).Create(ctx, cert, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.dynamic.Resource(certificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if err := unstructured.SetNestedMap(cur.Object, cert.Object["spec"].(map[string]any), "spec"); err != nil {
				return err
			}
			_, err = b.dynamic.Resource(certificateGVR).Namespace(ns).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

// listenerSpec names one HTTPS listener (name + hostname) to merge onto the shared
// Gateway, all referencing the same per-org cert Secret.
type listenerSpec struct {
	name     string
	hostname string
}

// ensureGatewayListeners merges the given HTTPS listeners onto the SHARED Gateway
// in a single RetryOnConflict-guarded read-modify-write, so several per-org
// wildcard listeners are added atomically without clobbering other tenants'
// listeners (or the bootstrap wildcard). Listeners already present (by name) are
// left untouched (idempotent). It refuses to exceed the Gateway API 64-listener
// limit rather than producing an invalid Gateway the controller would reject.
func (b *KubeBackend) ensureGatewayListeners(ctx context.Context, specs []listenerSpec, certSecret string) error {
	if len(specs) == 0 {
		return nil
	}
	ns := b.cfg.GatewayNamespace
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw, err := b.dynamic.Resource(gatewayGVR).Namespace(ns).Get(ctx, b.cfg.GatewayName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("kube: get gateway %s/%s: %w", ns, b.cfg.GatewayName, err)
		}
		listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		existing := make(map[string]bool, len(listeners))
		for _, l := range listeners {
			if m, ok := l.(map[string]any); ok {
				if n, _ := m["name"].(string); n != "" {
					existing[n] = true
				}
			}
		}
		added := false
		for _, spec := range specs {
			if existing[spec.name] {
				continue // idempotent: this listener is already present
			}
			if len(listeners) >= maxGatewayListeners {
				return fmt.Errorf("kube: shared gateway %s/%s at the %d-listener limit; cannot attach %s (move tenant to a dedicated Gateway)",
					ns, b.cfg.GatewayName, maxGatewayListeners, spec.hostname)
			}
			listeners = append(listeners, domainListener(spec.name, spec.hostname, certSecret))
			existing[spec.name] = true
			added = true
		}
		if !added {
			return nil
		}
		if err := unstructured.SetNestedSlice(gw.Object, listeners, "spec", "listeners"); err != nil {
			return err
		}
		_, err = b.dynamic.Resource(gatewayGVR).Namespace(ns).Update(ctx, gw, metav1.UpdateOptions{})
		return err
	})
}

// domainListener renders one Gateway API HTTPS listener (Terminate TLS with the
// per-domain cert Secret) for the exact host, accepting routes from all
// namespaces so the tenant's per-app HTTPRoute (in its own namespace) attaches.
func domainListener(name, host, certSecret string) map[string]any {
	return map[string]any{
		"name":     name,
		"protocol": "HTTPS",
		"port":     int64(443),
		"hostname": host,
		"tls": map[string]any{
			"mode": "Terminate",
			"certificateRefs": []any{
				map[string]any{"kind": "Secret", "name": certSecret},
			},
		},
		"allowedRoutes": map[string]any{
			"namespaces": map[string]any{"from": "All"},
		},
	}
}

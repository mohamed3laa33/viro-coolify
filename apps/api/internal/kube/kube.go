package kube

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

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
	// CPUOvercommitFactor / MemoryOvercommitFactor scale requested size down to the
	// scheduler requests (e.g. 0.2 and 0.35). Limits stay at the full requested size.
	CPUOvercommitFactor    float64
	MemoryOvercommitFactor float64
}

// KubeBackend is the real Backend: a typed clientset for namespace/quota/status/logs
// plus a HelmRunner for chart installs.
type KubeBackend struct {
	cfg    Config
	client kubernetes.Interface
	helm   HelmRunner
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
	return NewWithClient(cfg, cs, helm), nil
}

// NewWithClient builds a KubeBackend from an existing clientset (used by tests
// with client-go's fake clientset). Pass helm=nil to use the real `helm` binary.
func NewWithClient(cfg Config, client kubernetes.Interface, helm HelmRunner) *KubeBackend {
	if helm == nil {
		helm = NewExecHelmRunner("")
	}
	return &KubeBackend{cfg: cfg, client: client, helm: helm}
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
	if err := b.applyLimitRange(ctx, ns); err != nil {
		return "", err
	}
	return ns, nil
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
// resources still get sane values within the quota.
func (b *KubeBackend) applyLimitRange(ctx context.Context, ns string) error {
	lr := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "vortex-limits", Namespace: ns},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{{
				Type: corev1.LimitTypeContainer,
				Default: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
				DefaultRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
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

	args := []string{
		"upgrade", "--install", rel, b.cfg.ChartPath,
		"-n", ns, "--create-namespace",
		"-f", f.Name(),
	}
	if _, err := b.helm.Run(ctx, args...); err != nil {
		return "", "", err
	}
	return rel, h, nil
}

// buildValues maps a Workload to common-chart values.
func (b *KubeBackend) buildValues(w Workload, h string) map[string]any {
	stateful := strings.EqualFold(w.Kind, "database")
	port := defaultPort(w)

	img := w.Image
	if isWordPress(w) {
		// Force the pinned WordPress image per the volo recipe, ignoring any
		// catalog/build override.
		img = wordpressImage
	}
	repo, tag := splitImage(img)
	env := mergeEnv(w)

	resources := overcommitResources(w.CPU, w.MemoryMB, b.cfg.CPUOvercommitFactor, b.cfg.MemoryOvercommitFactor)

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

	// StatefulSet reads container ports from deployment.ports; Deployment reads
	// them from service.ports. Set both so either controller renders correctly.
	deployment["ports"] = []map[string]any{{
		"name":          "http",
		"containerPort": port,
		"protocol":      "TCP",
	}}

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

	// KEDA: CPU-utilization trigger with sane min/max. Databases keep a floor of 1
	// (no scale-to-zero); stateless workloads can be tuned to 0 by the caller later.
	minReplicas := 1
	maxReplicas := 5
	keda := map[string]any{
		"enabled":         true,
		"scaleTargetKind": scaleTargetKind(stateful),
		"minReplicaCount": minReplicas,
		"maxReplicaCount": maxReplicas,
		"pollingInterval": 30,
		"cooldownPeriod":  300,
		"triggers": []map[string]any{{
			"type":       "cpu",
			"metricType": "Utilization",
			"metadata":   map[string]any{"value": "70"},
		}},
	}

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

	return map[string]any{
		"deployment": deployment,
		"service":    service,
		"keda":       keda,
		"gateway":    gateway,
	}
}

func scaleTargetKind(stateful bool) string {
	if stateful {
		return "StatefulSet"
	}
	return "Deployment"
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// Stop scales the workload's controller to zero replicas via the clientset.
func (b *KubeBackend) Stop(ctx context.Context, namespace, release string) error {
	return b.scale(ctx, namespace, release, 0)
}

// Start scales the workload back up to one replica.
func (b *KubeBackend) Start(ctx context.Context, namespace, release string) error {
	return b.scale(ctx, namespace, release, 1)
}

func (b *KubeBackend) scale(ctx context.Context, namespace, release string, replicas int32) error {
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
}

// Restart triggers a rollout by stamping a restart annotation on the pod template.
func (b *KubeBackend) Restart(ctx context.Context, namespace, release string) error {
	stamp := time.Now().Format(time.RFC3339)
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
}

func setRestartAnnotation(meta *metav1.ObjectMeta, stamp string) {
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	meta.Annotations["kubectl.kubernetes.io/restartedAt"] = stamp
}

// Delete uninstalls the Helm release.
func (b *KubeBackend) Delete(ctx context.Context, namespace, release string) error {
	_, err := b.helm.Run(ctx, "uninstall", release, "-n", namespace)
	return err
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

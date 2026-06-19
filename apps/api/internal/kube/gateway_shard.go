package kube

import (
	"context"
	stderrors "errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/retry"
)

// Gateway listener sharding
// ---------------------------------------------------------------------------
//
// A single Gateway has a hard ceiling of maxGatewayListeners (64) listeners.
// Each VERIFIED custom domain consumes one dedicated HTTPS listener (the
// platform's wildcard host needs none — it rides the primary Gateway's wildcard
// listener). Past ~64 custom domains the platform used to error and ask the
// operator to move the tenant to a dedicated Gateway by hand.
//
// This file replaces that manual escape hatch with AUTOMATIC sharding: custom
// domain listeners are allocated across a POOL of Gateways. Shard 0 is the
// primary Gateway (cfg.GatewayName) that also carries the wildcard + http
// listeners; shards 1..N are additional Gateways (named "<GatewayName>-shard-<n>")
// in the same namespace and GatewayClass, created on demand when every existing
// shard is full. A custom domain's HTTPRoute parentRef is then pointed at the
// shard that actually holds its listener (see shardForHost / buildValues), so
// traffic flows regardless of which shard a domain landed on.
//
// The shard pool is DERIVED FROM CLUSTER STATE (the live Gateways), not from any
// in-process bookkeeping — the control plane is stateless across restarts and may
// run multiple replicas, so placement must always be reconstructable by listing
// Gateways. allocateListenerShard / shardForHost both scan the pool.
//
// COST PHILOSOPHY / TRADEOFFS (see deploy/charts/vortex-bootstrap and
// deploy/KUBERNETES.md): the single-LoadBalancer-for-everything model is the
// cheapest, and we keep it for the common case (one Gateway, <=~62 custom
// domains). Sharding only kicks in past that ceiling. Whether the extra shards
// SHARE the primary LoadBalancer or get their own depends on the Gateway
// controller:
//   - Envoy Gateway can MERGE multiple Gateways of the same GatewayClass onto one
//     Envoy fleet / one Service (one LB) when the EnvoyProxy infrastructure is
//     configured with mergeGateways: true. With that enabled, shards cost no extra
//     LBs — the single-LB philosophy is preserved even past 64 listeners.
//   - Without merging, each shard Gateway provisions its own LoadBalancer Service.
//     That is an HONEST extra cost (a handful of LBs only at very large custom-
//     domain counts), surfaced here and in the chart rather than hidden.
// GatewayShardLBSharing (Config) records the operator's intent for documentation /
// future address pinning; it does not by itself force the controller to merge
// (that is the controller's EnvoyProxy/infra config, set in the bootstrap chart).

// baseListenerReserve is the number of NON-custom-domain listeners the PRIMARY
// shard (shard 0) always carries: the wildcard "https" listener and the plain
// "http" listener from the bootstrap Gateway. The primary shard's custom-domain
// budget is therefore (perShardListenerBudget - baseListenerReserve); secondary
// shards reserve nothing (they hold only custom-domain listeners).
const baseListenerReserve = 2

// gatewayShardName returns the Gateway name for shard idx within the pool rooted
// at base. Shard 0 is the primary Gateway itself (base); shard n>0 is
// "<base>-shard-<n>". Names stay DNS-1123 (base is already a valid label and the
// suffix is ASCII digits), so they are valid Gateway object names.
func gatewayShardName(base string, idx int) string {
	if idx <= 0 {
		return base
	}
	return base + "-shard-" + strconv.Itoa(idx)
}

// gatewayShardIndex parses the shard index out of a Gateway name produced by
// gatewayShardName. The primary (base) name is shard 0; a "<base>-shard-<n>" name
// is shard n. A name that does not belong to the pool returns ok=false.
func gatewayShardIndex(base, name string) (int, bool) {
	if name == base {
		return 0, true
	}
	suffix := strings.TrimPrefix(name, base+"-shard-")
	if suffix == name {
		return 0, false
	}
	idx, err := strconv.Atoi(suffix)
	if err != nil || idx <= 0 {
		return 0, false
	}
	return idx, true
}

// perShardListenerBudget is the maximum number of listeners the backend will
// place on ONE Gateway shard before overflowing to the next. It honours an
// admin/DB-configurable Config override (GatewayShardMaxListeners) but never
// exceeds the Gateway API hard ceiling (maxGatewayListeners); a zero/oversized
// override falls back to maxGatewayListeners.
func (b *KubeBackend) perShardListenerBudget() int {
	budget := b.cfg.GatewayShardMaxListeners
	if budget <= 0 || budget > maxGatewayListeners {
		budget = maxGatewayListeners
	}
	return budget
}

// shardCustomDomainCapacity is the number of additional custom-domain listeners a
// shard can still hold (>=0) before the backend overflows to the next shard. It is
// the MINIMUM of two independent limits:
//
//   - The custom-domain budget: at most (perShardListenerBudget - reserve) custom
//     listeners. customListeners is the shard's count of CUSTOM-DOMAIN listeners
//     only (see customListenerCount). On the PRIMARY (idx 0) we lower the budget by
//     baseListenerReserve to keep room for the shard's base wildcard "https" + plain
//     "http" listeners; because customListeners already EXCLUDES those base
//     listeners, the reserve is applied exactly once (counting both the reserve AND
//     the live base listeners would double-count them and under-report capacity).
//     Secondary shards reserve nothing (they hold only custom-domain listeners).
//   - The Gateway API hard ceiling: a Gateway may carry at most maxGatewayListeners
//     listeners of ANY kind. totalListeners is the shard's FULL listener count
//     (base + custom + any operator-managed listeners), so a Gateway already at the
//     ceiling (regardless of listener type) reports zero capacity and overflows to a
//     shard instead of being pushed over the limit.
func (b *KubeBackend) shardCustomDomainCapacity(idx, customListeners, totalListeners int) int {
	budget := b.perShardListenerBudget()
	if idx == 0 {
		budget -= baseListenerReserve // reserve slots for the primary's base listeners
	}
	free := budget - customListeners
	if ceilingFree := maxGatewayListeners - totalListeners; ceilingFree < free {
		free = ceilingFree
	}
	if free < 0 {
		free = 0
	}
	return free
}

// customListenerCount returns how many of a Gateway's listeners are custom-domain
// listeners (those named by DomainListenerName, i.e. carrying the domainListenerPrefix).
// Base listeners (the wildcard "https"/"http") are excluded so capacity math counts
// only the listeners that sharding actually allocates.
func customListenerCount(listeners []any) int {
	n := 0
	for _, l := range listeners {
		if m, ok := l.(map[string]any); ok {
			if name, _ := m["name"].(string); strings.HasPrefix(name, domainListenerPrefix) {
				n++
			}
		}
	}
	return n
}

// listGatewayShards returns the existing Gateways in the pool rooted at
// cfg.GatewayName/cfg.GatewayNamespace, sorted by ascending shard index. The
// primary Gateway (shard 0) is included when present. It tolerates a List that
// returns Gateways outside the pool (they are filtered out by gatewayShardIndex).
func (b *KubeBackend) listGatewayShards(ctx context.Context) ([]gatewayShard, error) {
	ns := b.cfg.GatewayNamespace
	list, err := b.dynamic.Resource(gatewayGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube: list gateways in %s: %w", ns, err)
	}
	shards := make([]gatewayShard, 0, len(list.Items))
	for i := range list.Items {
		gw := list.Items[i]
		idx, ok := gatewayShardIndex(b.cfg.GatewayName, gw.GetName())
		if !ok {
			continue
		}
		listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		shards = append(shards, gatewayShard{
			index:  idx,
			name:   gw.GetName(),
			custom: customListenerCount(listeners),
			total:  len(listeners),
		})
	}
	sort.Slice(shards, func(i, j int) bool { return shards[i].index < shards[j].index })
	return shards, nil
}

// gatewayShard is a lightweight view of one Gateway in the pool used for
// allocation decisions (we do not carry the full object — re-Get under
// RetryOnConflict when mutating).
type gatewayShard struct {
	index int
	name  string
	// custom is the count of CUSTOM-DOMAIN listeners on the shard (base
	// wildcard/http listeners excluded); total is the shard's FULL listener count.
	// They feed shardCustomDomainCapacity's customListeners/totalListeners args.
	custom int
	total  int
}

// shardForHost scans the pool and returns the NAME of the Gateway shard that
// already holds a listener for host, or ("", false) if no shard does. It is used
// both for idempotent placement and to point a custom domain's HTTPRoute
// parentRef at the right shard. With no dynamic client (local/dev) it returns
// ("", false) so callers fall back to the primary Gateway.
func (b *KubeBackend) shardForHost(ctx context.Context, host string) (string, bool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || b.dynamic == nil {
		return "", false, nil
	}
	ns := b.cfg.GatewayNamespace
	lname := DomainListenerName(host)
	list, err := b.dynamic.Resource(gatewayGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", false, fmt.Errorf("kube: list gateways in %s: %w", ns, err)
	}
	for i := range list.Items {
		gw := list.Items[i]
		if _, ok := gatewayShardIndex(b.cfg.GatewayName, gw.GetName()); !ok {
			continue
		}
		listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		for _, l := range listeners {
			if m, ok := l.(map[string]any); ok {
				if n, _ := m["name"].(string); n == lname {
					return gw.GetName(), true, nil
				}
			}
		}
	}
	return "", false, nil
}

// allocateListenerShard picks the Gateway shard that should hold host's listener.
// Order of preference:
//  1. The shard that ALREADY holds host's listener (idempotency).
//  2. The lowest-index existing shard with remaining custom-domain capacity.
//  3. A NEW shard (next index after the highest existing one), created on demand
//     via ensureShardGateway.
//
// It returns the chosen shard's Gateway name. The caller (EnsureGatewayListener)
// then merges the listener into that named Gateway under RetryOnConflict.
func (b *KubeBackend) allocateListenerShard(ctx context.Context, host string) (string, error) {
	// 1) Already placed? (idempotency across re-verify / re-Apply / replicas.)
	if gwName, found, err := b.shardForHost(ctx, host); err != nil {
		return "", err
	} else if found {
		return gwName, nil
	}

	shards, err := b.listGatewayShards(ctx)
	if err != nil {
		return "", err
	}
	// The primary Gateway must exist (the bootstrap chart creates it). If the pool
	// is empty, fall back to the primary name and let the merge step surface a
	// clear "get gateway" error rather than silently creating a shard-0 clone that
	// would lack the wildcard/http listeners.
	if len(shards) == 0 {
		return b.cfg.GatewayName, nil
	}

	// 2) Lowest-index existing shard with capacity.
	for _, s := range shards {
		if b.shardCustomDomainCapacity(s.index, s.custom, s.total) > 0 {
			return s.name, nil
		}
	}

	// 3) All shards full — create the next one.
	maxIdx := shards[len(shards)-1].index
	nextIdx := maxIdx + 1
	name := gatewayShardName(b.cfg.GatewayName, nextIdx)
	if err := b.ensureShardGateway(ctx, name); err != nil {
		return "", err
	}
	return name, nil
}

// ensureShardGateway creates (idempotently) a secondary Gateway shard. It clones
// the PRIMARY Gateway's gatewayClassName and (when present) its
// spec.infrastructure block so the controller schedules the shard the same way —
// crucially, when the operator has configured Gateway MERGING (Envoy Gateway:
// mergeGateways via a shared EnvoyProxy referenced by infrastructure.parametersRef),
// the shard joins the SAME data plane / LoadBalancer as the primary, preserving
// the single-LB cost model. A secondary shard carries NO wildcard/http listeners
// of its own (those live only on the primary); it starts with an empty listener
// set that EnsureGatewayListener then fills. An AlreadyExists is not an error
// (concurrent control-plane replicas may race to create the same shard).
func (b *KubeBackend) ensureShardGateway(ctx context.Context, name string) error {
	ns := b.cfg.GatewayNamespace
	primary, err := b.dynamic.Resource(gatewayGVR).Namespace(ns).Get(ctx, b.cfg.GatewayName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("kube: get primary gateway %s/%s for shard clone: %w", ns, b.cfg.GatewayName, err)
	}
	className, _, _ := unstructured.NestedString(primary.Object, "spec", "gatewayClassName")

	shardSpec := map[string]any{
		"gatewayClassName": className,
		// Secondary shards hold ONLY custom-domain listeners; the wildcard/http
		// listeners stay on the primary. Start empty so EnsureGatewayListener's
		// merge appends the first custom-domain listener. (A Gateway with zero
		// listeners is valid; the controller simply has nothing to serve until the
		// first listener is merged in moments later.)
		"listeners": []any{},
	}
	// When the operator wants shards to SHARE the primary LoadBalancer, carry the
	// primary's infrastructure block (labels/annotations) onto the shard so a
	// shared-LB / merged-data-plane setup (Envoy Gateway mergeGateways) applies to
	// it too. Without LB sharing we leave it off so each shard gets its own Service.
	if b.cfg.GatewayShardLBSharing {
		if infra, found, _ := unstructured.NestedMap(primary.Object, "spec", "infrastructure"); found {
			shardSpec["infrastructure"] = infra
		}
	}

	shard := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "vortex",
				gatewayShardLabel:              "true",
			},
		},
		"spec": shardSpec,
	}}
	shard.SetGroupVersionKind(gatewayGVR.GroupVersion().WithKind("Gateway"))

	_, err = b.dynamic.Resource(gatewayGVR).Namespace(ns).Create(ctx, shard, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("kube: create gateway shard %s/%s: %w", ns, name, err)
	}
	return nil
}

// gatewayShardLabel marks Gateways the backend created as overflow shards (vs the
// bootstrap-managed primary), so an operator can tell them apart at a glance.
const gatewayShardLabel = "vortex.v60ai.com/gateway-shard"

// mergeListenerIntoGateway adds (idempotently) the custom-domain HTTPS listener
// for host into the named Gateway under RetryOnConflict, never clobbering other
// listeners. It enforces the per-shard listener budget as a final guard so a
// shard is never pushed past the Gateway API ceiling even under a concurrent
// race; on overflow it returns errShardFull so the caller can re-allocate.
func (b *KubeBackend) mergeListenerIntoGateway(ctx context.Context, gwName, host, certSecret string) error {
	ns := b.cfg.GatewayNamespace
	lname := DomainListenerName(host)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw, err := b.dynamic.Resource(gatewayGVR).Namespace(ns).Get(ctx, gwName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("kube: get gateway %s/%s: %w", ns, gwName, err)
		}
		listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		for _, l := range listeners {
			if m, ok := l.(map[string]any); ok {
				if n, _ := m["name"].(string); n == lname {
					return nil // already present: idempotent
				}
			}
		}
		idx, _ := gatewayShardIndex(b.cfg.GatewayName, gwName)
		if b.shardCustomDomainCapacity(idx, customListenerCount(listeners), len(listeners)) <= 0 {
			return errShardFull
		}
		listeners = append(listeners, domainListener(lname, host, certSecret))
		if err := unstructured.SetNestedSlice(gw.Object, listeners, "spec", "listeners"); err != nil {
			return err
		}
		// external-dns: when the custom domain falls under a managed zone, accumulate
		// it on the Gateway's external-dns hostname annotation so the controller
		// publishes a record for it pointing at the Gateway LB. No-op (and no spurious
		// re-write) when external-dns is off or the zone is unmanaged.
		b.addHostToGatewayDNS(gw, host)
		_, err = b.dynamic.Resource(gatewayGVR).Namespace(ns).Update(ctx, gw, metav1.UpdateOptions{})
		return err
	})
}

// errShardFull signals that a chosen shard filled up between allocation and merge
// (a concurrent attach). EnsureGatewayListener retries allocation a bounded number
// of times when it sees this.
var errShardFull = stderrors.New("kube: gateway shard full")

// isShardFull reports whether err is (or wraps) errShardFull. It lives here so
// the comparison uses the standard library errors.Is (the kube package's other
// files alias k8s.io/apimachinery/pkg/api/errors as `errors`, which has no Is).
func isShardFull(err error) bool { return stderrors.Is(err, errShardFull) }

// addShardParentRefs appends, to the HTTPRoute's gateway.parentRefs in values,
// one parentRef for every NON-PRIMARY Gateway shard that holds a listener for one
// of the workload's custom domains. The HTTPRoute already references the primary
// Gateway (buildValues), which serves the generated wildcard host and any custom
// domains whose listeners landed on shard 0; this adds the overflow shards so a
// custom domain placed on shard N still routes. It is a no-op when there are no
// custom domains, no dynamic client (local/dev), or every listener is on the
// primary — preserving the simple single-Gateway path for the common case.
//
// A per-host lookup error is swallowed (best-effort): a missing shard parentRef
// only affects that one custom domain's routing, and the next reconcile/Apply
// re-derives it; failing the whole deploy over a transient List error would be
// worse. The primary parentRef (the platform host) is always present regardless.
func (b *KubeBackend) addShardParentRefs(ctx context.Context, values map[string]any, domains []string) {
	if b.dynamic == nil || len(domains) == 0 {
		return
	}
	gw, ok := values["gateway"].(map[string]any)
	if !ok {
		return
	}
	// Custom domains are rendered through sanitizeDomains in buildValues; mirror
	// that here so the listener-name lookup matches what was actually attached.
	hosts := sanitizeDomains(domains)
	if len(hosts) == 0 {
		return
	}

	seen := map[string]bool{b.cfg.GatewayName: true} // primary already referenced
	extra := make([]map[string]any, 0, 2)
	for _, host := range hosts {
		gwName, found, err := b.shardForHost(ctx, host)
		if err != nil || !found {
			continue
		}
		if seen[gwName] {
			continue
		}
		seen[gwName] = true
		extra = append(extra, map[string]any{
			"name":      gwName,
			"namespace": b.cfg.GatewayNamespace,
		})
	}
	if len(extra) == 0 {
		return
	}

	// buildValues sets parentRefs as []map[string]any; append the shard refs in the
	// same shape (yaml.Marshal flattens both to a list of maps regardless).
	switch refs := gw["parentRefs"].(type) {
	case []map[string]any:
		gw["parentRefs"] = append(refs, extra...)
	case []any:
		for _, e := range extra {
			refs = append(refs, e)
		}
		gw["parentRefs"] = refs
	default:
		// No recognizable parentRefs slice (unexpected): leave the primary ref alone.
	}
}

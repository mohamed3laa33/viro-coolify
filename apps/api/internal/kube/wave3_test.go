package kube

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// Wave 3: automatic Gateway listener SHARDING. Custom-domain HTTPS listeners are
// allocated across a POOL of Gateways so the platform scales past one Gateway's
// 64-listener ceiling without any manual "move the tenant by hand" step.

func TestGatewayShardNameAndIndex(t *testing.T) {
	cases := []struct {
		idx  int
		name string
	}{
		{0, "vortex"},
		{1, "vortex-shard-1"},
		{7, "vortex-shard-7"},
	}
	for _, c := range cases {
		if got := gatewayShardName("vortex", c.idx); got != c.name {
			t.Errorf("gatewayShardName(vortex, %d) = %q, want %q", c.idx, got, c.name)
		}
		idx, ok := gatewayShardIndex("vortex", c.name)
		if !ok || idx != c.idx {
			t.Errorf("gatewayShardIndex(vortex, %q) = (%d, %v), want (%d, true)", c.name, idx, ok, c.idx)
		}
	}
	// Names outside the pool are rejected.
	for _, bad := range []string{"other", "vortex-shard-", "vortex-shard-x", "vortex-shard-0", "vortex-shard--1"} {
		if _, ok := gatewayShardIndex("vortex", bad); ok {
			t.Errorf("gatewayShardIndex(vortex, %q) reported in-pool, want rejected", bad)
		}
	}
}

func TestPerShardBudgetHonorsConfigButCapsAtHardCeiling(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()

	// Zero override -> hard ceiling.
	b := NewWithClient(testConfig(), cs, &mockHelm{})
	if got := b.perShardListenerBudget(); got != maxGatewayListeners {
		t.Errorf("default per-shard budget = %d, want %d", got, maxGatewayListeners)
	}
	// In-range override is honored.
	cfg := testConfig()
	cfg.GatewayShardMaxListeners = 10
	b = NewWithClient(cfg, cs, &mockHelm{})
	if got := b.perShardListenerBudget(); got != 10 {
		t.Errorf("per-shard budget with override 10 = %d, want 10", got)
	}
	// An over-ceiling override is clamped to the hard ceiling (never render an
	// invalid 65+ listener Gateway).
	cfg.GatewayShardMaxListeners = maxGatewayListeners + 50
	b = NewWithClient(cfg, cs, &mockHelm{})
	if got := b.perShardListenerBudget(); got != maxGatewayListeners {
		t.Errorf("per-shard budget with over-ceiling override = %d, want %d", got, maxGatewayListeners)
	}
}

func TestShardCustomDomainCapacityReservesPrimaryBaseListeners(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	cfg := testConfig()
	cfg.GatewayShardMaxListeners = 10
	b := NewWithClient(cfg, cs, &mockHelm{})

	// Primary (idx 0) reserves baseListenerReserve for wildcard/http. The budget
	// (10) is well under the hard ceiling, so the custom-domain limit governs.
	if got := b.shardCustomDomainCapacity(0, 2, 2); got != 10-baseListenerReserve-2 {
		t.Errorf("primary capacity with 2 listeners = %d, want %d", got, 10-baseListenerReserve-2)
	}
	// A secondary shard reserves nothing.
	if got := b.shardCustomDomainCapacity(1, 3, 3); got != 10-3 {
		t.Errorf("secondary capacity with 3 listeners = %d, want %d", got, 10-3)
	}
	// Never negative.
	if got := b.shardCustomDomainCapacity(1, 100, 100); got != 0 {
		t.Errorf("over-full capacity = %d, want 0", got)
	}
	// The Gateway API hard ceiling caps capacity even when the custom-domain budget
	// would allow more: a shard already at maxGatewayListeners total listeners has
	// zero room regardless of how few of them are custom-domain listeners.
	full := NewWithClient(testConfig(), cs, &mockHelm{}) // default budget = hard ceiling
	if got := full.shardCustomDomainCapacity(1, 0, maxGatewayListeners); got != 0 {
		t.Errorf("capacity at the hard ceiling = %d, want 0", got)
	}
}

// TestEnsureGatewayListenerOverflowsAcrossManyShards drives MANY custom domains
// through a small per-shard budget and asserts they spread across several auto-
// created shards (no error, no manual step), and that each shard respects its
// budget.
func TestEnsureGatewayListenerOverflowsAcrossManyShards(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newDomainDyn(t, sharedGateway("vortex-system", "vortex"))
	cfg := domainTestConfig()
	cfg.GatewayShardMaxListeners = 5 // tiny budget so overflow happens quickly
	b := NewWithClients(cfg, cs, dc, &mockHelm{})

	const n = 14
	for i := 0; i < n; i++ {
		host := fmt.Sprintf("d%02d.tenant.io", i)
		if err := b.EnsureGatewayListener(context.Background(), host, ""); err != nil {
			t.Fatalf("EnsureGatewayListener(%s): %v", host, err)
		}
	}

	shards, err := b.listGatewayShards(context.Background())
	if err != nil {
		t.Fatalf("listGatewayShards: %v", err)
	}
	// Primary holds wildcard(1) + up to (5-2)=3 custom listeners = 4 listeners max.
	// Each secondary holds up to 5. 14 custom domains => primary 3 + shards of 5.
	totalCustom := 0
	for _, s := range shards {
		gw, _ := dc.Resource(gatewayGVR).Namespace("vortex-system").Get(context.Background(), s.name, metav1.GetOptions{})
		listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		// Count only custom-domain listeners (name prefixed "d-").
		custom := 0
		for _, l := range listeners {
			m := l.(map[string]any)
			if name, _ := m["name"].(string); len(name) >= 2 && name[:2] == "d-" {
				custom++
			}
		}
		totalCustom += custom
		if s.index == 0 {
			if custom > cfg.GatewayShardMaxListeners-baseListenerReserve {
				t.Errorf("primary holds %d custom listeners, over its %d budget", custom, cfg.GatewayShardMaxListeners-baseListenerReserve)
			}
		} else if custom > cfg.GatewayShardMaxListeners {
			t.Errorf("shard %s holds %d custom listeners, over its %d budget", s.name, custom, cfg.GatewayShardMaxListeners)
		}
	}
	if totalCustom != n {
		t.Errorf("total custom listeners across pool = %d, want %d", totalCustom, n)
	}
	if len(shards) < 3 {
		t.Errorf("expected the pool to fan out to >=3 gateways for %d domains at budget %d, got %d", n, cfg.GatewayShardMaxListeners, len(shards))
	}
}

// TestEnsureGatewayListenerIdempotentAcrossShards re-attaching the same host must
// not duplicate the listener nor move it to a new shard.
func TestEnsureGatewayListenerIdempotentAcrossShards(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newDomainDyn(t, sharedGateway("vortex-system", "vortex"))
	cfg := domainTestConfig()
	cfg.GatewayShardMaxListeners = 4
	b := NewWithClients(cfg, cs, dc, &mockHelm{})

	// Fill the primary's custom budget (4-2=2) then push one onto shard-1.
	hosts := []string{"a.io", "b.io", "c.io"}
	for _, h := range hosts {
		if err := b.EnsureGatewayListener(context.Background(), h, ""); err != nil {
			t.Fatalf("attach %s: %v", h, err)
		}
	}
	gwBefore, _, _ := b.shardForHost(context.Background(), "c.io")

	// Re-attach every host: no duplicates, same placement.
	for _, h := range hosts {
		if err := b.EnsureGatewayListener(context.Background(), h, ""); err != nil {
			t.Fatalf("re-attach %s: %v", h, err)
		}
	}
	gwAfter, found, _ := b.shardForHost(context.Background(), "c.io")
	if !found || gwAfter != gwBefore {
		t.Errorf("re-attach moved c.io: before %q after %q (found=%v)", gwBefore, gwAfter, found)
	}
	if gwBefore != "vortex-shard-1" {
		t.Errorf("c.io should have overflowed to vortex-shard-1, got %q", gwBefore)
	}
}

// TestAddShardParentRefsPointsRouteAtOverflowShard verifies that a workload with a
// custom domain whose listener landed on an overflow shard gets that shard added
// to its HTTPRoute parentRefs (so traffic to the custom domain actually routes),
// while the primary parentRef is preserved.
func TestAddShardParentRefsPointsRouteAtOverflowShard(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	dc := newDomainDyn(t, sharedGateway("vortex-system", "vortex"))
	cfg := domainTestConfig()
	cfg.GatewayShardMaxListeners = 3 // primary custom budget = 1
	b := NewWithClients(cfg, cs, dc, &mockHelm{})

	// First custom domain fills the primary's single custom slot; the second
	// overflows to shard-1.
	onPrimary := "first.shop.io"
	onShard := "second.shop.io"
	if err := b.EnsureGatewayListener(context.Background(), onPrimary, ""); err != nil {
		t.Fatalf("attach %s: %v", onPrimary, err)
	}
	if err := b.EnsureGatewayListener(context.Background(), onShard, ""); err != nil {
		t.Fatalf("attach %s: %v", onShard, err)
	}

	values := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256,
		Domains: []string{onShard},
	}, "api.web.acme.vortex.v60ai.com")
	b.addShardParentRefs(context.Background(), values, []string{onShard})

	gw := values["gateway"].(map[string]any)
	refs := gw["parentRefs"].([]map[string]any)
	names := map[string]bool{}
	for _, r := range refs {
		names[r["name"].(string)] = true
	}
	if !names["vortex"] {
		t.Error("primary 'vortex' parentRef missing")
	}
	if !names["vortex-shard-1"] {
		t.Errorf("expected overflow shard 'vortex-shard-1' in parentRefs, got %v", names)
	}

	// A workload whose custom domain is on the PRIMARY needs no extra parentRef.
	values2 := b.buildValues(Workload{
		OrgSlug: "acme", ProjectSlug: "web", Name: "api2", Kind: "app",
		Image: "nginx:1", CPU: 1, MemoryMB: 256,
		Domains: []string{onPrimary},
	}, "api2.web.acme.vortex.v60ai.com")
	b.addShardParentRefs(context.Background(), values2, []string{onPrimary})
	refs2 := values2["gateway"].(map[string]any)["parentRefs"].([]map[string]any)
	if len(refs2) != 1 || refs2[0]["name"].(string) != "vortex" {
		t.Errorf("primary-only domain should keep a single primary parentRef, got %v", refs2)
	}
}

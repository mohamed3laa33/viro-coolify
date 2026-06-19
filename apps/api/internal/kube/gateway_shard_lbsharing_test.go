package kube

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// sharedGatewayWithInfra builds a primary Gateway carrying a spec.infrastructure
// block (labels/annotations the controller uses for LB merging) so shard-clone
// tests can assert whether the infrastructure is propagated onto overflow shards.
func sharedGatewayWithInfra(ns, name string) *unstructured.Unstructured {
	gw := sharedGateway(ns, name)
	_ = unstructured.SetNestedMap(gw.Object, map[string]any{
		"annotations": map[string]any{"vortex.v60ai.com/lb": "shared"},
	}, "spec", "infrastructure")
	return gw
}

// TestShardCloneSharesInfrastructureWhenLBSharingOn asserts that, with
// GatewayShardLBSharing true, a newly-created overflow shard CLONES the primary's
// spec.infrastructure block (so a merged-LB / shared-data-plane setup applies to it
// too, preserving the single-LB cost model past the listener ceiling).
func TestShardCloneSharesInfrastructureWhenLBSharingOn(t *testing.T) {
	dc := newDomainDyn(t, sharedGatewayWithInfra("vortex-system", "vortex"))
	cfg := domainTestConfig()
	cfg.GatewayShardMaxListeners = 3 // primary custom budget = 1; 2nd host overflows
	cfg.GatewayShardLBSharing = true
	b := NewWithClients(cfg, nil, dc, &mockHelm{})

	for i := 0; i < 2; i++ {
		host := fmt.Sprintf("d%d.tenant.io", i)
		if err := b.EnsureGatewayListener(context.Background(), host, ""); err != nil {
			t.Fatalf("attach %s: %v", host, err)
		}
	}

	shard, err := dc.Resource(gatewayGVR).Namespace("vortex-system").
		Get(context.Background(), "vortex-shard-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected overflow shard vortex-shard-1: %v", err)
	}
	infra, found, _ := unstructured.NestedMap(shard.Object, "spec", "infrastructure")
	if !found {
		t.Fatalf("LBSharing on: shard should clone the primary's infrastructure block, got none")
	}
	ann, _, _ := unstructured.NestedString(infra, "annotations", "vortex.v60ai.com/lb")
	if ann != "shared" {
		t.Errorf("cloned infrastructure annotation = %q, want shared", ann)
	}
}

// TestShardCloneOmitsInfrastructureWhenLBSharingOff asserts that, with
// GatewayShardLBSharing false (the default), an overflow shard does NOT carry the
// primary's infrastructure block, so each shard gets its own LoadBalancer Service
// (the honest extra cost surfaced rather than hidden).
func TestShardCloneOmitsInfrastructureWhenLBSharingOff(t *testing.T) {
	dc := newDomainDyn(t, sharedGatewayWithInfra("vortex-system", "vortex"))
	cfg := domainTestConfig()
	cfg.GatewayShardMaxListeners = 3
	cfg.GatewayShardLBSharing = false
	b := NewWithClients(cfg, nil, dc, &mockHelm{})

	for i := 0; i < 2; i++ {
		host := fmt.Sprintf("d%d.tenant.io", i)
		if err := b.EnsureGatewayListener(context.Background(), host, ""); err != nil {
			t.Fatalf("attach %s: %v", host, err)
		}
	}

	shard, err := dc.Resource(gatewayGVR).Namespace("vortex-system").
		Get(context.Background(), "vortex-shard-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected overflow shard vortex-shard-1: %v", err)
	}
	if _, found, _ := unstructured.NestedMap(shard.Object, "spec", "infrastructure"); found {
		t.Errorf("LBSharing off: shard must not carry the primary infrastructure block")
	}
}

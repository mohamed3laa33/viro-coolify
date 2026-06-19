package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// dns1123 keeps names valid for namespaces / Helm releases / hostnames:
// lowercase alphanumerics and '-', collapsed, trimmed.
var nonDNS = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonDNS.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return s
}

// namespaceName is the org-project namespace: vortex-<org>-<project>.
func namespaceName(orgSlug, projSlug string) string {
	return "vortex-" + sanitize(orgSlug) + "-" + sanitize(projSlug)
}

// releaseName is the Helm release name for a workload within its namespace.
// It is just the (sanitized) workload name; uniqueness is per-namespace.
func releaseName(name string) string { return sanitize(name) }

// host builds the public hostname: <name>.<proj>.<org>.<BaseDomain>.
func host(name, projSlug, orgSlug, baseDomain string) string {
	return fmt.Sprintf("%s.%s.%s.%s",
		sanitize(name), sanitize(projSlug), sanitize(orgSlug), baseDomain)
}

// orgWildcardHost is the per-org wildcard suffix that covers the PROJECT-level
// host <proj>.<org>.<BaseDomain> (one label below <org>.<BaseDomain>):
// "*.<org>.<BaseDomain>".
func orgWildcardHost(orgSlug, baseDomain string) string {
	return fmt.Sprintf("*.%s.%s", sanitize(orgSlug), baseDomain)
}

// projectWildcardHost is the per-project wildcard that covers the APP-level
// tenant host <app>.<proj>.<org>.<BaseDomain>: "*.<proj>.<org>.<BaseDomain>".
// A DNS/TLS wildcard matches exactly ONE label, so the 3-label tenant app host
// is only covered by a wildcard at the project level — not by the org wildcard
// alone (which stops at the project label). The per-org Certificate therefore
// lists a project wildcard SAN per known project (the default project always
// exists at org creation).
func projectWildcardHost(projSlug, orgSlug, baseDomain string) string {
	return fmt.Sprintf("*.%s.%s.%s", sanitize(projSlug), sanitize(orgSlug), baseDomain)
}

// orgCertName is the cert-manager Certificate object name for an org's wildcard.
func orgCertName(orgSlug string) string { return "vortex-org-" + sanitize(orgSlug) }

// OrgCertSecret is the TLS Secret name cert-manager writes for an org's wildcard
// (referenced by both the Certificate and the per-org Gateway listeners).
func OrgCertSecret(orgSlug string) string { return "vortex-org-tls-" + sanitize(orgSlug) }

// orgListenerName is the shared-Gateway HTTPS listener name for the org-level
// wildcard ("*.<org>.<base>"). Gateway listener names are DNS-1123 labels.
func orgListenerName(orgSlug string) string { return "o-" + sanitize(orgSlug) }

// projectListenerName is the shared-Gateway HTTPS listener name for a project
// wildcard ("*.<proj>.<org>.<base>"), kept distinct per org+project.
func projectListenerName(orgSlug, projSlug string) string {
	return "o-" + sanitize(orgSlug) + "-p-" + sanitize(projSlug)
}

// domainSlug derives a DNS-1123-safe, deterministic, collision-resistant slug
// from an arbitrary custom hostname so it can name a Certificate / Secret /
// Gateway listener. It combines a sanitized, length-capped prefix of the host
// with a short hash of the FULL host, so two distinct hosts that sanitize to the
// same prefix never collide on the same object name.
func domainSlug(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	sum := sha256.Sum256([]byte(host))
	short := hex.EncodeToString(sum[:])[:10]
	prefix := sanitize(host)
	if len(prefix) > 40 {
		prefix = strings.Trim(prefix[:40], "-")
	}
	if prefix == "" {
		return short
	}
	return prefix + "-" + short
}

// DomainCertName is the cert-manager Certificate object name for a custom host.
func DomainCertName(host string) string { return "vortex-dom-" + domainSlug(host) }

// DomainCertSecret is the TLS Secret name cert-manager writes for a custom host
// (referenced by both the Certificate and the Gateway listener).
func DomainCertSecret(host string) string { return "vortex-dom-tls-" + domainSlug(host) }

// domainListenerPrefix prefixes every custom-domain HTTPS listener name so the
// sharding capacity math (customListenerCount) can distinguish custom-domain
// listeners from the base wildcard/http listeners.
const domainListenerPrefix = "d-"

// DomainListenerName is the shared-Gateway HTTPS listener name for a custom host.
// Gateway listener names are validated as DNS-1123 labels (<=63 chars).
func DomainListenerName(host string) string { return domainListenerPrefix + domainSlug(host) }

// milliCPU renders cores as a millicore quantity string, e.g. 0.2 -> "200m".
// It rounds to the nearest millicore so the result is always integral.
func milliCPU(cores float64) string {
	return fmt.Sprintf("%dm", int64(math.Round(cores*1000)))
}

// mib renders a memory amount in MiB, e.g. 358 -> "358Mi".
func mib(mb int) string { return fmt.Sprintf("%dMi", mb) }

// gib renders a storage amount in GiB, e.g. 1 -> "1Gi".
func gib(gb int) string { return fmt.Sprintf("%dGi", gb) }

// Region scheduling label/annotation/taint keys.
//
//   - topologyRegionLabel is the WELL-KNOWN Kubernetes region label
//     (topology.kubernetes.io/region). Cloud providers (incl. DOKS) stamp it on
//     every node from the node-pool's region, so it is the most portable key to
//     steer placement by region.
//   - platformRegionLabel is Vortex's own region label. An operator can apply it
//     to a node pool (e.g. via the pool's k8s-labels) when the well-known label is
//     not granular enough (e.g. several logical "regions" share one cloud region),
//     keeping region naming admin/DB-driven rather than tied to cloud zone names.
//   - platformRegionTaint is the taint KEY a dedicated regional node pool may carry
//     (value = the region) so that ONLY workloads tolerating it land there. We emit
//     a matching toleration so region-pinned workloads can schedule onto such pools;
//     unlabeled/untainted clusters are unaffected.
const (
	topologyRegionLabel = "topology.kubernetes.io/region"
	platformRegionLabel = "vortex.v60ai.com/region"
	platformRegionTaint = "vortex.v60ai.com/region"
)

// regionScheduling translates a (validated) placement region into the
// common-chart's deployment.affinity and a deployment.tolerations entry so the
// workload ACTUALLY schedules onto the matching node pool inside the single
// cluster — the foundation for multi-region, not just a dormant label.
//
// Design (in-cluster, single-cluster foundation):
//
//   - SOFT (preferred) node affinity on BOTH the well-known region label
//     (topology.kubernetes.io/region) and the platform region label
//     (vortex.v60ai.com/region). It is intentionally a *preference*, not a hard
//     requiredDuringScheduling rule: a single-cluster install whose only node pool
//     is NOT region-labeled must still schedule the pod (non-breaking foundation).
//     When region-labeled pools DO exist, the scheduler steers the pod onto them.
//   - A toleration for the platform region taint (vortex.v60ai.com/region=<region>),
//     so an operator can dedicate (taint) a node pool to a region and have only
//     matching workloads land there. Harmless when no such taint exists.
//
// It returns nil for an empty region so callers leave the chart defaults untouched
// (no scheduling constraints, identical to pre-region behavior).
//
// We use SOFT affinity rather than a HARD deployment.nodeSelector on purpose: a
// nodeSelector would make the pod unschedulable on a cluster whose pools are not
// yet region-labeled. Promoting to a hard nodeSelector / requiredDuringScheduling
// pin is a documented opt-in for once every pool is guaranteed region-labeled.
//
// NOTE (centralization): scheduling translation lives here next to the overcommit
// math so all "how a Workload maps onto cluster resources" decisions stay in one
// place (kube/values.go), per the project invariants.
//
// TODO(multi-region, cross-CLUSTER): true global multi-region — placing/routing a
// workload onto a SEPARATE cluster in another physical region, with per-region
// Gateways/LoadBalancers and geo/Anycast DNS — is a from-scratch build and is NOT
// implemented here. That requires a cluster registry + a region-aware deploy
// router (pick the Backend by region) and is deliberately out of scope. This
// function only steers placement WITHIN the one cluster. Do not fake a 2nd cluster.
func regionScheduling(region string) (affinity, toleration map[string]any) {
	region = sanitize(region)
	if region == "" {
		return nil, nil
	}

	// Preferred (soft) node affinity: prefer nodes whose region label matches, on
	// either the well-known or the platform key. The well-known label is weighted
	// higher (it is the portable, provider-stamped key); the platform label adds a
	// smaller nudge. The scheduler sums weights, so a node carrying BOTH labels is
	// most preferred.
	preferred := []map[string]any{
		regionAffinityTerm(topologyRegionLabel, region, 80),
		regionAffinityTerm(platformRegionLabel, region, 20),
	}
	affinity = map[string]any{
		"nodeAffinity": map[string]any{
			"preferredDuringSchedulingIgnoredDuringExecution": preferred,
		},
	}

	// Tolerate a dedicated-region taint so region-pinned workloads can schedule
	// onto a tainted regional pool. Equal-match on the region value.
	toleration = map[string]any{
		"key":      platformRegionTaint,
		"operator": "Equal",
		"value":    region,
		"effect":   "NoSchedule",
	}

	return affinity, toleration
}

// regionAffinityTerm builds one preferredDuringScheduling node-affinity term that
// prefers nodes whose labelKey equals region, with the given scheduler weight.
func regionAffinityTerm(labelKey, region string, weight int) map[string]any {
	return map[string]any{
		"weight": weight,
		"preference": map[string]any{
			"matchExpressions": []map[string]any{{
				"key":      labelKey,
				"operator": "In",
				"values":   []string{region},
			}},
		},
	}
}

// overcommitResources computes the chart `deployment.resources` block.
//
//	requests.cpu    = CPU      * cpuFactor   (cores -> millicores)
//	requests.memory = MemoryMB * memFactor   (Mi, rounded)
//	limits.cpu      = CPU                     (full requested, millicores)
//	limits.memory   = MemoryMB                (full requested, Mi)
//
// e.g. CPU 1.0 + cpuFactor 0.2 -> requests "200m", limits "1000m";
//
//	MemoryMB 1024 + memFactor 0.35 -> requests "358Mi", limits "1024Mi".
func overcommitResources(cpu float64, memMB int, cpuFactor, memFactor float64) map[string]any {
	reqMem := int(math.Round(float64(memMB) * memFactor))
	return map[string]any{
		"requests": map[string]any{
			"cpu":    milliCPU(cpu * cpuFactor),
			"memory": mib(reqMem),
		},
		"limits": map[string]any{
			"cpu":    milliCPU(cpu),
			"memory": mib(memMB),
		},
	}
}

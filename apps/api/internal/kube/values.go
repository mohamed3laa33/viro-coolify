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

// DomainListenerName is the shared-Gateway HTTPS listener name for a custom host.
// Gateway listener names are validated as DNS-1123 labels (<=63 chars).
func DomainListenerName(host string) string { return "d-" + domainSlug(host) }

// milliCPU renders cores as a millicore quantity string, e.g. 0.2 -> "200m".
// It rounds to the nearest millicore so the result is always integral.
func milliCPU(cores float64) string {
	return fmt.Sprintf("%dm", int64(math.Round(cores*1000)))
}

// mib renders a memory amount in MiB, e.g. 358 -> "358Mi".
func mib(mb int) string { return fmt.Sprintf("%dMi", mb) }

// gib renders a storage amount in GiB, e.g. 1 -> "1Gi".
func gib(gb int) string { return fmt.Sprintf("%dGi", gb) }

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

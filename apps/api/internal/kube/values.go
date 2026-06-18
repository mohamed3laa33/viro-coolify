package kube

import (
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

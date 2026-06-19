package kube

import (
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// external-dns publishing for tenant + verified custom domains
// ---------------------------------------------------------------------------
//
// When Config.ExternalDNSEnabled is set, the backend stamps external-dns
// annotations onto the dynamically-created routing objects (the chart's HTTPRoute
// and Service, and the shared Gateway / per-domain listeners) so the in-cluster
// external-dns controller publishes the matching DNS records AUTOMATICALLY — the
// operator no longer hand-enters a record per tenant host. Records are only
// emitted for hostnames that fall under a zone the platform actually manages
// (Config.ExternalDNSZones), so external-dns never tries to write into a zone it
// does not own: a verified custom domain whose zone the operator does NOT manage
// gets no annotation, and the tenant points DNS at the Gateway LB themselves.
//
// We annotate the Gateway-API objects (HTTPRoute + Gateway) because the
// gateway-httproute external-dns source derives records from the HTTPRoute
// hostnames and the parent Gateway's published LB address. The tenant Service is a
// ClusterIP (internal only), so its annotation is informational; the load-bearing
// records come from the HTTPRoute/Gateway path.

const (
	// externalDNSHostnameAnnotation tells external-dns which hostname(s) to publish
	// for an object (comma-separated). On a Gateway-API HTTPRoute external-dns also
	// reads spec.hostnames, but setting it explicitly keeps records deterministic.
	externalDNSHostnameAnnotation = "external-dns.alpha.kubernetes.io/hostname"
	// externalDNSTTLAnnotation sets the published record TTL (seconds).
	externalDNSTTLAnnotation = "external-dns.alpha.kubernetes.io/ttl"
	// defaultDNSRecordTTL is the fallback record TTL (seconds) when Config.DNSRecordTTL
	// is unset.
	defaultDNSRecordTTL = 300
)

// dnsRecordTTL returns the configured record TTL, or the default.
func (b *KubeBackend) dnsRecordTTL() int {
	if b.cfg.DNSRecordTTL > 0 {
		return b.cfg.DNSRecordTTL
	}
	return defaultDNSRecordTTL
}

// managedZones is the set of DNS zones the platform manages. With ExternalDNSZones
// configured those are used verbatim; otherwise the platform BaseDomain is the
// single managed zone (the generated tenant hosts all live under it). Returned
// lowercased and trimmed.
func (b *KubeBackend) managedZones() []string {
	zones := make([]string, 0, len(b.cfg.ExternalDNSZones)+1)
	for _, z := range b.cfg.ExternalDNSZones {
		if z = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(z, "."))); z != "" {
			zones = append(zones, z)
		}
	}
	if len(zones) == 0 {
		if base := strings.ToLower(strings.TrimSpace(b.cfg.BaseDomain)); base != "" {
			zones = append(zones, base)
		}
	}
	return zones
}

// hostInManagedZone reports whether host falls under one of the platform's managed
// zones (an exact match or a subdomain), so external-dns only ever publishes into a
// zone the operator owns. A leading-dot / case mismatch is normalized.
func hostInManagedZone(host string, zones []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, z := range zones {
		if host == z || strings.HasSuffix(host, "."+z) {
			return true
		}
	}
	return false
}

// externalDNSEnabled reports whether external-dns publishing is turned on.
func (b *KubeBackend) externalDNSEnabled() bool { return b.cfg.ExternalDNSEnabled }

// managedDNSHosts filters hosts down to those that fall under a managed zone,
// preserving order and de-duplicating. It is the set external-dns annotations are
// emitted for; hosts outside every managed zone are dropped (the tenant manages
// their DNS themselves).
func (b *KubeBackend) managedDNSHosts(hosts []string) []string {
	if !b.externalDNSEnabled() {
		return nil
	}
	zones := b.managedZones()
	out := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] || !hostInManagedZone(h, zones) {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// externalDNSAnnotations returns the external-dns hostname + ttl annotations for
// the given managed hosts, or nil when external-dns is off or no host is in a
// managed zone (so callers leave the object's annotations untouched).
func (b *KubeBackend) externalDNSAnnotations(hosts []string) map[string]any {
	managed := b.managedDNSHosts(hosts)
	if len(managed) == 0 {
		return nil
	}
	return map[string]any{
		externalDNSHostnameAnnotation: strings.Join(managed, ","),
		externalDNSTTLAnnotation:      strconv.Itoa(b.dnsRecordTTL()),
	}
}

// addExternalDNSToValues stamps the external-dns hostname + ttl annotations onto
// the chart's gateway (HTTPRoute) and service value blocks for every workload host
// that falls under a managed zone, so the rendered HTTPRoute/Service carry them and
// external-dns publishes the records. It is a no-op when external-dns is disabled
// or no host is managed (the common single-host tenant under the platform zone is
// covered; an unmanaged custom domain is skipped). Existing annotations are merged,
// never clobbered.
func (b *KubeBackend) addExternalDNSToValues(values map[string]any, hosts []string) {
	ann := b.externalDNSAnnotations(hosts)
	if ann == nil {
		return
	}
	if gw, ok := values["gateway"].(map[string]any); ok {
		mergeAnnotations(gw, ann)
	}
	if svc, ok := values["service"].(map[string]any); ok {
		mergeAnnotations(svc, ann)
	}
}

// mergeAnnotations merges add into parent["annotations"] (creating it if absent),
// preserving any annotations already present.
func mergeAnnotations(parent map[string]any, add map[string]any) {
	cur, _ := parent["annotations"].(map[string]any)
	if cur == nil {
		cur = map[string]any{}
	}
	for k, v := range add {
		cur[k] = v
	}
	parent["annotations"] = cur
}

// addHostToGatewayDNS adds host to the Gateway object's external-dns hostname
// annotation (accumulating the comma-separated set) and sets the ttl annotation,
// so external-dns publishes a record for the custom domain pointing at the
// Gateway's LoadBalancer address. It is a no-op (returns false) when external-dns
// is disabled or host is not under a managed zone; otherwise it mutates obj's
// metadata.annotations in place and returns true (so the caller persists the
// Gateway). Hostnames are kept de-duplicated and sorted for deterministic output.
func (b *KubeBackend) addHostToGatewayDNS(obj *unstructured.Unstructured, host string) bool {
	if !b.externalDNSEnabled() {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || !hostInManagedZone(host, b.managedZones()) {
		return false
	}
	ann := obj.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	set := map[string]bool{}
	if cur := strings.TrimSpace(ann[externalDNSHostnameAnnotation]); cur != "" {
		for _, h := range strings.Split(cur, ",") {
			if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
				set[h] = true
			}
		}
	}
	if set[host] && ann[externalDNSTTLAnnotation] == strconv.Itoa(b.dnsRecordTTL()) {
		return false // already present with the right ttl: nothing to change
	}
	set[host] = true
	hosts := make([]string, 0, len(set))
	for h := range set {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	ann[externalDNSHostnameAnnotation] = strings.Join(hosts, ",")
	ann[externalDNSTTLAnnotation] = strconv.Itoa(b.dnsRecordTTL())
	obj.SetAnnotations(ann)
	return true
}

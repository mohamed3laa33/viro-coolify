package httpx

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Package-local Prometheus metrics registry, hand-rolled (no third-party deps).
//
// EXPOSURE: the registry holds ONLY aggregate control-plane RED metrics and
// process/dependency health — never tenant data, request bodies, or raw paths
// (routes are recorded as chi route PATTERNS, not concrete URLs, to bound
// cardinality and avoid leaking ids). It is intended for an INTERNAL scrape and
// is gated by the server (a bearer token and/or a separate metrics listen addr,
// VORTEX_METRICS_ADDR); it is not mounted on the public tenant API surface.

// histogramBuckets are the fixed upper bounds (seconds) for the request-duration
// histogram. They cover sub-millisecond control-plane reads up to slow,
// helm-bound deploys.
var histogramBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// counter is a concurrency-safe monotonic counter.
type counter struct{ v atomic.Int64 }

func (c *counter) inc()       { c.v.Add(1) }
func (c *counter) get() int64 { return c.v.Load() }

// gauge is a concurrency-safe inc/dec value.
type gauge struct{ v atomic.Int64 }

func (g *gauge) inc()       { g.v.Add(1) }
func (g *gauge) dec()       { g.v.Add(-1) }
func (g *gauge) get() int64 { return g.v.Load() }

// histogram is a concurrency-safe cumulative histogram over histogramBuckets.
type histogram struct {
	counts []atomic.Int64 // per-bucket (le) counts, NON-cumulative; rendered cumulatively
	sum    atomic.Uint64  // sum of observed values, stored as math.Float64bits
	count  atomic.Int64
}

func newHistogram() *histogram {
	return &histogram{counts: make([]atomic.Int64, len(histogramBuckets))}
}

func (h *histogram) observe(v float64) {
	h.count.Add(1)
	addFloat(&h.sum, v)
	for i, b := range histogramBuckets {
		if v <= b {
			h.counts[i].Add(1)
			return
		}
	}
	// Values above the last bucket only land in +Inf (count), tracked separately.
}

// labelKey is the composite label set for an http RED series.
type labelKey struct {
	method string
	route  string
	status int
}

// registry holds all control-plane metrics. Maps are guarded by mu; the leaf
// counters/gauges/histograms are themselves atomic, so the hot path only takes mu
// to look a series up (and rarely to create one).
type registry struct {
	mu sync.RWMutex

	requests  map[labelKey]*counter // http_requests_total{method,route,status}
	durations map[string]*histogram // http_request_duration_seconds{route}
	inFlight  gauge                 // http_requests_in_flight

	// Background-tick + dependency counters (set/incremented by the server).
	tickRuns   map[string]*counter // <loop>_runs_total
	tickErrors map[string]*counter // <loop>_errors_total
	helmExecs  counter
	helmFails  counter

	// dbUp is read lazily by the renderer via the dbUpFn probe.
	dbUpFn func() bool

	buildVersion string
	buildCommit  string
}

func newRegistry() *registry {
	return &registry{
		requests:   map[labelKey]*counter{},
		durations:  map[string]*histogram{},
		tickRuns:   map[string]*counter{},
		tickErrors: map[string]*counter{},
	}
}

// observeRequest records one completed request: the {method,route,status} counter
// and the per-route duration histogram.
// knownMethods bounds the `method` label to the verbs the router actually serves
// so an attacker sending arbitrary HTTP method tokens can't mint unlimited series
// (a cardinality / scrape-DoS bomb). Anything else collapses to "other".
var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true, http.MethodHead: true,
}

func (r *registry) observeRequest(method, route string, status int, seconds float64) {
	if route == "" {
		route = "unmatched"
	}
	if !knownMethods[method] {
		method = "other"
	}
	k := labelKey{method: method, route: route, status: status}

	r.mu.RLock()
	c := r.requests[k]
	h := r.durations[route]
	r.mu.RUnlock()

	if c == nil || h == nil {
		r.mu.Lock()
		if c = r.requests[k]; c == nil {
			c = &counter{}
			r.requests[k] = c
		}
		if h = r.durations[route]; h == nil {
			h = newHistogram()
			r.durations[route] = h
		}
		r.mu.Unlock()
	}
	c.inc()
	h.observe(seconds)
}

// tickRun / tickError record a background-loop run/error by name.
func (r *registry) tickRun(name string)   { r.tickCounter(r.runMap, name).inc() }
func (r *registry) tickError(name string) { r.tickCounter(r.errMap, name).inc() }

func (r *registry) runMap() map[string]*counter { return r.tickRuns }
func (r *registry) errMap() map[string]*counter { return r.tickErrors }

func (r *registry) tickCounter(pick func() map[string]*counter, name string) *counter {
	r.mu.RLock()
	c := pick()[name]
	r.mu.RUnlock()
	if c != nil {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := pick()
	if c = m[name]; c == nil {
		c = &counter{}
		m[name] = c
	}
	return c
}

// render writes the full registry in the Prometheus 0.0.4 text exposition format.
func (r *registry) render() string {
	var b strings.Builder

	// build_info
	b.WriteString("# HELP vortex_build_info Build metadata of the running control plane.\n")
	b.WriteString("# TYPE vortex_build_info gauge\n")
	fmt.Fprintf(&b, "vortex_build_info{version=%q,commit=%q} 1\n",
		escapeLabel(r.buildVersion), escapeLabel(r.buildCommit))

	// db_up
	dbUp := 1
	if r.dbUpFn != nil && !r.dbUpFn() {
		dbUp = 0
	}
	b.WriteString("# HELP vortex_db_up Whether the control-plane store responded to a readiness ping (1) or not (0).\n")
	b.WriteString("# TYPE vortex_db_up gauge\n")
	fmt.Fprintf(&b, "vortex_db_up %d\n", dbUp)

	// in-flight
	b.WriteString("# HELP http_requests_in_flight In-flight control-plane HTTP requests.\n")
	b.WriteString("# TYPE http_requests_in_flight gauge\n")
	fmt.Fprintf(&b, "http_requests_in_flight %d\n", r.inFlight.get())

	r.mu.RLock()
	defer r.mu.RUnlock()

	// http_requests_total
	b.WriteString("# HELP http_requests_total Total control-plane HTTP requests by method, route pattern and status.\n")
	b.WriteString("# TYPE http_requests_total counter\n")
	for _, k := range sortedRequestKeys(r.requests) {
		fmt.Fprintf(&b, "http_requests_total{method=%q,route=%q,status=\"%d\"} %d\n",
			escapeLabel(k.method), escapeLabel(k.route), k.status, r.requests[k].get())
	}

	// http_request_duration_seconds (histogram, per route)
	b.WriteString("# HELP http_request_duration_seconds Control-plane HTTP request duration by route pattern.\n")
	b.WriteString("# TYPE http_request_duration_seconds histogram\n")
	for _, route := range sortedStringKeys(r.durations) {
		h := r.durations[route]
		var cum int64
		for i, bound := range histogramBuckets {
			cum += h.counts[i].Load()
			fmt.Fprintf(&b, "http_request_duration_seconds_bucket{route=%q,le=%q} %d\n",
				escapeLabel(route), formatFloat(bound), cum)
		}
		total := h.count.Load()
		fmt.Fprintf(&b, "http_request_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\n", escapeLabel(route), total)
		fmt.Fprintf(&b, "http_request_duration_seconds_sum{route=%q} %s\n", escapeLabel(route), formatFloat(loadFloat(&h.sum)))
		fmt.Fprintf(&b, "http_request_duration_seconds_count{route=%q} %d\n", escapeLabel(route), total)
	}

	// Background-loop runs/errors.
	b.WriteString("# HELP vortex_background_runs_total Background loop iterations by loop name.\n")
	b.WriteString("# TYPE vortex_background_runs_total counter\n")
	for _, name := range sortedStringKeys(r.tickRuns) {
		fmt.Fprintf(&b, "vortex_background_runs_total{loop=%q} %d\n", escapeLabel(name), r.tickRuns[name].get())
	}
	b.WriteString("# HELP vortex_background_errors_total Background loop errors by loop name.\n")
	b.WriteString("# TYPE vortex_background_errors_total counter\n")
	for _, name := range sortedStringKeys(r.tickErrors) {
		fmt.Fprintf(&b, "vortex_background_errors_total{loop=%q} %d\n", escapeLabel(name), r.tickErrors[name].get())
	}

	// Helm exec counters.
	b.WriteString("# HELP vortex_helm_exec_total Total helm executions invoked by the deploy backend.\n")
	b.WriteString("# TYPE vortex_helm_exec_total counter\n")
	fmt.Fprintf(&b, "vortex_helm_exec_total %d\n", r.helmExecs.get())
	b.WriteString("# HELP vortex_helm_exec_failures_total Failed helm executions.\n")
	b.WriteString("# TYPE vortex_helm_exec_failures_total counter\n")
	fmt.Fprintf(&b, "vortex_helm_exec_failures_total %d\n", r.helmFails.get())

	return b.String()
}

func sortedRequestKeys(m map[labelKey]*counter) []labelKey {
	keys := make([]labelKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})
	return keys
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// escapeLabel escapes a Prometheus label value (backslash, double-quote, newline).
func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// formatFloat renders a float in the Prometheus-preferred shortest form.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// addFloat atomically adds delta to a float64 stored as bits in an atomic.Uint64
// (CAS loop), so the histogram sum is concurrency-safe without a mutex.
func addFloat(u *atomic.Uint64, delta float64) {
	for {
		old := u.Load()
		nw := math.Float64bits(math.Float64frombits(old) + delta)
		if u.CompareAndSwap(old, nw) {
			return
		}
	}
}

func loadFloat(u *atomic.Uint64) float64 {
	return math.Float64frombits(u.Load())
}

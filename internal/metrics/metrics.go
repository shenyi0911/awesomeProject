// Package metrics implements a minimal Prometheus text exposition endpoint.
// It avoids an external client dependency so the demo stays lightweight while still exposing
// scrapeable counters and histograms.
package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Invoke phases. Together they explain where time is spent for one algorithm call.
const (
	PhaseGate        = "gate"
	PhaseMeta        = "meta"
	PhaseSession     = "session"
	PhaseDaprCall    = "dapr_call"
	PhaseAlgorithm   = "algorithm"
	PhasePostprocess = "postprocess"
	PhasePlatform    = "platform"
	PhaseTotal       = "total"
)

// durationBuckets covers sub-millisecond platform work up to one-minute algorithm calls.
var durationBuckets = []float64{
	0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

type seriesKey struct {
	name   string
	labels string
}

type histogram struct {
	counts []uint64
	sum    float64
	count  uint64
}

// Metrics collects counters and histograms and renders them in Prometheus text format.
type Metrics struct {
	mu         sync.Mutex
	started    time.Time
	counters   map[seriesKey]float64
	histograms map[seriesKey]*histogram
}

// New creates an empty metrics collector.
func New() *Metrics {
	return &Metrics{
		started:    time.Now(),
		counters:   map[seriesKey]float64{},
		histograms: map[seriesKey]*histogram{},
	}
}

// ObserveHTTP records one HTTP request by route template, method and status.
func (m *Metrics) ObserveHTTP(method, route string, status int, d time.Duration) {
	if m == nil {
		return
	}
	labels := encodeLabels(
		"method", method,
		"route", route,
		"status", strconv.Itoa(status),
	)
	m.mu.Lock()
	m.counters[seriesKey{"awesome_http_requests_total", labels}]++
	m.observeLocked("awesome_http_request_duration_seconds", labels, d)
	m.mu.Unlock()
}

// ObserveInvokePhase records one phase of an algorithm invocation for a code/method pair.
func (m *Metrics) ObserveInvokePhase(code, method, phase string, d time.Duration) {
	if m == nil {
		return
	}
	labels := encodeLabels("code", code, "method", method, "phase", phase)
	m.mu.Lock()
	m.observeLocked("awesome_invoke_phase_duration_seconds", labels, d)
	m.mu.Unlock()
}

// IncInvoke counts one invocation result. Status 0 means the call failed before reaching the algorithm.
func (m *Metrics) IncInvoke(code, method string, status int) {
	if m == nil {
		return
	}
	labels := encodeLabels("code", code, "method", method, "status", strconv.Itoa(status))
	m.mu.Lock()
	m.counters[seriesKey{"awesome_invoke_total", labels}]++
	m.mu.Unlock()
}

// Handler renders all metrics in Prometheus text exposition format.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.mu.Lock()
		defer m.mu.Unlock()

		var b strings.Builder
		m.writeRuntime(&b)
		writeCounters(&b, m.counters)
		writeHistograms(&b, m.histograms)
		_, _ = w.Write([]byte(b.String()))
	})
}

func (m *Metrics) observeLocked(name, labels string, d time.Duration) {
	k := seriesKey{name, labels}
	h := m.histograms[k]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(durationBuckets))}
		m.histograms[k] = h
	}
	seconds := d.Seconds()
	for i, bound := range durationBuckets {
		if seconds <= bound {
			h.counts[i]++
			break
		}
	}
	h.sum += seconds
	h.count++
}

func (m *Metrics) writeRuntime(b *strings.Builder) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(b, "# TYPE go_goroutines gauge\ngo_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(b, "# TYPE go_memstats_alloc_bytes gauge\ngo_memstats_alloc_bytes %d\n", ms.Alloc)
	fmt.Fprintf(b, "# TYPE go_memstats_sys_bytes gauge\ngo_memstats_sys_bytes %d\n", ms.Sys)
	fmt.Fprintf(b, "# TYPE process_start_time_seconds gauge\nprocess_start_time_seconds %d\n", m.started.Unix())
}

func writeCounters(b *strings.Builder, counters map[seriesKey]float64) {
	keys := make([]seriesKey, 0, len(counters))
	for k := range counters {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return keys[i].labels < keys[j].labels
	})
	lastName := ""
	for _, k := range keys {
		if k.name != lastName {
			fmt.Fprintf(b, "# TYPE %s counter\n", k.name)
			lastName = k.name
		}
		fmt.Fprintf(b, "%s %s\n", series(k.name, k.labels), formatFloat(counters[k]))
	}
}

func writeHistograms(b *strings.Builder, histograms map[seriesKey]*histogram) {
	keys := make([]seriesKey, 0, len(histograms))
	for k := range histograms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return keys[i].labels < keys[j].labels
	})
	lastName := ""
	for _, k := range keys {
		h := histograms[k]
		if k.name != lastName {
			fmt.Fprintf(b, "# TYPE %s histogram\n", k.name)
			lastName = k.name
		}
		var cumulative uint64
		for i, bound := range durationBuckets {
			cumulative += h.counts[i]
			fmt.Fprintf(b, "%s_bucket{%s} %d\n",
				k.name, withLabel(k.labels, "le", formatFloat(bound)), cumulative)
		}
		fmt.Fprintf(b, "%s_bucket{%s} %d\n", k.name, withLabel(k.labels, "le", "+Inf"), h.count)
		fmt.Fprintf(b, "%s_sum%s %s\n", k.name, braces(k.labels), formatFloat(h.sum))
		fmt.Fprintf(b, "%s_count%s %d\n", k.name, braces(k.labels), h.count)
	}
}

func encodeLabels(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(pairs[i])
		b.WriteString(`="`)
		b.WriteString(escapeLabel(pairs[i+1]))
		b.WriteString(`"`)
	}
	return b.String()
}

func withLabel(labels, name, value string) string {
	extra := name + `="` + escapeLabel(value) + `"`
	if labels == "" {
		return extra
	}
	return labels + "," + extra
}

func series(name, labels string) string {
	if labels == "" {
		return name
	}
	return name + "{" + labels + "}"
}

func braces(labels string) string {
	if labels == "" {
		return ""
	}
	return "{" + labels + "}"
}

func escapeLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

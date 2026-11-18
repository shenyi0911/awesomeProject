package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExposition(t *testing.T) {
	m := New()
	m.ObserveHTTP("GET", "/api/v1/algorithms", 200, 20*time.Millisecond)
	m.ObserveInvokePhase("demo", "calculate", PhaseDaprCall, 120*time.Millisecond)
	m.IncInvoke("demo", "calculate", 200)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	want := []string{
		`awesome_http_requests_total{method="GET",route="/api/v1/algorithms",status="200"} 1`,
		`awesome_http_request_duration_seconds_bucket{method="GET",route="/api/v1/algorithms",status="200",le="0.025"} 1`,
		`awesome_invoke_phase_duration_seconds_count{code="demo",method="calculate",phase="dapr_call"} 1`,
		`awesome_invoke_total{code="demo",method="calculate",status="200"} 1`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("exposition is missing %q\n%s", w, body)
		}
	}
}

func TestNilMetricsIsSafe(t *testing.T) {
	var m *Metrics
	m.ObserveHTTP("GET", "/", 200, time.Millisecond)
	m.ObserveInvokePhase("demo", "calculate", PhaseTotal, time.Millisecond)
	m.IncInvoke("demo", "calculate", 200)
}

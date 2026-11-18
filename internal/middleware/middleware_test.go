package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	platformmetrics "awesomeProject/internal/metrics"
	"awesomeProject/pkg/reqctx"
)

func newTestEngine(mw gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(mw)
	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/ready", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/api", func(c *gin.Context) {
		c.String(http.StatusOK, reqctx.OperatorFrom(c.Request.Context()))
	})
	return r
}

func TestCORSWildcard(t *testing.T) {
	r := newTestEngine(CORS(nil))
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Allow-Origin = %q, want *", got)
	}
}

func TestCORSAllowlist(t *testing.T) {
	r := newTestEngine(CORS([]string{"http://allowed.example"}))
	cases := map[string]string{
		"http://allowed.example": "http://allowed.example",
		"http://other.example":   "",
	}
	for origin, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Fatalf("origin %q: Allow-Origin = %q, want %q", origin, got, want)
		}
	}
}

func TestBearerAuth(t *testing.T) {
	r := newTestEngine(BearerAuth("secret", "alice"))

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200", w.Code)
	}
	if w.Body.String() != "alice" {
		t.Fatalf("operator = %q, want alice", w.Body.String())
	}
}

func TestBearerAuthAllowsProbes(t *testing.T) {
	r := newTestEngine(BearerAuth("secret", ""))
	for _, path := range []string{"/health", "/ready"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, w.Code)
		}
	}
}

func TestMetricsMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := platformmetrics.New()
	r := gin.New()
	r.Use(Metrics(m))
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `awesome_http_requests_total{method="GET",route="/x",status="200"} 1`) {
		t.Fatalf("metrics did not record the request:\n%s", rec.Body.String())
	}
}

func TestBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimit(10, 100))
	r.POST("/", func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.String(http.StatusRequestEntityTooLarge, "too large")
			return
		}
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 11)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

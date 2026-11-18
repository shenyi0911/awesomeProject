package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"awesomeProject/internal/handler"
	platformmetrics "awesomeProject/internal/metrics"
	"awesomeProject/pkg/response"
)

func newTestEngine(readiness func(context.Context) error) *gin.Engine {
	gin.SetMode(gin.TestMode)
	return New(&Dependencies{
		AlgorithmHandler: handler.NewAlgorithmHandler(nil),
		VersionHandler:   handler.NewVersionHandler(nil, nil, 0),
		InfoHandler:      handler.NewInfoHandler(nil),
		Readiness:        readiness,
	})
}

func TestHealth(t *testing.T) {
	r := newTestEngine(nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestReady(t *testing.T) {
	r := newTestEngine(func(context.Context) error { return nil })
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestReadyFails(t *testing.T) {
	r := newTestEngine(func(context.Context) error { return errors.New("db down") })
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	m := platformmetrics.New()
	r := New(&Dependencies{
		AlgorithmHandler: handler.NewAlgorithmHandler(nil),
		VersionHandler:   handler.NewVersionHandler(nil, nil, 0),
		InfoHandler:      handler.NewInfoHandler(nil),
		Metrics:          m,
	})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "go_goroutines") {
		t.Fatalf("metrics body is missing runtime metrics:\n%s", w.Body.String())
	}
}

func TestNoRoute(t *testing.T) {
	r := newTestEngine(nil)
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var body response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != response.CodeNotFound {
		t.Fatalf("code = %d, want %d", body.Code, response.CodeNotFound)
	}
}

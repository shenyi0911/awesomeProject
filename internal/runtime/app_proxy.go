package runtime

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"awesomeProject/pkg/logger"
)

// startAppProxy starts a local reverse proxy in front of the algorithm app. Dapr talks to this proxy,
// which lets the platform measure time spent inside the algorithm (per method) without scraping daprd.
func (m *Manager) startAppProxy(inst *AlgoInstance) (*http.Server, int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("listen algorithm app proxy failed: %w", err)
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", inst.appPort)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method := proxyMethod(r.URL.Path)
			observe := m.opts.ObserveAlgorithm != nil && method != ""
			start := time.Now()
			proxy.ServeHTTP(w, r)
			if observe {
				m.opts.ObserveAlgorithm(inst.Code, method, time.Since(start))
			}
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("algorithm app proxy stopped", "code", inst.Code, "error", err)
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	return srv, port, nil
}

// proxyMethod maps an app request path to a bounded method label; health probes are skipped.
func proxyMethod(path string) string {
	p := strings.Trim(path, "/")
	if p == "" {
		return ""
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	switch p {
	case "healthz", "health", "ready", "metrics", "dapr":
		return ""
	}
	if !daprMethodPattern.MatchString(p) || strings.Contains(p, "..") {
		return "other"
	}
	return p
}

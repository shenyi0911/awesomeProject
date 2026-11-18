package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// daprMethodPattern prevents path/query injection into the Dapr invoke URL.
var daprMethodPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// validDaprMethod rejects unsafe characters and traversal sequences.
func validDaprMethod(method string) bool {
	return daprMethodPattern.MatchString(method) && !strings.Contains(method, "..")
}

// Invoke calls the algorithm over HTTP through the Dapr sidecar and returns the raw body and status code.
// Instance state is read as a snapshot to avoid concurrent access while a process is being reclaimed.
func (m *Manager) Invoke(ctx context.Context, code, daprMethod, httpMethod string, body []byte, headers http.Header) ([]byte, int, error) {
	if !validDaprMethod(daprMethod) {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid method name: %q", daprMethod)
	}
	inst, ok := m.getInstance(code)
	if !ok {
		return nil, http.StatusNotFound, fmt.Errorf("algorithm not registered: %s", code)
	}
	inst.mu.Lock()
	ready := inst.manifest != nil
	healthy := inst.healthy.Load()
	daprHTTPPort := inst.daprHTTP
	inst.mu.Unlock()

	if !ready || !healthy {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("algorithm %s is not ready (launch may have failed or the process exited)", code)
	}

	target := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", daprHTTPPort, code, daprMethod)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, httpMethod, target, reader)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, m.opts.InvokeTimeout)
	defer cancel()
	req = req.WithContext(callCtx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, http.StatusBadGateway, readErr
	}
	return b, resp.StatusCode, nil
}

// ReleaseCheck is the release gate: any latest version that is not released is rejected (fail-closed).
func (m *Manager) ReleaseCheck(ctx context.Context, code string) (ok bool, httpStatus int, msg string) {
	if !m.opts.ReleaseCheckEnabled {
		return true, 0, ""
	}
	// Cache hit (including notFound).
	m.relCacheMu.Lock()
	if e, hit := m.relCache[code]; hit && time.Now().Before(e.expires) {
		m.relCacheMu.Unlock()
		return m.cacheDecision(code, e.status, e.notFound)
	}
	m.relCacheMu.Unlock()

	if m.opts.Resolver == nil {
		return false, http.StatusServiceUnavailable, "release check is not configured (no version resolver); request rejected"
	}
	versionID, _, status, err := m.opts.Resolver.LatestReleasedVersion(ctx, code)
	if err != nil {
		// A DB failure must not be cached as "not registered"; reject this call instead.
		return false, http.StatusServiceUnavailable, fmt.Sprintf("release status query failed for algorithm %s; request rejected", code)
	}
	notFound := versionID == "" && status == ""
	m.relCacheMu.Lock()
	m.relCache[code] = &relCacheEntry{status: status, notFound: notFound, expires: time.Now().Add(m.opts.ReleaseCacheTTL)}
	m.relCacheMu.Unlock()
	return m.cacheDecision(code, status, notFound)
}

func (m *Manager) cacheDecision(code, status string, notFound bool) (bool, int, string) {
	if notFound {
		return false, http.StatusNotFound, fmt.Sprintf("algorithm %s is not registered on the platform (cannot confirm release); request rejected", code)
	}
	if status != statusReleased {
		return false, http.StatusForbidden, fmt.Sprintf("latest version of algorithm %s is %s (not released); request rejected", code, status)
	}
	return true, 0, ""
}

// InvalidateRelease clears the release-state cache for an algorithm.
func (m *Manager) InvalidateRelease(code string) {
	m.relCacheMu.Lock()
	delete(m.relCache, code)
	m.relCacheMu.Unlock()
}

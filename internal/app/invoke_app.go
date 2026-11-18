package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/errs"
	"awesomeProject/internal/domain/manifest"
	"awesomeProject/internal/domain/port"
	"awesomeProject/internal/domain/service"
	platformmetrics "awesomeProject/internal/metrics"
	"awesomeProject/pkg/reqctx"
)

// InvokeResult is the result of one algorithm invocation (body, status code, session ID).
type InvokeResult struct {
	Body      []byte
	Status    int
	SessionID string
}

// InvokeApp orchestrates algorithm invocation: release gate -> session resolution -> forwarding -> response decoration.
type InvokeApp interface {
	Invoke(ctx context.Context, code, method string, body []byte, sessionID string) (*InvokeResult, error)
	Metadata(ctx context.Context, code string) ([]byte, int, error)
	Contract(ctx context.Context, code string) ([]manifest.MethodSpec, error)
}

type invokeApp struct {
	rt      port.Runtime
	algoSvc service.AlgorithmService
	sessSvc service.SessionService
	meta    *metaCache
	metrics *platformmetrics.Metrics
}

// NewInvokeApp creates the invocation orchestration service.
func NewInvokeApp(rt port.Runtime, algoSvc service.AlgorithmService, sessSvc service.SessionService, metaCacheTTL time.Duration, m *platformmetrics.Metrics) *invokeApp {
	return &invokeApp{rt: rt, algoSvc: algoSvc, sessSvc: sessSvc, meta: newMetaCache(metaCacheTTL), metrics: m}
}

func (a *invokeApp) Invoke(ctx context.Context, code, method string, body []byte, sessionID string) (*InvokeResult, error) {
	start := time.Now()
	finalStatus := 0
	var daprDuration time.Duration
	defer func() {
		total := time.Since(start)
		a.observe(code, method, platformmetrics.PhaseTotal, total)
		platform := total
		if daprDuration > 0 {
			platform = total - daprDuration
		}
		a.observe(code, method, platformmetrics.PhasePlatform, platform)
		a.metrics.IncInvoke(code, method, finalStatus)
	}()

	gateStart := time.Now()
	if _, ok := a.rt.Instance(code); !ok {
		a.observe(code, method, platformmetrics.PhaseGate, time.Since(gateStart))
		return nil, errs.NotFoundf("unknown algorithm code: %s", code)
	}
	if err := a.checkReleased(ctx, code); err != nil {
		a.observe(code, method, platformmetrics.PhaseGate, time.Since(gateStart))
		return nil, err
	}
	a.observe(code, method, platformmetrics.PhaseGate, time.Since(gateStart))

	metaStart := time.Now()
	meta, err := a.algorithmMeta(ctx, code)
	a.observe(code, method, platformmetrics.PhaseMeta, time.Since(metaStart))
	if err != nil {
		return nil, err
	}

	var sess *entity.Session
	if meta.stateful {
		sessionStart := time.Now()
		sess, _, err = a.sessSvc.Resolve(ctx, code, sessionID)
		a.observe(code, method, platformmetrics.PhaseSession, time.Since(sessionStart))
		if err != nil {
			return nil, mapSessionError(err)
		}
	}

	hdr := http.Header{}
	if id := reqctx.IDFrom(ctx); id != "" {
		hdr.Set("X-Request-Id", id)
	}
	if sess != nil {
		hdr.Set("X-Session-Id", sess.SessionID)
	}

	daprStart := time.Now()
	respBody, status, err := a.rt.Invoke(ctx, code, method, http.MethodPost, body, hdr)
	daprDuration = time.Since(daprStart)
	a.observe(code, method, platformmetrics.PhaseDaprCall, daprDuration)
	if err != nil {
		finalStatus = status
		kind := errs.KindBadGateway
		if status == http.StatusServiceUnavailable {
			kind = errs.KindUnavailable
		}
		return nil, errs.Wrap(kind, err, "invoke algorithm failed")
	}

	postStart := time.Now()
	if sess != nil {
		respBody = withSession(respBody, sess.SessionID)
	}
	// Override algorithm metadata with platform values only for the main calculate method;
	// pass other methods through unchanged.
	if method == "calculate" {
		info, _ := a.rt.Instance(code)
		respBody = enrichAlgorithmMeta(respBody, code, meta.name, info)
	}

	res := &InvokeResult{Body: respBody, Status: status}
	if sess != nil {
		res.SessionID = sess.SessionID
	}
	a.observe(code, method, platformmetrics.PhasePostprocess, time.Since(postStart))
	finalStatus = status
	return res, nil
}

// observe records an invocation phase; a nil collector is allowed so tests can skip metrics.
func (a *invokeApp) observe(code, method, phase string, d time.Duration) {
	if a.metrics != nil {
		a.metrics.ObserveInvokePhase(code, method, phase, d)
	}
}

// Metadata proxies the algorithm's self-description endpoint and shares the release gate with
// calculate, preventing bypass of the state check.
func (a *invokeApp) Metadata(ctx context.Context, code string) ([]byte, int, error) {
	start := time.Now()
	defer func() { a.observe(code, "metadata", platformmetrics.PhaseTotal, time.Since(start)) }()
	if _, ok := a.rt.Instance(code); !ok {
		return nil, http.StatusNotFound, errs.NotFoundf("unknown algorithm code: %s", code)
	}
	if err := a.checkReleased(ctx, code); err != nil {
		return nil, http.StatusForbidden, err
	}
	daprStart := time.Now()
	body, status, err := a.rt.Invoke(ctx, code, "metadata", http.MethodGet, nil, nil)
	a.observe(code, "metadata", platformmetrics.PhaseDaprCall, time.Since(daprStart))
	if err != nil {
		return nil, http.StatusBadGateway, errs.Wrap(errs.KindBadGateway, err, "fetch algorithm metadata failed")
	}
	return body, status, nil
}

// Contract returns the platform-side structured method contract (independent of the algorithm process).
func (a *invokeApp) Contract(ctx context.Context, code string) ([]manifest.MethodSpec, error) {
	return a.algoSvc.GetContract(ctx, code)
}

// InvalidateMeta drops the cached metadata of an algorithm after it is updated or deleted.
func (a *invokeApp) InvalidateMeta(code string) {
	a.meta.invalidate(code)
}

func (a *invokeApp) algorithmMeta(ctx context.Context, code string) (algoMeta, error) {
	if meta, ok := a.meta.get(code); ok {
		return meta, nil
	}
	algo, err := a.algoSvc.GetByCode(ctx, code)
	if err != nil {
		return algoMeta{}, err
	}
	meta := algoMeta{name: algo.Name, stateful: algo.Stateful}
	a.meta.set(code, meta)
	return meta, nil
}

// checkReleased maps release-gate rejections from the runtime to domain errors.
func (a *invokeApp) checkReleased(ctx context.Context, code string) error {
	ok, status, msg := a.rt.ReleaseCheck(ctx, code)
	if ok {
		return nil
	}
	switch status {
	case http.StatusNotFound:
		return errs.New(errs.KindNotFound, msg)
	case http.StatusForbidden:
		return errs.New(errs.KindForbidden, msg)
	default:
		return errs.New(errs.KindUnavailable, msg)
	}
}

func mapSessionError(err error) error {
	switch {
	case errors.Is(err, entity.ErrNotFound):
		return errs.New(errs.KindUnauthorized, "session not found or invalid; start a new first request to obtain a new sessionId")
	case errors.Is(err, entity.ErrExpired):
		return errs.New(errs.KindUnauthorized, "session expired; start a new first request to obtain a new sessionId")
	case errors.Is(err, entity.ErrCodeMismatch):
		return errs.New(errs.KindUnauthorized, "sessionId does not match the algorithm code (a session can only be reused by the same algorithm)")
	case errors.Is(err, entity.ErrInactive):
		return errs.New(errs.KindUnauthorized, "session is closed")
	case errors.Is(err, entity.ErrTooMany):
		return errs.New(errs.KindTooMany, "the algorithm reached its session limit; reuse an existing sessionId instead of creating a new one")
	default:
		return errs.Wrap(errs.KindInternal, err, "resolve session failed")
	}
}

// withSession injects sessionId into the top level of the algorithm response envelope.
// UseNumber preserves numeric literals, avoiding precision loss in float64 round trips.
func withSession(body []byte, sessionID string) []byte {
	env, ok := decodeObject(body)
	if !ok {
		return body
	}
	env["sessionId"] = sessionID
	return encodeObject(env, body)
}

// enrichAlgorithmMeta overrides the algorithm metadata block in the response with platform values.
func enrichAlgorithmMeta(body []byte, code, name string, info port.InstanceInfo) []byte {
	env, ok := decodeObject(body)
	if !ok {
		return body
	}
	data, ok := env["data"].(map[string]interface{})
	if !ok {
		return body
	}
	if name == "" {
		name = code
	}
	data["algorithm"] = map[string]interface{}{
		"code":      code,
		"name":      name,
		"version":   info.Version,
		"versionId": info.VersionID,
	}
	return encodeObject(env, body)
}

func decodeObject(body []byte) (map[string]interface{}, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var env map[string]interface{}
	if err := dec.Decode(&env); err != nil {
		return nil, false
	}
	return env, true
}

func encodeObject(env map[string]interface{}, fallback []byte) []byte {
	out, err := json.Marshal(env)
	if err != nil {
		return fallback
	}
	return out
}

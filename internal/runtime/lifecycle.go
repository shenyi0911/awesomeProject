package runtime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"awesomeProject/pkg/logger"
)

// Runtime timing constants kept in one place instead of scattered magic numbers.
const (
	launchInProgressWait = 90 * time.Second       // Maximum wait for a concurrent launch to finish.
	launchInProgressPoll = 200 * time.Millisecond // Poll interval while waiting for a concurrent launch.
	healthCheckTimeout   = 60 * time.Second       // Maximum wait for the process health endpoint.
	healthCheckInterval  = 1 * time.Second        // Health probe interval.
	restartBackoffUnit   = 3 * time.Second        // Linear restart backoff unit.
	releaseCheckTimeout  = 5 * time.Second        // Timeout for the detached release-status query.
	healthProbeTimeout   = 5 * time.Second        // Per-probe timeout so a silent endpoint cannot block launch forever.
)

// launch starts one algorithm instance and waits until it is ready, downloading the released
// version files from object storage first.
func (m *Manager) launch(ctx context.Context, inst *AlgoInstance) error {
	if err := m.ensureStaged(ctx, inst); err != nil {
		return fmt.Errorf("prepare algorithm %s failed (not launched): %w", inst.Code, err)
	}

	inst.mu.Lock()
	appPort := inst.appPort
	mfst := inst.manifest
	inst.mu.Unlock()
	if mfst == nil {
		return fmt.Errorf("algorithm %s has no launch contract bound", inst.Code)
	}
	if !isPortFree(appPort) {
		return fmt.Errorf("listen port %d of algorithm %s is already in use; check for port conflicts", appPort, inst.Code)
	}

	// New launch round: clear the stopping flag from the previous round.
	inst.mu.Lock()
	inst.stopping = false
	inst.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("launch of algorithm %s canceled: %w", inst.Code, err)
	}
	if m.rootCtx != nil && m.rootCtx.Err() != nil {
		return fmt.Errorf("launch of algorithm %s canceled: service is shutting down", inst.Code)
	}
	if m.stopped.Load() {
		return fmt.Errorf("launch of algorithm %s canceled: runtime is stopping", inst.Code)
	}
	// Re-check right before starting: publish/archive may have changed the released version during staging.
	if err := m.verifyReleaseUnchanged(ctx, inst); err != nil {
		return err
	}
	exitCh, err := m.startInstance(inst)
	if err != nil {
		return fmt.Errorf("start algorithm %s failed: %w", inst.Code, err)
	}
	if !m.waitHealthy(ctx, inst, exitCh) {
		m.killProcess(inst)
		return fmt.Errorf("algorithm %s health check timed out or the process exited early (check entry, port and process logs)", inst.Code)
	}
	inst.mu.Lock()
	inst.restarts = 0
	ver := inst.version
	staging := inst.stagingDir
	inst.mu.Unlock()
	m.cleanupOldStaging(inst.Code, staging)
	logger.Info("algorithm ready", "code", inst.Code, "version", ver, "app-port", appPort)
	return nil
}

// launchGuarded launches asynchronously with a re-entry guard held by the background goroutine
// until the launch finishes. The goroutine is tracked so shutdown can wait for it.
func (m *Manager) launchGuarded(inst *AlgoInstance) {
	m.trackLaunch(func() { m.launchIfIdle(inst) })
}

// launchWithRetry retries a startup launch with exponential backoff, so a transient download or
// dependency failure does not leave the algorithm permanently down until the next publish.
func (m *Manager) launchWithRetry(ctx context.Context, inst *AlgoInstance) {
	for attempt := 1; attempt <= startupLaunchAttempts; attempt++ {
		if inst.healthy.Load() {
			return
		}
		if inst.launching.CompareAndSwap(false, true) {
			err := m.launch(ctx, inst)
			inst.launching.Store(false)
			if err == nil {
				return
			}
			logger.Warn("startup launch failed", "code", inst.Code, "attempt", attempt, "error", err)
		}
		delay := startupRetryBaseDelay * time.Duration(1<<(attempt-1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
	logger.Error("launch retries exhausted", "code", inst.Code, "attempts", startupLaunchAttempts)
}

// launchIfIdle launches synchronously when the instance is not healthy and no launch is in flight.
// The launching flag stays set for the whole launch, so concurrent callers skip instead of stacking up.
func (m *Manager) launchIfIdle(inst *AlgoInstance) {
	if inst.healthy.Load() {
		return
	}
	if !inst.launching.CompareAndSwap(false, true) {
		return
	}
	defer inst.launching.Store(false)
	if inst.healthy.Load() {
		return
	}
	ctx := m.rootCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := m.launch(ctx, inst); err != nil {
		logger.Warn("background launch failed", "code", inst.Code, "error", err)
	}
}

// Launch is triggered by the publish action: registers and synchronously launches code, blocking
// until it is ready or the launch times out.
func (m *Manager) Launch(ctx context.Context, code string) error {
	inst, err := m.registerCode(ctx, code)
	if err != nil {
		return fmt.Errorf("register algorithm %s failed: %w", code, err)
	}
	if inst.healthy.Load() {
		return nil
	}
	// Wait up to launchInProgressWait for a launch that may already be in progress.
	deadline := time.Now().Add(launchInProgressWait)
	for inst.launching.Load() {
		if time.Now().After(deadline) {
			return fmt.Errorf("launch of algorithm %s timed out (another launch is still in progress)", code)
		}
		time.Sleep(launchInProgressPoll)
	}
	if inst.healthy.Load() {
		return nil
	}
	if !inst.launching.CompareAndSwap(false, true) {
		return m.Launch(ctx, code)
	}
	defer inst.launching.Store(false)
	return m.launch(ctx, inst)
}

// MaybeKill is triggered by the archive action: reclaims the process only when the algorithm has no
// released version left.
func (m *Manager) MaybeKill(ctx context.Context, code string) {
	inst, ok := m.getInstance(code)
	if !ok || !inst.hasProcess() {
		return
	}
	// Archiving may happen while the request is finishing, so use a detached timeout context for the status query.
	checkCtx, cancel := context.WithTimeout(context.Background(), releaseCheckTimeout)
	defer cancel()
	released, err := m.isReleased(checkCtx, code)
	if err != nil {
		logger.Warn("release status query failed; keeping the algorithm process", "code", code, "error", err)
		return
	}
	if released {
		logger.Info("algorithm still has a released version; keeping the process", "code", code)
		return
	}
	logger.Info("algorithm has no released version; reclaiming the process", "code", code)
	m.kill(inst)
}

func (inst *AlgoInstance) hasProcess() bool {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.cmd != nil
}

// kill stops the instance process and clears its local staging (reclaim never triggers auto restart).
func (m *Manager) kill(inst *AlgoInstance) {
	inst.mu.Lock()
	inst.stopping = true
	cmd := inst.cmd
	inst.cmd = nil
	inst.healthy.Store(false)
	staging := inst.stagingDir
	inst.manifest = nil
	inst.dir = ""
	inst.version = ""
	inst.versionID = ""
	inst.stagingDir = ""
	proxy := inst.proxy
	inst.proxy = nil
	inst.mu.Unlock()

	killProcessTree(cmd)
	if proxy != nil {
		_ = proxy.Close()
	}
	if staging != "" {
		if err := os.RemoveAll(staging); err != nil {
			logger.Warn("clean staging directory failed", "dir", staging, "error", err)
		} else {
			logger.Info("staging directory cleaned", "dir", staging)
		}
	}
}

// killProcess reclaims only the process and keeps staging, so the next launch can reuse it after
// a launch or health-check failure.
func (m *Manager) killProcess(inst *AlgoInstance) {
	inst.mu.Lock()
	inst.stopping = true
	cmd := inst.cmd
	inst.cmd = nil
	proxy := inst.proxy
	inst.proxy = nil
	inst.healthy.Store(false)
	inst.mu.Unlock()
	killProcessTree(cmd)
	if proxy != nil {
		_ = proxy.Close()
	}
}

// isReleased reports whether the algorithm's latest version is released.
// On error callers must keep the current state and must not kill the process.
func (m *Manager) isReleased(ctx context.Context, code string) (bool, error) {
	if m.opts.Resolver == nil {
		return false, fmt.Errorf("VersionResolver is not configured")
	}
	_, _, status, err := m.opts.Resolver.LatestReleasedVersion(ctx, code)
	if err != nil {
		return false, err
	}
	return status == statusReleased, nil
}

// verifyReleaseUnchanged ensures the staged version is still the active released version. This closes
// the race where an algorithm is archived or a new version is published while staging is in progress.
func (m *Manager) verifyReleaseUnchanged(ctx context.Context, inst *AlgoInstance) error {
	if m.opts.Resolver == nil {
		return fmt.Errorf("VersionResolver is not configured")
	}
	versionID, _, status, err := m.opts.Resolver.LatestReleasedVersion(ctx, inst.Code)
	if err != nil {
		return fmt.Errorf("check release status before launch failed: %w", err)
	}
	inst.mu.Lock()
	stagedVersionID := inst.versionID
	inst.mu.Unlock()
	if status != statusReleased || versionID == "" {
		return fmt.Errorf("algorithm %s has no released version; launch canceled", inst.Code)
	}
	if stagedVersionID != "" && versionID != stagedVersionID {
		return fmt.Errorf("released version of algorithm %s changed during launch; aborting to avoid starting a stale version", inst.Code)
	}
	return nil
}

// startInstance builds and starts the dapr run process and returns a channel signaling process exit.
func (m *Manager) startInstance(inst *AlgoInstance) (<-chan struct{}, error) {
	inst.mu.Lock()
	mfst := inst.manifest
	dir := inst.dir
	appPort := inst.appPort
	httpPort := inst.daprHTTP
	grpcPort := inst.daprGRPC
	inst.mu.Unlock()

	if mfst == nil {
		return nil, fmt.Errorf("manifest is not bound")
	}
	parts := strings.Fields(mfst.Entry)
	if len(parts) == 0 {
		return nil, fmt.Errorf("manifest entry is empty")
	}
	// Dapr talks to this local proxy, which forwards to the real algorithm port and measures app time.
	proxySrv, proxyPort, err := m.startAppProxy(inst)
	if err != nil {
		return nil, err
	}
	inst.mu.Lock()
	inst.proxy = proxySrv
	inst.mu.Unlock()
	args := []string{
		"run",
		"--app-id", inst.Code,
		"--app-port", strconv.Itoa(proxyPort),
		"--dapr-http-port", strconv.Itoa(httpPort),
		"--dapr-grpc-port", strconv.Itoa(grpcPort),
		"--placement-host-address", "",
		"--scheduler-host-address", "",
		"--",
	}
	args = append(args, parts...)

	cmd := exec.Command(m.opts.DaprBin, args...)
	cmd.Dir = dir
	cmd.Env = algorithmEnv(appPort)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		inst.mu.Lock()
		if inst.proxy == proxySrv {
			inst.proxy = nil
		}
		inst.mu.Unlock()
		_ = proxySrv.Close()
		return nil, err
	}
	inst.mu.Lock()
	inst.cmd = cmd
	inst.mu.Unlock()

	exitCh := make(chan struct{})
	go m.watchProcess(inst, cmd, exitCh)
	return exitCh, nil
}

// watchProcess waits for the process to exit; unless the instance was reclaimed intentionally,
// it auto-restarts within the configured limit.
func (m *Manager) watchProcess(inst *AlgoInstance, cmd *exec.Cmd, exitCh chan struct{}) {
	err := cmd.Wait()
	close(exitCh)

	inst.mu.Lock()
	var proxy *http.Server
	if inst.cmd == cmd {
		inst.cmd = nil
		proxy = inst.proxy
		inst.proxy = nil
	}
	inst.healthy.Store(false)
	stopping := inst.stopping
	inst.mu.Unlock()
	if proxy != nil {
		_ = proxy.Close()
	}

	if stopping {
		logger.Info("algorithm process reclaimed", "code", inst.Code)
		return
	}
	logger.Warn("algorithm process exited", "code", inst.Code, "error", err)
	m.scheduleRestart(inst)
}

// scheduleRestart retries with linear backoff up to the configured limit.
func (m *Manager) scheduleRestart(inst *AlgoInstance) {
	if m.opts.MaxRestarts <= 0 {
		return
	}
	inst.mu.Lock()
	inst.restarts++
	attempt := inst.restarts
	inst.mu.Unlock()

	if attempt > m.opts.MaxRestarts {
		logger.Error("algorithm process keeps exiting; restart limit reached", "code", inst.Code, "attempts", attempt)
		return
	}

	ctx := m.rootCtx
	if ctx == nil {
		ctx = context.Background()
	}
	released, err := m.isReleased(ctx, inst.Code)
	if err != nil || !released {
		return
	}
	delay := time.Duration(attempt) * restartBackoffUnit
	logger.Warn("algorithm process exited unexpectedly; scheduling restart", "code", inst.Code, "attempt", attempt, "delay", delay)
	go func() {
		select {
		case <-time.After(delay):
			// The instance may have been reclaimed meanwhile (archive or publish rollback); do not restart then.
			inst.mu.Lock()
			stopped := inst.stopping
			inst.mu.Unlock()
			if stopped {
				return
			}
			m.launchWithRetry(ctx, inst)
		case <-ctx.Done():
		}
	}()
}

// waitHealthy blocks until the algorithm becomes healthy for up to healthCheckTimeout, returning false
// early if the process exits or ctx is canceled.
func (m *Manager) waitHealthy(ctx context.Context, inst *AlgoInstance, exitCh <-chan struct{}) bool {
	inst.mu.Lock()
	appPort := inst.appPort
	mfst := inst.manifest
	inst.mu.Unlock()
	if mfst == nil {
		return false
	}
	url := fmt.Sprintf("http://localhost:%d%s", appPort, mfst.Health)
	deadline := time.After(healthCheckTimeout)
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()
	for {
		probeCtx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
		req, rerr := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
		healthy := false
		if rerr == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				healthy = resp.StatusCode == 200
				_ = resp.Body.Close()
			}
		}
		cancel()
		if healthy {
			inst.healthy.Store(true)
			return true
		}
		select {
		case <-exitCh:
			return false
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case <-ticker.C:
		}
	}
}

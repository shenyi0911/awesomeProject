// Package runtime is the algorithm runtime orchestrator (a drop-in execution layer).
//
// Responsibilities:
//   - Algorithm files are stored per version in object storage; the platform is the source of truth,
//     and the runtime relies on no fixed directory
//   - On startup, algorithms whose latest version is released are registered and launched; the rest
//     are launched when a publish action triggers it
//   - On launch, all files of the released version are downloaded from object storage into a local
//     staging directory, algorithm.yaml is parsed, and the process is started with dapr run
//   - Publish triggers a launch; archive reclaims the process once no released version remains
//   - Before each call, a release gate rejects (fail-closed) unless the latest version is released
//   - Abnormally exited processes are restarted up to a configurable limit; graceful reclaim does not restart
//
// Files:
//   - runtime.go   core types and lifecycle entry points (Manager/Options/AlgoInstance/Start/Stop/Instance)
//   - ports.go     port allocation and registration (registerCode / reservePort)
//   - staging.go   object storage download, staging directory, dependency installation, manifest lookup
//   - lifecycle.go launch scheduling, process supervision, self-healing restarts, process reclaim
//   - invoke.go    Dapr forwarding and the release gate
package runtime

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"awesomeProject/internal/domain/manifest"
	"awesomeProject/internal/domain/port"
	"awesomeProject/pkg/logger"
)

// statusReleased is the released status, matching enum.VersionStatusReleased.
const statusReleased = "released"

// startupLaunchConcurrency bounds how many algorithms are registered and launched at once on startup.
const startupLaunchConcurrency = 4

// Startup retry and shutdown constants.
const (
	startupLaunchAttempts = 3
	startupRetryBaseDelay = 5 * time.Second
	stopWaitTimeout       = 30 * time.Second
)

// AlgoInstance is the runtime instance of one algorithm; fields must be accessed through inst.mu.
type AlgoInstance struct {
	Code string

	mu         sync.Mutex
	manifest   *manifest.Manifest
	dir        string // Working directory of the process (where the manifest lives).
	stagingDir string // Local staging directory.
	version    string
	versionID  string
	appPort    int
	daprHTTP   int
	daprGRPC   int
	proxy      *http.Server
	cmd        *exec.Cmd
	stopping   bool
	restarts   int
	initErr    error
	ready      chan struct{}

	healthy   atomic.Bool // Whether the process is ready (health check passed).
	launching atomic.Bool // Whether a launch is in progress (re-entry guard).
}

// Options configures the orchestrator (built from config in main).
type Options struct {
	Workspace           string
	DaprBin             string
	ReleaseCheckEnabled bool
	ReleaseCacheTTL     time.Duration
	InvokeTimeout       time.Duration
	MaxRestarts         int                  // Maximum automatic restarts after an abnormal exit; <=0 disables restarts.
	Resolver            port.VersionResolver // Source of truth for versions.
	Downloader          port.BlobDownloader  // Object storage download.
	PortResolver        port.PortResolver    // Reads and writes algorithm listen ports.
	// ObserveAlgorithm receives the time spent inside the algorithm app, measured by the local
	// reverse proxy that sits between Dapr and the algorithm. Nil disables this metric.
	ObserveAlgorithm func(code, method string, d time.Duration)
}

// DefaultWorkspace returns the default staging root (under the user cache directory, outside the source tree).
func DefaultWorkspace() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return "runtime-workspace"
	}
	return filepath.Join(base, "algorithm-platform", "runtime-workspace")
}

type relCacheEntry struct {
	status   string
	notFound bool
	expires  time.Time
}

// Manager is the algorithm runtime orchestrator.
type Manager struct {
	opts Options

	mu       sync.RWMutex
	registry map[string]*AlgoInstance

	portMu    sync.Mutex
	usedPorts map[int]bool

	relCacheMu sync.Mutex
	relCache   map[string]*relCacheEntry

	// launchMu guards launchWG registration so Stop can wait without racing new launches.
	launchMu sync.Mutex
	launchWG sync.WaitGroup
	stopped  atomic.Bool

	rootCtx context.Context
}

var _ port.Runtime = (*Manager)(nil)

// New creates the orchestrator, filling in default option values.
func New(opts Options) *Manager {
	if opts.DaprBin == "" {
		opts.DaprBin = "dapr"
	}
	if opts.ReleaseCacheTTL <= 0 {
		opts.ReleaseCacheTTL = 30 * time.Second
	}
	if opts.InvokeTimeout <= 0 {
		opts.InvokeTimeout = 60 * time.Second
	}
	if opts.Workspace == "" {
		opts.Workspace = DefaultWorkspace()
	}
	return &Manager{
		opts:      opts,
		registry:  map[string]*AlgoInstance{},
		usedPorts: map[int]bool{},
		relCache:  map[string]*relCacheEntry{},
	}
}

// Start registers and launches algorithms whose latest version is released.
func (m *Manager) Start(ctx context.Context) {
	m.launchMu.Lock()
	m.stopped.Store(false)
	m.launchMu.Unlock()
	m.rootCtx = ctx
	if m.opts.Resolver == nil {
		logger.Warn("version resolver is not configured, skip startup launch")
		return
	}
	codes, err := m.opts.Resolver.ListReleasedCodes(ctx)
	if err != nil {
		logger.Error("list released algorithms failed", "error", err)
		return
	}
	logger.Info("launching released algorithms on startup", "count", len(codes))
	// Bounded concurrency: each slot covers registration, launch and bounded retries.
	sem := make(chan struct{}, startupLaunchConcurrency)
	for _, code := range codes {
		m.trackLaunch(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			inst, rerr := m.registerCode(ctx, code)
			if rerr != nil {
				logger.Warn("register algorithm failed, skip launch", "code", code, "error", rerr)
				return
			}
			m.launchWithRetry(ctx, inst)
		})
	}
}

// trackLaunch runs fn in a goroutine tracked by Stop, refusing to start after shutdown begins.
func (m *Manager) trackLaunch(fn func()) {
	m.launchMu.Lock()
	if m.stopped.Load() {
		m.launchMu.Unlock()
		return
	}
	m.launchWG.Add(1)
	m.launchMu.Unlock()
	go func() {
		defer m.launchWG.Done()
		fn()
	}()
}

// Stop shuts down gracefully: stops new launches, kills all algorithm subprocesses and clears staging.
func (m *Manager) Stop() {
	m.launchMu.Lock()
	m.stopped.Store(true)
	m.launchMu.Unlock()

	m.killAll()
	m.waitLaunches(stopWaitTimeout)
	// A launch that was already running may have registered an instance after the first sweep.
	m.killAll()

	m.mu.Lock()
	m.registry = map[string]*AlgoInstance{}
	m.mu.Unlock()
}

// killAll reclaims every currently registered instance.
func (m *Manager) killAll() {
	m.mu.RLock()
	insts := make([]*AlgoInstance, 0, len(m.registry))
	for _, inst := range m.registry {
		insts = append(insts, inst)
	}
	m.mu.RUnlock()
	for _, inst := range insts {
		m.kill(inst)
	}
}

// waitLaunches waits for tracked launch goroutines, giving up after timeout.
func (m *Manager) waitLaunches(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		m.launchWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		logger.Warn("timed out waiting for launches to stop", "timeout", timeout)
	}
}

// Kill reclaims an algorithm instance immediately, regardless of its release state, and drops it
// from the registry. Used when the algorithm itself is deleted.
func (m *Manager) Kill(code string) {
	m.mu.Lock()
	inst, ok := m.registry[code]
	if ok {
		delete(m.registry, code)
	}
	m.mu.Unlock()
	if ok {
		inst.mu.Lock()
		ports := []int{inst.appPort, inst.daprHTTP, inst.daprGRPC}
		inst.mu.Unlock()
		m.kill(inst)
		m.releasePorts(ports...)
	}
	m.removeStagingRoot(code)
}

func (m *Manager) getInstance(code string) (*AlgoInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.registry[code]
	return inst, ok
}

// Instance returns a read-only snapshot of a running instance.
func (m *Manager) Instance(code string) (port.InstanceInfo, bool) {
	inst, ok := m.getInstance(code)
	if !ok {
		return port.InstanceInfo{}, false
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return port.InstanceInfo{
		Code:      inst.Code,
		Version:   inst.version,
		VersionID: inst.versionID,
		Healthy:   inst.healthy.Load(),
	}, true
}

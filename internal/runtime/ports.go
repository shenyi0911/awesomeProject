package runtime

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"awesomeProject/pkg/logger"
)

// Port allocation retry budgets.
const (
	freePortAttempts    = 10
	reservePortAttempts = 50
)

// registerCode registers (or returns) the instance for an algorithm code, allocating Dapr internal
// ports and the algorithm listen port. Port resolution happens outside the manager lock so DB queries
// do not block the whole registry; concurrent registrations are synchronized through the ready channel.
func (m *Manager) registerCode(ctx context.Context, code string) (*AlgoInstance, error) {
	m.mu.Lock()
	if inst, ok := m.registry[code]; ok {
		m.mu.Unlock()
		<-inst.ready
		inst.mu.Lock()
		initErr := inst.initErr
		inst.mu.Unlock()
		return inst, initErr
	}
	inst := &AlgoInstance{
		Code:     code,
		daprHTTP: m.reservePort(),
		ready:    make(chan struct{}),
	}
	inst.daprGRPC = m.reservePort(inst.daprHTTP)
	m.registry[code] = inst
	m.mu.Unlock()

	appPort := 0
	if m.opts.PortResolver != nil {
		if p, err := m.opts.PortResolver.PortForCode(ctx, code); err == nil && p > 0 && isPortFree(p) &&
			p != inst.daprHTTP && p != inst.daprGRPC {
			appPort = p
			m.markPortUsed(p)
		}
	}
	if appPort <= 0 {
		appPort = m.reservePort(inst.daprHTTP, inst.daprGRPC)
		if appPort > 0 && m.opts.PortResolver != nil {
			if err := m.opts.PortResolver.SetPort(ctx, code, appPort); err != nil {
				logger.Warn("persist algorithm port failed", "code", code, "port", appPort, "error", err)
			}
		}
	}

	inst.mu.Lock()
	if inst.appPort == 0 {
		inst.appPort = appPort
	}
	finalPort := inst.appPort
	httpPort := inst.daprHTTP
	grpcPort := inst.daprGRPC
	if finalPort <= 0 || httpPort <= 0 || grpcPort <= 0 {
		inst.initErr = fmt.Errorf("algorithm %s cannot allocate a free port (check local port resources)", code)
	}
	initErr := inst.initErr
	inst.mu.Unlock()
	close(inst.ready)

	if initErr != nil {
		// Do not keep a placeholder instance on init failure so later retries can succeed.
		m.mu.Lock()
		if m.registry[code] == inst {
			delete(m.registry, code)
		}
		m.mu.Unlock()
		return nil, initErr
	}
	if httpPort == grpcPort || httpPort == finalPort || grpcPort == finalPort {
		logger.Warn("duplicate port allocation detected, may affect launch", "code", code)
	}
	logger.Info("algorithm registered", "code", code, "app-port", finalPort, "dapr-http", httpPort, "dapr-grpc", grpcPort)
	return inst, nil
}

// isPortFree reports whether the given port can currently be bound.
func isPortFree(port int) bool {
	if port <= 0 {
		return false
	}
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// findFreePort asks the OS for a free port and releases it immediately.
func findFreePort() int {
	for i := 0; i < freePortAttempts; i++ {
		addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			continue
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		if isPortFree(port) {
			return port
		}
	}
	return 0
}

// reservePort picks and records a free port not already used by this process.
func (m *Manager) reservePort(exclude ...int) int {
	m.portMu.Lock()
	defer m.portMu.Unlock()
	for i := 0; i < reservePortAttempts; i++ {
		p := findFreePort()
		if p <= 0 || m.usedPorts[p] {
			continue
		}
		dup := false
		for _, e := range exclude {
			if p == e {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		m.usedPorts[p] = true
		return p
	}
	return 0
}

// markPortUsed records a platform-persisted port so other instances do not allocate it again.
func (m *Manager) markPortUsed(port int) {
	if port <= 0 {
		return
	}
	m.portMu.Lock()
	m.usedPorts[port] = true
	m.portMu.Unlock()
}

// releasePorts frees ports reserved for a removed instance (used when an algorithm is deleted).
func (m *Manager) releasePorts(ports ...int) {
	m.portMu.Lock()
	for _, p := range ports {
		if p > 0 {
			delete(m.usedPorts, p)
		}
	}
	m.portMu.Unlock()
}

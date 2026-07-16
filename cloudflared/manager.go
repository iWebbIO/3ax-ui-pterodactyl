package cloudflared

import (
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"
)

// restartDelay is how long the supervisor waits before relaunching a tunnel that
// exited unexpectedly (transient network drops, edge restarts, etc.).
const restartDelay = 5 * time.Second

// Manager owns the single cloudflared process for the panel. It is safe for
// concurrent use.
type Manager struct {
	mu   sync.Mutex
	cfg  Config
	proc *Process
	fp   string // fingerprint of the currently running config
	gen  int    // generation counter; bumped on every (re)start to void stale supervisors
}

var (
	managerOnce sync.Once
	manager     *Manager
)

// GetManager returns the process-wide cloudflared manager singleton.
func GetManager() *Manager {
	managerOnce.Do(func() { manager = &Manager{} })
	return manager
}

// Status is a snapshot of the tunnel state for the API/UI.
type Status struct {
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	Installed bool   `json:"installed"`
	Mode      string `json:"mode"`
	PublicURL string `json:"publicUrl"`
	Version   string `json:"version"`
	LastLog   string `json:"lastLog"`
}

// Apply reconciles the running process with the desired config: it starts, stops
// or restarts cloudflared as needed. A no-op when the config is unchanged and the
// process is already in the desired state.
func (m *Manager) Apply(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg = cfg

	if !cfg.Enabled {
		m.stopLocked()
		return nil
	}
	if err := cfg.Valid(); err != nil {
		m.stopLocked()
		return err
	}
	if !Installed() {
		m.stopLocked()
		logger.Warning("cloudflared: binary not found at", GetBinaryPath(), "- tunnel not started")
		return nil
	}

	// Already running the desired config? Nothing to do.
	if m.proc != nil && m.proc.IsRunning() && m.fp == cfg.fingerprint() {
		return nil
	}

	m.stopLocked()
	return m.startLocked()
}

// Stop tears down the tunnel and disables supervision.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Enabled = false
	m.stopLocked()
}

// Status returns a snapshot of the current tunnel state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{
		Enabled:   m.cfg.Enabled,
		Installed: Installed(),
		Mode:      string(m.cfg.Mode),
		Version:   GetVersion(),
	}
	if m.proc != nil {
		st.Running = m.proc.IsRunning()
		st.PublicURL = m.proc.logWriter.PublicURL()
		st.LastLog = m.proc.logWriter.LastLine()
	}
	return st
}

// buildArgs renders the cloudflared CLI arguments for the current config.
func (m *Manager) buildArgs() []string {
	// --no-autoupdate: never let the sidecar replace its own binary at runtime
	//   (the binary is managed by the image / bin folder).
	// Logs go to stderr, which the panel captures via procLogWriter.
	switch m.cfg.Mode {
	case ModeToken:
		return []string{"tunnel", "--no-autoupdate", "run", "--token", m.cfg.Token}
	default: // ModeQuick
		return []string{"tunnel", "--no-autoupdate", "--url", m.cfg.Target}
	}
}

func (m *Manager) startLocked() error {
	proc := newProcess(m.buildArgs())
	if err := proc.Start(); err != nil {
		return err
	}
	m.proc = proc
	m.fp = m.cfg.fingerprint()
	m.gen++
	gen := m.gen
	logger.Info("cloudflared: tunnel started (mode:", string(m.cfg.Mode)+")")
	go m.superviseExit(gen, proc)
	return nil
}

func (m *Manager) stopLocked() {
	if m.proc == nil {
		return
	}
	m.gen++ // void any pending supervisor for the process we're stopping
	if err := m.proc.Stop(); err != nil {
		logger.Warning("cloudflared: stop:", err)
	}
	m.proc = nil
	m.fp = ""
}

// superviseExit relaunches the tunnel if it exits unexpectedly while still
// desired. gen guards against acting on a process that has since been superseded.
func (m *Manager) superviseExit(gen int, proc *Process) {
	<-proc.done

	m.mu.Lock()
	stale := m.gen != gen
	m.mu.Unlock()
	if stale || proc.intentionalStop.Load() {
		return
	}

	logger.Warningf("cloudflared: tunnel exited unexpectedly, restarting in %s", restartDelay)
	time.AfterFunc(restartDelay, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.gen != gen || !m.cfg.Enabled {
			return
		}
		if err := m.startLocked(); err != nil {
			logger.Error("cloudflared: restart failed:", err)
		}
	})
}

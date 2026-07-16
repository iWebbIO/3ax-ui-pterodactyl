package cloudflared

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/config"
	"github.com/coinman-dev/3ax-ui/v2/logger"
)

var (
	gracefulStopTimeout = 5 * time.Second
	forceStopTimeout    = 2 * time.Second
)

// archBinaryName returns the cloudflared binary filename for this platform,
// matching the naming scheme used for the Xray and mtg binaries
// (e.g. cloudflared-linux-amd64).
func archBinaryName() string {
	name := fmt.Sprintf("cloudflared-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// GetBinaryPath returns the full path to the cloudflared binary in the bin folder.
func GetBinaryPath() string {
	return config.GetBinFolderPath() + "/" + archBinaryName()
}

// Installed reports whether the cloudflared binary is present.
func Installed() bool {
	info, err := os.Stat(GetBinaryPath())
	return err == nil && !info.IsDir()
}

var (
	versionOnce   sync.Once
	cachedVersion string
)

// GetVersion returns the bundled cloudflared version (e.g. "2024.8.3"), or
// "unknown" if the binary is missing or unparseable. Cached after first call.
func GetVersion() string {
	versionOnce.Do(func() {
		cachedVersion = detectVersion()
	})
	return cachedVersion
}

func detectVersion() string {
	if !Installed() {
		return "unknown"
	}
	out, err := exec.Command(GetBinaryPath(), "--version").Output()
	if err != nil {
		return "unknown"
	}
	// "cloudflared version 2024.8.3 (built ...)" → "2024.8.3"
	fields := strings.Fields(strings.TrimSpace(string(out)))
	for i, f := range fields {
		if f == "version" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	if len(fields) > 0 {
		return fields[len(fields)-1]
	}
	return "unknown"
}

// quickURLRegex matches the ephemeral hostname cloudflared prints for a quick tunnel.
var quickURLRegex = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

// procLogWriter consumes cloudflared's stdout/stderr, forwards each line to the
// panel log, remembers the last line, and captures the quick-tunnel public URL
// when cloudflared prints it.
type procLogWriter struct {
	mu        sync.Mutex
	buf       string
	lastLine  string
	publicURL string
}

func (w *procLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf += string(p)
	for {
		i := strings.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i]
		w.buf = w.buf[i+1:]
		w.emitLocked(line)
	}
	return len(p), nil
}

func (w *procLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf != "" {
		line := w.buf
		w.buf = ""
		w.emitLocked(line)
	}
}

func (w *procLogWriter) emitLocked(line string) {
	trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
	if trimmed == "" {
		return
	}
	w.lastLine = trimmed
	if url := quickURLRegex.FindString(trimmed); url != "" {
		w.publicURL = url
		logger.Info("cloudflared: quick tunnel URL:", url)
	}
	logger.Infof("cloudflared | %s", trimmed)
}

func (w *procLogWriter) LastLine() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastLine
}

func (w *procLogWriter) PublicURL() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.publicURL
}

// Process wraps a single cloudflared invocation.
type Process struct {
	cmd             *exec.Cmd
	args            []string
	done            chan struct{}
	logWriter       *procLogWriter
	exitErr         error
	intentionalStop atomic.Bool
}

func newProcess(args []string) *Process {
	return &Process{
		args:      args,
		logWriter: &procLogWriter{},
	}
}

// IsRunning reports whether the cloudflared process is currently running.
func (p *Process) IsRunning() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	if p.done != nil {
		select {
		case <-p.done:
			return false
		default:
		}
	}
	return p.cmd.ProcessState == nil
}

// Start launches cloudflared with the process's arguments.
func (p *Process) Start() error {
	if p.IsRunning() {
		return errors.New("cloudflared is already running")
	}
	cmd := exec.Command(GetBinaryPath(), p.args...)
	cmd.Stdout = p.logWriter
	cmd.Stderr = p.logWriter
	p.cmd = cmd
	p.done = make(chan struct{})
	p.exitErr = nil
	p.intentionalStop.Store(false)
	if err := cmd.Start(); err != nil {
		close(p.done)
		p.cmd = nil
		return err
	}
	attachChildLifetime(cmd)
	go p.wait(cmd)
	return nil
}

func (p *Process) wait(cmd *exec.Cmd) {
	defer close(p.done)
	err := cmd.Wait()
	p.logWriter.Flush()
	if err == nil || p.intentionalStop.Load() {
		return
	}
	logger.Errorf("cloudflared: process exited: %v", err)
	p.exitErr = err
}

// Stop terminates the running cloudflared process gracefully, then forcefully.
func (p *Process) Stop() error {
	if !p.IsRunning() {
		return nil
	}
	p.intentionalStop.Store(true)

	if runtime.GOOS == "windows" {
		if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return p.waitForExit(forceStopTimeout)
	}

	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return p.waitForExit(forceStopTimeout)
		}
		return err
	}
	if err := p.waitForExit(gracefulStopTimeout); err == nil {
		return nil
	}
	logger.Warning("cloudflared: did not stop after SIGTERM, killing process")
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return p.waitForExit(forceStopTimeout)
}

func (p *Process) waitForExit(timeout time.Duration) error {
	if p.done == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("timed out waiting for cloudflared to stop after %s", timeout)
	}
}

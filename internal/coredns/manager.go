// Package coredns manages the lifecycle of a local CoreDNS process: locating
// the binary, starting and stopping it, and reporting status via a pidfile.
package coredns

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Manager controls a single CoreDNS process for a project.
type Manager struct {
	Binary   string // path to the coredns executable (required for Start/Restart)
	Corefile string // path to the Corefile
	WorkDir  string // directory CoreDNS runs in (so relative zone paths resolve)
	StateDir string // directory holding the pidfile and log (e.g. <root>/.devdns)
}

// Status describes whether CoreDNS is currently running.
type Status struct {
	Running bool
	PID     int
}

func (m *Manager) pidFile() string { return filepath.Join(m.StateDir, "coredns.pid") }
func (m *Manager) logFile() string { return filepath.Join(m.StateDir, "coredns.log") }

// FindBinary locates the coredns executable. Search order: the explicit path,
// $DEVDNS_COREDNS, <workDir>/bin/coredns, then $PATH.
func FindBinary(explicit, workDir string) (string, error) {
	var candidates []string
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if env := os.Getenv("DEVDNS_COREDNS"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, filepath.Join(workDir, "bin", "coredns"))
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	if p, err := exec.LookPath("coredns"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("coredns binary not found; install it with `make coredns` (or `brew install coredns`), " +
		"or point DEVDNS_COREDNS at an existing binary")
}

// Status reports whether the tracked CoreDNS process is alive.
func (m *Manager) Status() Status {
	pid, err := m.readPID()
	if err != nil || pid <= 0 || !processAlive(pid) {
		return Status{}
	}
	return Status{Running: true, PID: pid}
}

// Start launches CoreDNS in the background. It is idempotent: if a tracked
// process is already running it returns a message and no error.
func (m *Manager) Start() (string, error) {
	if st := m.Status(); st.Running {
		return fmt.Sprintf("CoreDNS is already running (pid %d)", st.PID), nil
	}
	if _, err := os.Stat(m.Corefile); err != nil {
		return "", fmt.Errorf("Corefile %s not found; run `devdns generate` first", m.Corefile)
	}
	if err := os.MkdirAll(m.StateDir, 0o755); err != nil {
		return "", err
	}
	logf, err := os.OpenFile(m.logFile(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	defer logf.Close()

	cmd := exec.Command(m.Binary, "-conf", m.Corefile)
	cmd.Dir = m.WorkDir
	cmd.Stdout = logf
	cmd.Stderr = logf
	// Put CoreDNS in its own process group so it keeps running after devdns exits.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("starting coredns: %w", err)
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(m.pidFile(), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return "", err
	}
	_ = cmd.Process.Release()

	// Give it a moment so we can surface immediate failures (e.g. a port that
	// needs privileges, or an address already in use).
	time.Sleep(500 * time.Millisecond)
	if !processAlive(pid) {
		_ = os.Remove(m.pidFile())
		tail := m.tailLog(15)
		if tail == "" {
			return "", fmt.Errorf("coredns exited immediately after start")
		}
		return "", fmt.Errorf("coredns exited immediately after start:\n%s", tail)
	}
	return fmt.Sprintf("CoreDNS started (pid %d); logs at %s", pid, m.logFile()), nil
}

// Stop terminates the tracked CoreDNS process. It is idempotent.
func (m *Manager) Stop() (string, error) {
	st := m.Status()
	if !st.Running {
		_ = os.Remove(m.pidFile())
		return "CoreDNS is not running", nil
	}
	proc, err := os.FindProcess(st.PID)
	if err != nil {
		return "", err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return "", fmt.Errorf("sending SIGTERM to pid %d: %w", st.PID, err)
	}
	for i := 0; i < 30 && processAlive(st.PID); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(st.PID) {
		_ = proc.Signal(syscall.SIGKILL)
	}
	_ = os.Remove(m.pidFile())
	return fmt.Sprintf("CoreDNS stopped (pid %d)", st.PID), nil
}

// Restart stops CoreDNS (if running) and starts it again.
func (m *Manager) Restart() (string, error) {
	if _, err := m.Stop(); err != nil {
		return "", err
	}
	return m.Start()
}

func (m *Manager) readPID() (int, error) {
	data, err := os.ReadFile(m.pidFile())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func (m *Manager) tailLog(n int) string {
	data, err := os.ReadFile(m.logFile())
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// processAlive reports whether a process with the given PID exists and is
// signalable by this user (signal 0 performs error checking without delivery).
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

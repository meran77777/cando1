// Package procmgr runs a cando1 tunnel as a detached background process,
// tracked by a small pid file and a log file next to the config it runs. This
// lets the interactive menu (and the start/stop/status CLI subcommands) manage
// a tunnel without holding the terminal open.
//
// The bookkeeping files live beside the config so a machine can run several
// independent tunnels (e.g. a server and a client config) at once, each tracked
// on its own. Timeouts here are deliberately short — stopping never blocks the
// menu for more than a couple of seconds.
package procmgr

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Instance describes the background process associated with one config file.
type Instance struct {
	ConfigPath string // absolute path to the TOML config
	PidPath    string // <config>.pid
	LogPath    string // <config>.log
}

// For returns the bookkeeping paths for a config file.
func For(configPath string) (Instance, error) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return Instance{}, err
	}
	return Instance{
		ConfigPath: abs,
		PidPath:    abs + ".pid",
		LogPath:    abs + ".log",
	}, nil
}

// Status reports whether the tracked process is currently running and its pid.
// A stale pid file (process gone) reads as not running.
func (i Instance) Status() (running bool, pid int) {
	p, err := i.readPid()
	if err != nil || p <= 0 {
		return false, 0
	}
	if processAlive(p) {
		return true, p
	}
	return false, 0
}

func (i Instance) readPid() (int, error) {
	b, err := os.ReadFile(i.PidPath)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// Start launches `exe -c <config>` as a detached background process whose
// output is appended to the log file, and records its pid. It refuses to start
// a second copy if one is already running for this config.
func (i Instance) Start(exe string) (int, error) {
	if running, pid := i.Status(); running {
		return pid, fmt.Errorf("already running (pid %d)", pid)
	}
	logf, err := os.OpenFile(i.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open log %s: %w", i.LogPath, err)
	}
	defer logf.Close()

	cmd := exec.Command(exe, "-c", i.ConfigPath)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	cmd.SysProcAttr = detachAttr() // new session on unix, so it survives our exit

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Detach: we never Wait on the child, so release its OS resources.
	_ = cmd.Process.Release()
	if err := os.WriteFile(i.PidPath, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return pid, fmt.Errorf("write pid file: %w", err)
	}
	return pid, nil
}

// Stop asks the process to terminate (SIGTERM for a graceful tunnel shutdown),
// waits briefly, then force-kills if it is still alive. The pid file is removed.
func (i Instance) Stop() error {
	pid, err := i.readPid()
	if err != nil {
		return fmt.Errorf("not running (no pid file)")
	}
	if !processAlive(pid) {
		_ = os.Remove(i.PidPath)
		return fmt.Errorf("not running (stale pid %d cleaned up)", pid)
	}
	if err := terminate(pid); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	// Wait up to ~2s for a clean exit (short by design), then force-kill.
	for n := 0; n < 20; n++ {
		if !processAlive(pid) {
			_ = os.Remove(i.PidPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = forceKill(pid)
	_ = os.Remove(i.PidPath)
	return nil
}

// Restart stops any running instance (ignoring "not running") and starts a new
// one. Returns the new pid.
func (i Instance) Restart(exe string) (int, error) {
	if running, _ := i.Status(); running {
		if err := i.Stop(); err != nil {
			return 0, err
		}
	}
	return i.Start(exe)
}

// LogTail returns the last n lines of the log file (fewer if the file is short).
func (i Instance) LogTail(n int) ([]string, error) {
	b, err := os.ReadFile(i.LogPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// SelfExe returns the absolute path to the currently running cando1 binary, used
// as the command to spawn for background instances.
func SelfExe() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

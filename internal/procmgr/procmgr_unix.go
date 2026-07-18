//go:build !windows

package procmgr

import "syscall"

// detachAttr puts the child in its own session so it is not killed when the
// parent (the interactive menu) exits or its terminal closes.
func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// processAlive reports whether a pid refers to a live process. Signal 0 performs
// existence/permission checking without actually delivering a signal.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// terminate requests a graceful shutdown (the tunnel handles SIGTERM cleanly).
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// forceKill is the last-resort SIGKILL.
func forceKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

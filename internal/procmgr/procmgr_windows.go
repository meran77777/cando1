//go:build windows

package procmgr

import (
	"os"
	"syscall"
)

// detachAttr: Windows has no session concept like unix; rely on the default.
func detachAttr() *syscall.SysProcAttr { return nil }

// processAlive is best-effort on Windows (FindProcess always succeeds), so we
// additionally probe with a zero-length signal which is unsupported and returns
// an error only when the process is gone.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func forceKill(pid int) error { return terminate(pid) }

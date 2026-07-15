// Package app wires a validated configuration to the running tunnel and
// handles graceful shutdown on interrupt.
package app

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/meran77777/cando1/internal/config"
	"github.com/meran77777/cando1/internal/tunnel"
	"github.com/meran77777/cando1/internal/xlog"
)

// Runnable is the common surface of the server and client.
type Runnable interface {
	Run() error
	Close() error
}

// RunConfig starts the tunnel described by c and blocks until it stops or the
// process is interrupted.
func RunConfig(c *config.Config) error {
	xlog.SetLevel(c.Log.Level)

	var r Runnable
	switch {
	case c.Server != nil:
		r = tunnel.NewServer(c.Server)
	case c.Client != nil:
		cl, err := tunnel.NewClient(c.Client)
		if err != nil {
			return err
		}
		r = cl
	default:
		return config.ErrNoRole
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		xlog.Infof("shutting down...")
		_ = r.Close()
	}()

	return r.Run()
}

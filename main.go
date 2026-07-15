// Command cando1 is a high-performance, DPI-resistant, multiplexed TCP tunnel.
//
// Usage:
//
//	cando1                      # interactive English menu / wizard
//	cando1 -c config.toml       # run whatever role the config defines
//	cando1 server -c srv.toml   # run as server (validates the role)
//	cando1 client -c cli.toml   # run as client (validates the role)
//	cando1 gen-token            # print a fresh random token
//	cando1 version              # print version
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"

	"github.com/meran77777/cando1/internal/app"
	"github.com/meran77777/cando1/internal/config"
	"github.com/meran77777/cando1/internal/menu"
)

// Build metadata, overridable via -ldflags "-X main.version=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		if err := menu.Run(version, commit, date); err != nil {
			fatal(err)
		}
		return
	}

	switch args[0] {
	case "menu":
		if err := menu.Run(version, commit, date); err != nil {
			fatal(err)
		}
	case "version", "-v", "--version":
		fmt.Printf("cando1 %s (commit %s, built %s)\n", version, commit, date)
	case "gen-token":
		fmt.Println(genToken())
	case "help", "-h", "--help":
		usage()
	case "server":
		runRole(args[1:], "server")
	case "client":
		runRole(args[1:], "client")
	default:
		if len(args[0]) > 0 && args[0][0] == '-' {
			runRole(args, "")
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

func runRole(args []string, forceRole string) {
	name := "cando1"
	if forceRole != "" {
		name += " " + forceRole
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	cfgPath := fs.String("c", "", "path to TOML config file (required)")
	_ = fs.Parse(args)

	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "error: -c <config.toml> is required")
		fs.Usage()
		os.Exit(2)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(err)
	}
	switch forceRole {
	case "server":
		if cfg.Server == nil {
			fatal(fmt.Errorf("%s is not a server config", *cfgPath))
		}
	case "client":
		if cfg.Client == nil {
			fatal(fmt.Errorf("%s is not a client config", *cfgPath))
		}
	}
	if err := app.RunConfig(cfg); err != nil {
		fatal(err)
	}
}

func genToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func usage() {
	fmt.Print(`cando1 - high-performance, DPI-resistant, multiplexed TCP tunnel

USAGE:
  cando1                     interactive menu / setup wizard
  cando1 -c <config.toml>    run the role defined by the config
  cando1 server -c <file>    run as server (validates role)
  cando1 client -c <file>    run as client (validates role)
  cando1 gen-token           print a fresh random token
  cando1 version             print version
  cando1 help                show this help

Run with no arguments for the guided English wizard.
`)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
	os.Exit(1)
}

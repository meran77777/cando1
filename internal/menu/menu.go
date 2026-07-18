// Package menu implements the interactive English-language console: a main menu
// plus a guided wizard that generates matching server/client configs for the
// two common Iran relay topologies.
package menu

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/meran77777/cando1/internal/app"
	"github.com/meran77777/cando1/internal/config"
)

var stdin = bufio.NewReader(os.Stdin)

// Channel is the project's Telegram channel, shown under the banner.
const Channel = "https://t.me/cando1tunnel"

const banner = `
   ██████╗ █████╗ ███╗   ██╗██████╗  ██████╗  ██╗
  ██╔════╝██╔══██╗████╗  ██║██╔══██╗██╔═══██╗███║
  ██║     ███████║██╔██╗ ██║██║  ██║██║   ██║╚██║
  ██║     ██╔══██║██║╚██╗██║██║  ██║██║   ██║ ██║
  ╚██████╗██║  ██║██║ ╚████║██████╔╝╚██████╔╝ ██║
   ╚═════╝╚═╝  ╚═╝╚═╝  ╚═══╝╚═════╝  ╚═════╝  ╚═╝
    high-performance · anti-DPI · multiplexed tunnel
`

// Run drives the interactive menu until the user exits.
func Run(version, commit, date string) error {
	fmt.Print(banner)
	fmt.Printf("    channel · %s\n", Channel)
	fmt.Printf("  version %s  (%s, %s)\n", version, commit, date)
	for {
		fmt.Println()
		fmt.Println("  ===================  MAIN MENU  ===================")
		fmt.Println("   [1]  Quick setup wizard  (generate configs & run)")
		fmt.Println("   [2]  Run as SERVER   from a config file (foreground)")
		fmt.Println("   [3]  Run as CLIENT   from a config file (foreground)")
		fmt.Println("   ---- background ----")
		fmt.Println("   [4]  Start a tunnel in the BACKGROUND")
		fmt.Println("   [5]  Stop a background tunnel")
		fmt.Println("   [6]  Status / logs of a background tunnel")
		fmt.Println("   [7]  Edit a tunnel  (add / remove ports)")
		fmt.Println("   --------------------")
		fmt.Println("   [8]  Show example configurations")
		fmt.Println("   [9]  Generate a random token")
		fmt.Println("   [h]  Help & concepts")
		fmt.Println("   [0]  Exit")
		fmt.Println("  ==================================================")
		switch strings.ToLower(strings.TrimSpace(ask("  Select"))) {
		case "1":
			if err := wizard(); err != nil {
				fmt.Printf("  ! wizard error: %v\n", err)
			}
		case "2":
			runFromFile("server")
		case "3":
			runFromFile("client")
		case "4":
			startBackground()
		case "5":
			stopBackground()
		case "6":
			showStatus()
		case "7":
			editTunnel()
		case "8":
			printExamples()
		case "9":
			fmt.Printf("\n  token = %s\n", genToken())
		case "h", "help":
			printHelp()
		case "0", "q", "quit", "exit":
			fmt.Println("  bye.")
			return nil
		default:
			fmt.Println("  ? unknown option")
		}
	}
}

// ---- prompt helpers ----

func ask(label string) string {
	fmt.Printf("%s: ", label)
	line, _ := stdin.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func askDefault(label, def string) string {
	v := strings.TrimSpace(ask(fmt.Sprintf("%s [%s]", label, def)))
	if v == "" {
		return def
	}
	return v
}

func askRequired(label string) string {
	for {
		v := strings.TrimSpace(ask(label))
		if v != "" {
			return v
		}
		fmt.Println("  (required)")
	}
}

func askYesNo(label string, def bool) bool {
	d := "y/N"
	if def {
		d = "Y/n"
	}
	v := strings.ToLower(strings.TrimSpace(ask(fmt.Sprintf("%s [%s]", label, d))))
	if v == "" {
		return def
	}
	return v == "y" || v == "yes"
}

func askInt(label string, def int) int {
	v := strings.TrimSpace(ask(fmt.Sprintf("%s [%d]", label, def)))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func genToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// runLoaded starts an already-parsed config (used by the wizard).
func runLoaded(cfg *config.Config) error { return app.RunConfig(cfg) }

// ---- run from file ----

func runFromFile(role string) {
	def := "cando1-" + role + ".toml"
	path := askDefault("  Config file", def)
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Printf("  ! cannot load config: %v\n", err)
		return
	}
	if role == "server" && cfg.Server == nil {
		fmt.Println("  ! that file is not a server config")
		return
	}
	if role == "client" && cfg.Client == nil {
		fmt.Println("  ! that file is not a client config")
		return
	}
	fmt.Printf("  starting %s (Ctrl+C to stop and return to menu)...\n\n", role)
	if err := app.RunConfig(cfg); err != nil {
		fmt.Printf("  ! %s stopped: %v\n", role, err)
	}
}

func printHelp() {
	fmt.Print(`
  CONCEPTS
  --------
  cando1 links two machines with an encrypted, multiplexed tunnel and moves
  TCP ports across it. There are two roles:

    SERVER  - the anchor that accepts the tunnel and (optionally) exposes
              public ports.
    CLIENT  - dials the server and keeps the tunnel alive (auto-reconnect).

  Two forwarding directions:

    forward - the CLIENT opens a local port; every connection is tunneled to
              the SERVER, which dials a target address. Use this when the
              CLIENT is in Iran and should reach services via the foreign
              SERVER. (You choose which local ports to expose.)

    reverse - the SERVER opens a public port; every connection is tunneled to
              the CLIENT, which dials a local target. Use this when the CLIENT
              is abroad and Iran only relays: run the SERVER in Iran.

  Transports (pick per censorship conditions):
    tls  - uTLS browser-fingerprinted TLS. Best all-round DPI resistance.
    wss  - WebSocket over uTLS TLS. Looks like normal HTTPS/CDN traffic.
    ws   - plain WebSocket (use behind a TLS-terminating CDN/reverse proxy).
    tcp  - raw TCP, optionally with chacha20 obfuscation (obfs=true).

  Run the wizard [1] to generate a matching server+client pair.
`)
}

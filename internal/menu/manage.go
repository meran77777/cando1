package menu

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/meran77777/cando1/internal/config"
	"github.com/meran77777/cando1/internal/procmgr"
)

// ---- background process management (menu-driven) ----

// startBackground launches a config as a detached background tunnel.
func startBackground() {
	path := askDefault("  Config file to run in background", "cando1-server.toml")
	if _, err := config.Load(path); err != nil {
		fmt.Printf("  ! cannot load config: %v\n", err)
		return
	}
	inst, err := procmgr.For(path)
	if err != nil {
		fmt.Printf("  ! %v\n", err)
		return
	}
	exe, err := procmgr.SelfExe()
	if err != nil {
		fmt.Printf("  ! cannot locate cando1 binary: %v\n", err)
		return
	}
	pid, err := inst.Start(exe)
	if err != nil {
		fmt.Printf("  ! %v\n", err)
		return
	}
	fmt.Printf("  started in background (pid %d)\n", pid)
	fmt.Printf("  logs: %s\n", inst.LogPath)
	fmt.Println("  use [Status / logs] to check it, [Stop] to shut it down.")
}

// stopBackground stops a running background tunnel.
func stopBackground() {
	path := askDefault("  Config file to stop", "cando1-server.toml")
	inst, err := procmgr.For(path)
	if err != nil {
		fmt.Printf("  ! %v\n", err)
		return
	}
	if err := inst.Stop(); err != nil {
		fmt.Printf("  ! %v\n", err)
		return
	}
	fmt.Println("  stopped.")
}

// showStatus prints whether a background tunnel is running and its recent logs.
func showStatus() {
	path := askDefault("  Config file to inspect", "cando1-server.toml")
	inst, err := procmgr.For(path)
	if err != nil {
		fmt.Printf("  ! %v\n", err)
		return
	}
	running, pid := inst.Status()
	if running {
		fmt.Printf("  status: RUNNING (pid %d)\n", pid)
	} else {
		fmt.Println("  status: not running")
	}
	lines, err := inst.LogTail(20)
	if err != nil {
		fmt.Printf("  (no logs yet: %s)\n", inst.LogPath)
		return
	}
	if len(lines) == 0 {
		fmt.Println("  (log is empty)")
		return
	}
	fmt.Printf("  --- last %d log line(s) [%s] ---\n", len(lines), inst.LogPath)
	for _, l := range lines {
		fmt.Printf("  %s\n", l)
	}
}

// ---- interactive config editor (add/remove ports) ----

// editTunnel loads a config, lets the user add or remove port mappings, saves
// it back, and offers to restart a running background instance.
func editTunnel() {
	path := askDefault("  Config file to edit", "cando1-server.toml")
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Printf("  ! cannot load config: %v\n", err)
		return
	}
	switch {
	case cfg.Server != nil:
		editServer(cfg)
	case cfg.Client != nil:
		editClient(cfg)
	default:
		fmt.Println("  ! config has no [server] or [client] role")
		return
	}
	if err := cfg.Validate(); err != nil {
		fmt.Printf("  ! changes NOT saved — config would be invalid: %v\n", err)
		return
	}
	if !askYesNo("  Save changes?", true) {
		fmt.Println("  (discarded)")
		return
	}
	if err := saveConfig(path, cfg); err != nil {
		fmt.Printf("  ! save failed: %v\n", err)
		return
	}
	fmt.Printf("  saved %s  (note: hand-written comments are not preserved)\n", path)
	offerRestart(path)
}

func editClient(cfg *config.Config) {
	cl := cfg.Client
	for {
		fmt.Println("\n  CLIENT tunnel — current port mappings:")
		fmt.Println("   Forwards (local port on THIS client -> server dials the target):")
		if len(cl.Forwards) == 0 {
			fmt.Println("     (none)")
		}
		for i, f := range cl.Forwards {
			fmt.Printf("     [%d] %s (%s)   %s -> %s\n", i+1, nameOr(f.Name, "svc"), config.NormProto(f.Protocol), f.LocalAddr, f.TargetAddr)
		}
		fmt.Println("   Reverse services (server public port -> local target on this client):")
		if len(cl.Services) == 0 {
			fmt.Println("     (none)")
		}
		for i, s := range cl.Services {
			fmt.Printf("     [%d] %s   -> %s\n", i+1, s.Name, s.LocalAddr)
		}
		fmt.Print(`
   [af] add forward      [df] delete forward
   [ar] add reverse      [dr] delete reverse
   [q]  done editing
`)
		switch strings.ToLower(strings.TrimSpace(ask("  edit"))) {
		case "af":
			name := askDefault("    name", fmt.Sprintf("svc%d", len(cl.Forwards)+1))
			proto := askProto()
			local := askDefault("    local listen on THIS client (host:port)", "0.0.0.0:1080")
			target := askDefault("    target the server dials (host:port)", "127.0.0.1:1080")
			cl.Forwards = append(cl.Forwards, config.Forward{Name: name, Protocol: proto, LocalAddr: local, TargetAddr: target})
		case "df":
			cl.Forwards = deleteAt(cl.Forwards, pickIndex("    delete forward #", len(cl.Forwards)))
		case "ar":
			name := askRequired("    reverse service name (must match the server's)")
			local := askDefault("    local target on this client (host:port)", "127.0.0.1:8443")
			cl.Services = append(cl.Services, config.ClientTarget{Name: name, LocalAddr: local})
		case "dr":
			cl.Services = deleteAt(cl.Services, pickIndex("    delete reverse #", len(cl.Services)))
		case "q", "":
			return
		default:
			fmt.Println("    ? unknown option")
		}
	}
}

func editServer(cfg *config.Config) {
	s := cfg.Server
	for {
		fmt.Println("\n  SERVER tunnel — current public services:")
		fmt.Println("   Reverse services (public port on THIS server -> tunneled to the client):")
		if len(s.Services) == 0 {
			fmt.Println("     (none)")
		}
		for i, svc := range s.Services {
			fmt.Printf("     [%d] %s (%s)   %s\n", i+1, svc.Name, config.NormProto(svc.Protocol), svc.BindAddr)
		}
		fmt.Printf("   allow_forward = %v  (client may open forward tunnels through this server)\n", s.AllowForward)
		fmt.Print(`
   [as] add service      [ds] delete service
   [tf] toggle allow_forward
   [q]  done editing
`)
		switch strings.ToLower(strings.TrimSpace(ask("  edit"))) {
		case "as":
			name := askRequired("    service name (must match the client's)")
			proto := askProto()
			bind := askDefault("    public bind on THIS server (host:port)", "0.0.0.0:8443")
			s.Services = append(s.Services, config.Service{Name: name, Protocol: proto, BindAddr: bind})
		case "ds":
			s.Services = deleteAt(s.Services, pickIndex("    delete service #", len(s.Services)))
		case "tf":
			s.AllowForward = !s.AllowForward
			fmt.Printf("    allow_forward is now %v\n", s.AllowForward)
		case "q", "":
			return
		default:
			fmt.Println("    ? unknown option")
		}
	}
}

// ---- helpers ----

// offerRestart restarts a running background instance for path, if any.
func offerRestart(path string) {
	inst, err := procmgr.For(path)
	if err != nil {
		return
	}
	running, pid := inst.Status()
	if !running {
		return
	}
	if !askYesNo(fmt.Sprintf("  A background tunnel is running (pid %d). Restart it to apply changes?", pid), true) {
		return
	}
	exe, err := procmgr.SelfExe()
	if err != nil {
		fmt.Printf("  ! cannot locate cando1 binary: %v\n", err)
		return
	}
	npid, err := inst.Restart(exe)
	if err != nil {
		fmt.Printf("  ! restart failed: %v\n", err)
		return
	}
	fmt.Printf("  restarted (pid %d)\n", npid)
}

// pickIndex prompts for a 1-based list index and returns the 0-based index, or
// -1 if the input is blank or out of range.
func pickIndex(label string, n int) int {
	if n == 0 {
		fmt.Println("    (nothing to delete)")
		return -1
	}
	v := strings.TrimSpace(ask(label))
	if v == "" {
		return -1
	}
	i, err := strconv.Atoi(v)
	if err != nil || i < 1 || i > n {
		fmt.Println("    ? out of range")
		return -1
	}
	return i - 1
}

// deleteAt removes element i from s (no-op if i is out of range).
func deleteAt[T any](s []T, i int) []T {
	if i < 0 || i >= len(s) {
		return s
	}
	return append(s[:i], s[i+1:]...)
}

func nameOr(name, def string) string {
	if strings.TrimSpace(name) == "" {
		return def
	}
	return name
}

// saveConfig writes a Config back to a TOML file. Comments in the original file
// are not preserved (the struct is re-encoded), but every functional field is.
func saveConfig(path string, c *config.Config) error {
	var b strings.Builder
	b.WriteString("# cando1 config (edited via the menu)\n\n")
	enc := toml.NewEncoder(&b)
	if err := enc.Encode(c); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

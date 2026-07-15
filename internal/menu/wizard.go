package menu

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/meran77777/cando1/internal/config"
)

type svcPair struct {
	name       string
	publicBind string // public bind on the server (Iran, scenario 2)
	localAddr  string // local target on the client (foreign, scenario 2)
}

type wizardData struct {
	scenario    int    // 1 = client-in-Iran forward, 2 = client-abroad reverse
	transport   string // tcp|tls|ws|wss
	token       string
	serverAddr  string // host:port that the client dials
	bindPort    string // port the server listens on
	sni         string
	host        string
	wsPath      string
	fingerprint string
	obfs        bool
	insecure    bool
	poolSize    int
	forwards    []config.Forward // scenario 1
	services    []svcPair        // scenario 2
	thisMachine string           // "server" | "client"
}

func wizard() error {
	fmt.Print(`
  ======================  SETUP WIZARD  ======================
  Choose your topology:

   [1]  Client in IRAN  ->  forwards chosen local ports to a
        FOREIGN server.  (Iran users hit the Iran box; traffic
        exits through the foreign server. You pick the ports.)

   [2]  Client ABROAD;  IRAN is only the relay. The Iran box
        exposes public ports and tunnels them to the foreign
        client, which reaches the real services.
  ============================================================
`)
	var d wizardData
	switch askDefault("  Topology", "1") {
	case "2":
		d.scenario = 2
	default:
		d.scenario = 1
	}

	// Roles per scenario.
	var iranRole, foreignRole string
	if d.scenario == 1 {
		iranRole, foreignRole = "client", "server"
	} else {
		iranRole, foreignRole = "server", "client"
	}
	fmt.Printf("\n  In this topology: IRAN = %s, FOREIGN = %s\n", strings.ToUpper(iranRole), strings.ToUpper(foreignRole))
	if askYesNo("  Is THIS machine the IRAN side?", true) {
		d.thisMachine = iranRole
	} else {
		d.thisMachine = foreignRole
	}

	// Transport.
	fmt.Print(`
  Transport:
   [tls]  uTLS browser-fingerprinted TLS  (recommended, best stealth)
   [wss]  WebSocket over uTLS TLS         (looks like HTTPS/CDN)
   [ws]   plain WebSocket                 (behind a TLS CDN/proxy)
   [tcp]  raw TCP + optional obfuscation
   [kcp]  UDP + FEC                       (fastest on lossy links; less stealthy)
`)
	d.transport = strings.ToLower(askDefault("  Transport", "tls"))
	switch d.transport {
	case "tls", "wss", "ws", "tcp", "kcp":
	default:
		d.transport = "tls"
	}

	// Shared secret.
	tok := askDefault("  Token (blank = auto-generate)", "")
	if tok == "" {
		tok = genToken()
		fmt.Printf("  -> generated token: %s\n", tok)
	}
	d.token = tok

	// Endpoint.
	d.serverAddr = askRequired("  Public address clients dial (host:port, e.g. 1.2.3.4:443)")
	d.bindPort = portOf(d.serverAddr, "443")

	// Transport-specific camouflage.
	if config.IsTLS(d.transport) {
		d.sni = askDefault("  SNI / camouflage domain", "www.example.com")
		d.insecure = askYesNo("  Server uses a self-signed cert (skip verification)?", true)
		d.fingerprint = askDefault("  TLS fingerprint (chrome|firefox|safari|edge|random)", "chrome")
	}
	if config.IsWS(d.transport) {
		d.wsPath = askDefault("  WebSocket path", "/cando")
		d.host = askDefault("  Host header / camouflage domain", firstNonEmpty(d.sni, "www.example.com"))
	}
	if d.transport == "tcp" {
		d.obfs = askYesNo("  Enable chacha20 obfuscation?", true)
	}

	// Anti-filtering guidance: a self-signed cert on a bare IP with a made-up
	// SNI is the #1 reason a fresh tunnel IP gets burned. Warn loudly and point
	// at the robust options before the user commits to it.
	warnStealth(d)

	d.poolSize = askInt("  Parallel tunnel connections (pool size)", 2)

	// Port mappings.
	if d.scenario == 1 {
		d.forwards = gatherForwards()
	} else {
		d.services = gatherServices()
	}

	// Generate both configs.
	serverTOML := buildServerTOML(d)
	clientTOML := buildClientTOML(d)
	serverPath := "cando1-server.toml"
	clientPath := "cando1-client.toml"
	if err := writeFile(serverPath, serverTOML); err != nil {
		return err
	}
	if err := writeFile(clientPath, clientTOML); err != nil {
		return err
	}

	fmt.Printf(`
  ============================================================
  Wrote:
    %s   -> run this on the %s (%s side)
    %s   -> run this on the %s (%s side)

  Copy each file to the matching machine, then run:
    cando1 -c <file>     (or use menu options [2]/[3])
  ============================================================
`, serverPath, roleMachine(d, "server"), "server",
		clientPath, roleMachine(d, "client"), "client")

	if askYesNo(fmt.Sprintf("  Run the %s config on THIS machine now?", d.thisMachine), false) {
		path := clientPath
		if d.thisMachine == "server" {
			path = serverPath
		}
		cfg, err := config.Load(path)
		if err != nil {
			return err
		}
		fmt.Printf("  starting %s (Ctrl+C to stop)...\n\n", d.thisMachine)
		return runLoaded(cfg)
	}
	return nil
}

func roleMachine(d wizardData, role string) string {
	if d.scenario == 1 {
		if role == "server" {
			return "FOREIGN"
		}
		return "IRAN"
	}
	if role == "server" {
		return "IRAN"
	}
	return "FOREIGN"
}

func gatherForwards() []config.Forward {
	fmt.Print(`
  Define forward tunnels (client-side local port -> server dials target).
  Enter a blank name to finish.
`)
	var out []config.Forward
	i := 1
	for {
		name := strings.TrimSpace(ask(fmt.Sprintf("  [forward %d] name", i)))
		if name == "" {
			break
		}
		local := askDefault("    local listen on the IRAN client (host:port)", "0.0.0.0:1080")
		target := askDefault("    target the FOREIGN server dials (host:port)", "127.0.0.1:1080")
		out = append(out, config.Forward{Name: name, LocalAddr: local, TargetAddr: target})
		i++
	}
	if len(out) == 0 {
		out = append(out, config.Forward{Name: "svc1", LocalAddr: "0.0.0.0:1080", TargetAddr: "127.0.0.1:1080"})
		fmt.Println("  (no entries given; added a default forward svc1)")
	}
	return out
}

func gatherServices() []svcPair {
	fmt.Print(`
  Define reverse services (Iran public port -> foreign client local target).
  Enter a blank name to finish.
`)
	var out []svcPair
	i := 1
	for {
		name := strings.TrimSpace(ask(fmt.Sprintf("  [service %d] name", i)))
		if name == "" {
			break
		}
		pub := askDefault("    public bind on the IRAN server (host:port)", "0.0.0.0:8443")
		local := askDefault("    local target on the FOREIGN client (host:port)", "127.0.0.1:8443")
		out = append(out, svcPair{name: name, publicBind: pub, localAddr: local})
		i++
	}
	if len(out) == 0 {
		out = append(out, svcPair{name: "svc1", publicBind: "0.0.0.0:8443", localAddr: "127.0.0.1:8443"})
		fmt.Println("  (no entries given; added a default service svc1)")
	}
	return out
}

func buildServerTOML(d wizardData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# cando1 SERVER config  (run on the %s machine)\n", roleMachine(d, "server"))
	b.WriteString("[log]\nlevel = \"info\"\n\n")
	b.WriteString("[server]\n")
	fmt.Fprintf(&b, "bind_addr = \"0.0.0.0:%s\"\n", d.bindPort)
	fmt.Fprintf(&b, "transport = \"%s\"\n", d.transport)
	fmt.Fprintf(&b, "token = \"%s\"\n", d.token)
	if d.transport == "tcp" {
		fmt.Fprintf(&b, "obfs = %v\n", d.obfs)
	}
	if config.IsWS(d.transport) {
		fmt.Fprintf(&b, "ws_path = \"%s\"\n", firstNonEmpty(d.wsPath, "/cando"))
		fmt.Fprintf(&b, "host = \"%s\"\n", firstNonEmpty(d.host, d.sni))
	}
	// Scenario 1: the server dials client-requested targets.
	fmt.Fprintf(&b, "allow_forward = %v\n", d.scenario == 1)
	if config.IsTLS(d.transport) {
		b.WriteString("\n[server.tls]\n")
		fmt.Fprintf(&b, "# Leave cert/key empty to auto-generate a self-signed cert for self_name.\n")
		fmt.Fprintf(&b, "self_name = \"%s\"\n", firstNonEmpty(d.sni, "www.example.com"))
		b.WriteString("cert = \"\"\nkey = \"\"\n")
	}
	if d.transport == "kcp" {
		writeKCPTOML(&b, "server.kcp")
	}
	writeMuxTOML(&b, "server.mux")
	if d.scenario == 2 {
		for _, s := range d.services {
			b.WriteString("\n[[server.services]]\n")
			fmt.Fprintf(&b, "name = \"%s\"\n", s.name)
			fmt.Fprintf(&b, "bind_addr = \"%s\"\n", s.publicBind)
		}
	}
	return b.String()
}

func buildClientTOML(d wizardData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# cando1 CLIENT config  (run on the %s machine)\n", roleMachine(d, "client"))
	b.WriteString("[log]\nlevel = \"info\"\n\n")
	b.WriteString("[client]\n")
	fmt.Fprintf(&b, "server_addr = \"%s\"\n", d.serverAddr)
	fmt.Fprintf(&b, "transport = \"%s\"\n", d.transport)
	fmt.Fprintf(&b, "token = \"%s\"\n", d.token)
	fmt.Fprintf(&b, "pool_size = %d\n", d.poolSize)
	if config.IsTLS(d.transport) {
		fmt.Fprintf(&b, "sni = \"%s\"\n", firstNonEmpty(d.sni, "www.example.com"))
		fmt.Fprintf(&b, "fingerprint = \"%s\"\n", firstNonEmpty(d.fingerprint, "chrome"))
		fmt.Fprintf(&b, "insecure = %v\n", d.insecure)
	}
	if config.IsWS(d.transport) {
		fmt.Fprintf(&b, "ws_path = \"%s\"\n", firstNonEmpty(d.wsPath, "/cando"))
		fmt.Fprintf(&b, "host = \"%s\"\n", firstNonEmpty(d.host, d.sni))
	}
	if d.transport == "tcp" {
		fmt.Fprintf(&b, "obfs = %v\n", d.obfs)
	}
	if d.transport == "kcp" {
		writeKCPTOML(&b, "client.kcp")
	}
	writeMuxTOML(&b, "client.mux")
	b.WriteString("\n[client.reconnect]\nmin_millis = 500\nmax_millis = 30000\n")
	if d.scenario == 1 {
		for _, f := range d.forwards {
			b.WriteString("\n[[client.forwards]]\n")
			fmt.Fprintf(&b, "name = \"%s\"\n", f.Name)
			fmt.Fprintf(&b, "local_addr = \"%s\"\n", f.LocalAddr)
			fmt.Fprintf(&b, "target_addr = \"%s\"\n", f.TargetAddr)
		}
	} else {
		for _, s := range d.services {
			b.WriteString("\n[[client.services]]\n")
			fmt.Fprintf(&b, "name = \"%s\"\n", s.name)
			fmt.Fprintf(&b, "local_addr = \"%s\"\n", s.localAddr)
		}
	}
	return b.String()
}

func writeKCPTOML(b *strings.Builder, section string) {
	fmt.Fprintf(b, "\n[%s]\n", section)
	b.WriteString("mode = \"fast3\"\n")
	b.WriteString("fec_data = 10\n")  // must match the other end
	b.WriteString("fec_parity = 3\n") // must match the other end
	b.WriteString("mtu = 1350\n")
	b.WriteString("snd_wnd = 2048\n")
	b.WriteString("rcv_wnd = 2048\n")
	b.WriteString("sock_buf = 16777216\n")
}

func writeMuxTOML(b *strings.Builder, section string) {
	fmt.Fprintf(b, "\n[%s]\n", section)
	b.WriteString("enable = true\n")
	b.WriteString("keepalive_seconds = 10\n")
	b.WriteString("max_stream_buffer = 8388608\n")
	b.WriteString("max_receive_buffer = 33554432\n")
}

func writeFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		if !askYesNo(fmt.Sprintf("  %s exists; overwrite?", path), false) {
			return fmt.Errorf("aborted: %s exists", path)
		}
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// warnStealth prints a prominent anti-filtering warning when the chosen TLS
// setup is the kind that active probing spots easily: a self-signed cert
// (insecure = true) and/or a bare IP endpoint with no real domain. These are
// the setups that get an IP filtered within a day on hostile networks.
func warnStealth(d wizardData) {
	if !config.IsTLS(d.transport) {
		return
	}
	bareIP := net.ParseIP(hostPart(d.serverAddr)) != nil
	if !d.insecure && !bareIP {
		return
	}
	fmt.Print(`
  ------------------------------------------------------------
  ! ANTI-FILTERING WARNING
  A self-signed certificate and/or a bare-IP endpoint is the #1
  reason a tunnel IP gets filtered: an active prober connects,
  sees a certificate that chains to no public CA (or an SNI that
  does not match the IP), and burns the address.

  For a setup that survives, do ONE of:
    1) Front the FOREIGN server behind Cloudflare (ws / wss).
       Hides the origin IP and looks like ordinary HTTPS. Best
       option. See the cloudflare-* examples and the README.
    2) Point a real domain at the server and use a real
       Let's Encrypt certificate via [server.tls] cert/key, and
       set insecure = false on the client.

  Always set the SNI to a real, resolvable domain -- never a
  placeholder like "www.example.com".
  ------------------------------------------------------------
`)
}

// hostPart returns the host portion of a host:port (or the input unchanged).
func hostPart(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func portOf(hostport, def string) string {
	if i := strings.LastIndex(hostport, ":"); i >= 0 && i < len(hostport)-1 {
		return hostport[i+1:]
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

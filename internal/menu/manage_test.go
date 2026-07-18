package menu

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/meran77777/cando1/internal/config"
)

// TestSaveConfigRoundTrip verifies that re-encoding a client config does not
// emit a spurious [server] table (which would make the reloaded file invalid),
// and that edits to the port mappings survive a save + reload.
func TestSaveConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cli.toml")
	toml := `
[client]
server_addr = "1.2.3.4:443"
transport = "tls"
token = "abc123"
sni = "www.example.com"
insecure = true

[[client.forwards]]
name = "socks"
local_addr = "0.0.0.0:1080"
target_addr = "127.0.0.1:1080"
`
	if err := os.WriteFile(src, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Simulate an edit: add a second forward, this one UDP.
	c.Client.Forwards = append(c.Client.Forwards, config.Forward{
		Name: "dns", Protocol: config.ProtoUDP, LocalAddr: "0.0.0.0:5353", TargetAddr: "1.1.1.1:53",
	})

	out := filepath.Join(dir, "cli-saved.toml")
	if err := saveConfig(out, c); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	c2, err := config.Load(out)
	if err != nil {
		t.Fatalf("reload re-encoded config: %v", err)
	}
	if c2.Server != nil {
		t.Fatalf("re-encoded client config grew a [server] table")
	}
	if c2.Client == nil {
		t.Fatal("re-encoded config lost its [client] table")
	}
	if got := len(c2.Client.Forwards); got != 2 {
		t.Fatalf("forwards after round-trip = %d, want 2", got)
	}
	if c2.Client.Forwards[1].Protocol != config.ProtoUDP {
		t.Fatalf("udp protocol not preserved: %q", c2.Client.Forwards[1].Protocol)
	}
	if c2.Client.Token != "abc123" || c2.Client.ServerAddr != "1.2.3.4:443" {
		t.Fatalf("core fields not preserved: %+v", c2.Client)
	}
}

// TestSaveConfigServerRole checks the server side round-trips without growing a
// spurious [client] table and preserves reverse services.
func TestSaveConfigServerRole(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "srv.toml")
	toml := `
[server]
bind_addr = "0.0.0.0:443"
transport = "wss"
token = "tok"
allow_forward = true

[[server.services]]
name = "web"
bind_addr = "0.0.0.0:8443"
`
	if err := os.WriteFile(src, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := filepath.Join(dir, "srv-saved.toml")
	if err := saveConfig(out, c); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	c2, err := config.Load(out)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c2.Client != nil {
		t.Fatal("re-encoded server config grew a [client] table")
	}
	if len(c2.Server.Services) != 1 || c2.Server.Services[0].Name != "web" {
		t.Fatalf("services not preserved: %+v", c2.Server.Services)
	}
	if !c2.Server.AllowForward {
		t.Fatal("allow_forward not preserved")
	}
}

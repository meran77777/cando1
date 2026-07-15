package menu

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/meran77777/cando1/internal/config"
)

// TestWizardBuildersProduceValidConfigs verifies that the TOML the wizard
// generates actually parses and validates for every topology/transport, and
// that roles/sections land where they should.
func TestWizardBuildersProduceValidConfigs(t *testing.T) {
	cases := []struct {
		name string
		d    wizardData
	}{
		{"s1-tls", wizardData{
			scenario: 1, transport: "tls", token: "tok-123456",
			serverAddr: "1.2.3.4:443", bindPort: "443",
			sni: "www.example.com", fingerprint: "chrome", insecure: true, poolSize: 3,
			forwards: []config.Forward{{Name: "openvpn", LocalAddr: "0.0.0.0:1194", TargetAddr: "127.0.0.1:1194"}},
		}},
		{"s2-wss", wizardData{
			scenario: 2, transport: "wss", token: "tok-abcdef",
			serverAddr: "9.9.9.9:443", bindPort: "443",
			sni: "cdn.example.com", host: "cdn.example.com", wsPath: "/cando",
			fingerprint: "chrome", insecure: true, poolSize: 2,
			services: []svcPair{{name: "proxy", publicBind: "0.0.0.0:8388", localAddr: "127.0.0.1:8388"}},
		}},
		{"s1-tcp-obfs", wizardData{
			scenario: 1, transport: "tcp", token: "tok-tcp",
			serverAddr: "1.1.1.1:9000", bindPort: "9000", obfs: true, poolSize: 2,
			forwards: []config.Forward{{Name: "s", LocalAddr: "0.0.0.0:1080", TargetAddr: "127.0.0.1:1080"}},
		}},
		{"s2-ws", wizardData{
			scenario: 2, transport: "ws", token: "tok-ws",
			serverAddr: "2.2.2.2:80", bindPort: "80", host: "site.example", wsPath: "/cando", poolSize: 2,
			services: []svcPair{{name: "web", publicBind: "0.0.0.0:8080", localAddr: "127.0.0.1:8080"}},
		}},
		{"s1-kcp", wizardData{
			scenario: 1, transport: "kcp", token: "tok-kcp",
			serverAddr: "3.3.3.3:443", bindPort: "443", poolSize: 4,
			forwards: []config.Forward{{Name: "s", LocalAddr: "0.0.0.0:1080", TargetAddr: "127.0.0.1:1080"}},
		}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			srvPath := filepath.Join(dir, "server.toml")
			cliPath := filepath.Join(dir, "client.toml")
			if err := os.WriteFile(srvPath, []byte(buildServerTOML(tc.d)), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cliPath, []byte(buildClientTOML(tc.d)), 0o600); err != nil {
				t.Fatal(err)
			}

			srv, err := config.Load(srvPath)
			if err != nil {
				t.Fatalf("server config invalid:\n%s\nerror: %v", buildServerTOML(tc.d), err)
			}
			cli, err := config.Load(cliPath)
			if err != nil {
				t.Fatalf("client config invalid:\n%s\nerror: %v", buildClientTOML(tc.d), err)
			}

			if srv.Server == nil || srv.Client != nil {
				t.Fatal("server file did not produce a server role")
			}
			if cli.Client == nil || cli.Server != nil {
				t.Fatal("client file did not produce a client role")
			}
			if srv.Server.Token != tc.d.token || cli.Client.Token != tc.d.token {
				t.Fatalf("token mismatch: srv=%q cli=%q want=%q", srv.Server.Token, cli.Client.Token, tc.d.token)
			}
			if cli.Client.ServerAddr != tc.d.serverAddr {
				t.Fatalf("client server_addr = %q, want %q", cli.Client.ServerAddr, tc.d.serverAddr)
			}

			switch tc.d.scenario {
			case 1:
				if !srv.Server.AllowForward {
					t.Fatal("scenario 1 server must set allow_forward = true")
				}
				if len(cli.Client.Forwards) != len(tc.d.forwards) {
					t.Fatalf("client forwards = %d, want %d", len(cli.Client.Forwards), len(tc.d.forwards))
				}
			case 2:
				if len(srv.Server.Services) != len(tc.d.services) {
					t.Fatalf("server services = %d, want %d", len(srv.Server.Services), len(tc.d.services))
				}
				if len(cli.Client.Services) != len(tc.d.services) {
					t.Fatalf("client services = %d, want %d", len(cli.Client.Services), len(tc.d.services))
				}
			}
		})
	}
}

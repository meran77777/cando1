package tunnel

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/meran77777/cando1/internal/config"
	"github.com/meran77777/cando1/internal/xlog"
)

// TestTunnelEndToEnd exercises the full stack (auth handshake, obfs/uTLS/WS
// carrier, smux multiplexing, reverse and forward forwarding) over the loopback
// interface for every transport.
func TestTunnelEndToEnd(t *testing.T) {
	xlog.SetLevel("error")

	cases := []struct {
		name      string
		transport string
		obfs      bool
	}{
		{"tcp-obfs", config.TransportTCP, true},
		{"tcp-plain", config.TransportTCP, false},
		{"tls", config.TransportTLS, false},
		{"ws", config.TransportWS, false},
		{"wss", config.TransportWSS, false},
		{"kcp", config.TransportKCP, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runTunnelCase(t, tc.transport, tc.obfs)
		})
	}
}

func runTunnelCase(t *testing.T, transport string, obfs bool) {
	echoAddr := startEcho(t)
	srvAddr := "127.0.0.1:" + freePort(t)
	revAddr := "127.0.0.1:" + freePort(t)
	fwdAddr := "127.0.0.1:" + freePort(t)

	srvCfg := &config.ServerConfig{
		BindAddr:     srvAddr,
		Transport:    transport,
		Token:        "s3cr3t-token",
		Obfs:         obfs,
		WSPath:       "/cando",
		Host:         "example.com",
		TLS:          config.TLSConfig{SelfName: "example.com"},
		Mux:          config.DefaultMux(),
		AllowForward: true,
		Services:     []config.Service{{Name: "echo", BindAddr: revAddr}},
	}
	srv := NewServer(srvCfg)
	go func() { _ = srv.Run() }()
	defer srv.Close()

	cliCfg := &config.ClientConfig{
		ServerAddr:  srvAddr,
		Transport:   transport,
		Token:       "s3cr3t-token",
		Obfs:        obfs,
		WSPath:      "/cando",
		Host:        "example.com",
		SNI:         "example.com",
		Fingerprint: "chrome",
		Insecure:    true,
		PoolSize:    2,
		Mux:         config.DefaultMux(),
		Reconnect:   config.ReconnectConfig{MinMillis: 200, MaxMillis: 2000},
		Services:    []config.ClientTarget{{Name: "echo", LocalAddr: echoAddr}},
		Forwards:    []config.Forward{{Name: "fwd", LocalAddr: fwdAddr, TargetAddr: echoAddr}},
	}
	cli, err := NewClient(cliCfg)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	go func() { _ = cli.Run() }()
	defer cli.Close()

	// Wait for at least one healthy session.
	if !waitUntil(3*time.Second, func() bool { return cli.sessions.Healthy() > 0 }) {
		t.Fatalf("tunnel did not come up (transport=%s)", transport)
	}

	// Reverse path: public server port -> tunnel -> client -> echo.
	assertEcho(t, revAddr, []byte("reverse-hello-"+transport))
	// Forward path: client local port -> tunnel -> server dials target -> echo.
	assertEcho(t, fwdAddr, []byte("forward-hello-"+transport))
}

// startEcho starts a TCP echo server and returns its address.
func startEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}(c)
		}
	}()
	return ln.Addr().String()
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	return port
}

func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func assertEcho(t *testing.T, addr string, payload []byte) {
	t.Helper()
	var conn net.Conn
	var err error
	// The public/forward listener may need a beat after the tunnel is up.
	for i := 0; i < 50; i++ {
		conn, err = net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo mismatch: sent %q got %q", payload, got)
	}
}

// TestLargeTransfer verifies the multiplexer moves a larger payload intact.
func TestLargeTransfer(t *testing.T) {
	xlog.SetLevel("error")
	echoAddr := startEcho(t)
	srvAddr := "127.0.0.1:" + freePort(t)
	revAddr := "127.0.0.1:" + freePort(t)

	srvCfg := &config.ServerConfig{
		BindAddr: srvAddr, Transport: config.TransportTLS, Token: "tok",
		TLS: config.TLSConfig{SelfName: "example.com"}, Mux: config.DefaultMux(),
		Services: []config.Service{{Name: "echo", BindAddr: revAddr}},
	}
	srv := NewServer(srvCfg)
	go func() { _ = srv.Run() }()
	defer srv.Close()

	cliCfg := &config.ClientConfig{
		ServerAddr: srvAddr, Transport: config.TransportTLS, Token: "tok",
		SNI: "example.com", Fingerprint: "chrome", Insecure: true, PoolSize: 1,
		Mux: config.DefaultMux(), Reconnect: config.ReconnectConfig{MinMillis: 200, MaxMillis: 2000},
		Services: []config.ClientTarget{{Name: "echo", LocalAddr: echoAddr}},
	}
	cli, err := NewClient(cliCfg)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	go func() { _ = cli.Run() }()
	defer cli.Close()

	if !waitUntil(3*time.Second, func() bool { return cli.sessions.Healthy() > 0 }) {
		t.Fatalf("tunnel did not come up")
	}

	var conn net.Conn
	for i := 0; i < 50; i++ {
		conn, err = net.DialTimeout("tcp", revAddr, time.Second)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	const size = 4 << 20 // 4 MiB
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	done := make(chan error, 1)
	go func() {
		got := make([]byte, size)
		_, err := io.ReadFull(conn, got)
		if err != nil {
			done <- err
			return
		}
		if !bytes.Equal(got, payload) {
			done <- fmt.Errorf("payload mismatch")
			return
		}
		done <- nil
	}()
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("large transfer: %v", err)
	}
}

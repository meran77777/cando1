package tunnel

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/meran77777/cando1/internal/config"
	"github.com/meran77777/cando1/internal/xlog"
)

// TestUDPForwardAndReverse exercises UDP datagrams across the tunnel in both
// directions: a client-side UDP forward (local port -> tunnel -> server dials
// the UDP target) and a server-side UDP reverse service (public port -> tunnel
// -> client dials the UDP target).
func TestUDPForwardAndReverse(t *testing.T) {
	xlog.SetLevel("error")

	udpEcho := startUDPEcho(t)
	srvAddr := "127.0.0.1:" + freePort(t)
	revAddr := "127.0.0.1:" + freePort(t) // public UDP on the server (reverse)
	fwdAddr := "127.0.0.1:" + freePort(t) // local UDP on the client (forward)

	srvCfg := &config.ServerConfig{
		BindAddr:     srvAddr,
		Transport:    config.TransportTLS,
		Token:        "tok",
		TLS:          config.TLSConfig{SelfName: "example.com"},
		Mux:          config.DefaultMux(),
		AllowForward: true,
		Services:     []config.Service{{Name: "udpecho", Protocol: config.ProtoUDP, BindAddr: revAddr}},
	}
	srv := NewServer(srvCfg)
	go func() { _ = srv.Run() }()
	defer srv.Close()

	cliCfg := &config.ClientConfig{
		ServerAddr:  srvAddr,
		Transport:   config.TransportTLS,
		Token:       "tok",
		SNI:         "example.com",
		Fingerprint: "chrome",
		Insecure:    true,
		PoolSize:    1,
		Mux:         config.DefaultMux(),
		Reconnect:   config.ReconnectConfig{MinMillis: 200, MaxMillis: 2000},
		Services:    []config.ClientTarget{{Name: "udpecho", LocalAddr: udpEcho}},
		Forwards:    []config.Forward{{Name: "fwd", Protocol: config.ProtoUDP, LocalAddr: fwdAddr, TargetAddr: udpEcho}},
	}
	cli, err := NewClient(cliCfg)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	go func() { _ = cli.Run() }()
	defer cli.Close()

	if !waitUntil(3*time.Second, func() bool { return cli.sessions.Healthy() > 0 }) {
		t.Fatal("tunnel did not come up")
	}

	assertUDPEcho(t, fwdAddr, []byte("forward-udp-hello"))
	assertUDPEcho(t, revAddr, []byte("reverse-udp-hello"))
}

// startUDPEcho starts a UDP echo server and returns its address.
func startUDPEcho(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("udp echo listen: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().String()
}

// assertUDPEcho sends payload to a UDP endpoint and expects it echoed back.
// UDP is lossy and the first datagram lazily opens the tunnel flow, so it
// retries a few times with short per-attempt deadlines.
func assertUDPEcho(t *testing.T, addr string, payload []byte) {
	t.Helper()
	uc, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial udp %s: %v", addr, err)
	}
	defer uc.Close()

	got := make([]byte, len(payload)+16)
	for i := 0; i < 50; i++ {
		_ = uc.SetDeadline(time.Now().Add(150 * time.Millisecond))
		if _, err := uc.Write(payload); err != nil {
			t.Fatalf("write udp: %v", err)
		}
		n, err := uc.Read(got)
		if err == nil && bytes.Equal(got[:n], payload) {
			return
		}
	}
	t.Fatalf("no UDP echo from %s after retries", addr)
}

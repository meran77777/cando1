package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/meran77777/cando1/internal/config"
)

// fingerprintID maps a friendly name to a uTLS ClientHello profile. These make
// cando1's TLS ClientHello byte-for-byte resemble a mainstream browser, which
// defeats fingerprint-based DPI classifiers.
func fingerprintID(name string) utls.ClientHelloID {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "firefox":
		return utls.HelloFirefox_Auto
	case "safari", "ios":
		return utls.HelloSafari_Auto
	case "edge":
		return utls.HelloEdge_Auto
	case "randomized", "randomised":
		return utls.HelloRandomizedALPN
	case "random":
		return utls.HelloRandomized
	default:
		return utls.HelloChrome_Auto
	}
}

func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// --- TLS transport (uTLS client / std-lib server) ---

type tlsDialer struct {
	addr        string
	sni         string
	insecure    bool
	fingerprint utls.ClientHelloID
}

func newTLSDialer(cfg *config.ClientConfig) (Dialer, error) {
	sni := cfg.SNI
	if sni == "" {
		sni = hostOnly(cfg.ServerAddr)
	}
	return &tlsDialer{
		addr:        cfg.ServerAddr,
		sni:         sni,
		insecure:    cfg.Insecure,
		fingerprint: fingerprintID(cfg.Fingerprint),
	}, nil
}

func (d *tlsDialer) String() string { return "tls://" + d.addr + " (sni=" + d.sni + ")" }

func (d *tlsDialer) Dial(ctx context.Context) (net.Conn, error) {
	var nd net.Dialer
	raw, err := nd.DialContext(ctx, "tcp", d.addr)
	if err != nil {
		return nil, err
	}
	tuneTCP(raw)
	uconn := utls.UClient(raw, &utls.Config{
		ServerName:         d.sni,
		InsecureSkipVerify: d.insecure,
		NextProtos:         []string{"h2", "http/1.1"},
	}, d.fingerprint)

	_ = uconn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := uconn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, fmt.Errorf("uTLS handshake: %w", err)
	}
	_ = uconn.SetDeadline(time.Time{})
	return uconn, nil
}

func newTLSListener(cfg *config.ServerConfig) (Listener, error) {
	tc, err := serverTLSConfig(cfg.TLS)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", cfg.BindAddr)
	if err != nil {
		return nil, err
	}
	// The TLS handshake runs in a worker goroutine (via asyncListener) so a
	// slow or silent peer cannot stall the accept loop.
	return newAsyncListener(ln, func(raw net.Conn) (net.Conn, error) {
		tuneTCP(raw)
		tconn := tls.Server(raw, tc)
		_ = tconn.SetDeadline(time.Now().Add(15 * time.Second))
		if err := tconn.Handshake(); err != nil {
			return nil, fmt.Errorf("tls handshake: %w", err)
		}
		_ = tconn.SetDeadline(time.Time{})
		return tconn, nil
	}), nil
}

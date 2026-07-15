package transport

import (
	"context"
	"net"

	"github.com/meran77777/cando1/internal/config"
)

// --- plain TCP (optionally obfuscated) ---

type tcpDialer struct {
	addr  string
	token string
	obfs  bool
}

func (d *tcpDialer) String() string { return "tcp://" + d.addr }

func (d *tcpDialer) Dial(ctx context.Context) (net.Conn, error) {
	var nd net.Dialer
	raw, err := nd.DialContext(ctx, "tcp", d.addr)
	if err != nil {
		return nil, err
	}
	tuneTCP(raw)
	if d.obfs {
		oc, err := obfsClient(raw, d.token)
		if err != nil {
			raw.Close()
			return nil, err
		}
		return oc, nil
	}
	return raw, nil
}

func newTCPListener(cfg *config.ServerConfig) (Listener, error) {
	ln, err := net.Listen("tcp", cfg.BindAddr)
	if err != nil {
		return nil, err
	}
	token := cfg.Token
	obfs := cfg.Obfs
	// The obfs nonce read runs in a worker goroutine (via asyncListener) so a
	// slow or silent peer cannot stall the accept loop.
	return newAsyncListener(ln, func(raw net.Conn) (net.Conn, error) {
		tuneTCP(raw)
		if obfs {
			return obfsServer(raw, token)
		}
		return raw, nil
	}), nil
}

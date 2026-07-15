// Package transport provides the pluggable carrier layer that cando1 runs its
// multiplexed tunnel over. Every transport exposes the same Dialer/Listener
// pair and yields a plain net.Conn once the carrier (TLS / WebSocket / raw TCP
// with optional obfuscation) is fully established, so the higher layers stay
// carrier-agnostic.
package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/meran77777/cando1/internal/config"
)

// bufPool holds reusable 32 KiB scratch buffers shared by copy loops and the
// obfuscation writer.
var bufPool = sync.Pool{New: func() any { return make([]byte, 32*1024) }}

// GetBuffer / PutBuffer expose the shared pool to other packages.
func GetBuffer() []byte  { return bufPool.Get().([]byte) }
func PutBuffer(b []byte) { bufPool.Put(b) } //nolint:staticcheck // slice header by value is fine here

// Dialer establishes a fresh carrier connection to the server.
type Dialer interface {
	Dial(ctx context.Context) (net.Conn, error)
	String() string
}

// Listener accepts fully-established carrier connections.
type Listener interface {
	Accept() (net.Conn, error)
	Close() error
	Addr() net.Addr
}

// TuneTCP enables low-latency options on a raw TCP connection (exported for
// use on the plain sockets accepted/dialed by the tunnel layer).
func TuneTCP(c net.Conn) { tuneTCP(c) }

// tuneTCP enables low-latency options on the underlying TCP socket.
func tuneTCP(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true) // disable Nagle: critical for ping / interactivity
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
}

// NewClientDialer builds the dialer described by the client config.
func NewClientDialer(cfg *config.ClientConfig) (Dialer, error) {
	switch cfg.Transport {
	case config.TransportTCP:
		return &tcpDialer{addr: cfg.ServerAddr, token: cfg.Token, obfs: cfg.Obfs}, nil
	case config.TransportTLS:
		return newTLSDialer(cfg)
	case config.TransportWS, config.TransportWSS:
		return newWSDialer(cfg)
	case config.TransportKCP:
		return newKCPDialer(cfg)
	default:
		return nil, fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
}

// NewServerListener builds the listener described by the server config.
func NewServerListener(cfg *config.ServerConfig) (Listener, error) {
	switch cfg.Transport {
	case config.TransportTCP:
		return newTCPListener(cfg)
	case config.TransportTLS:
		return newTLSListener(cfg)
	case config.TransportWS, config.TransportWSS:
		return newWSListener(cfg)
	case config.TransportKCP:
		return newKCPListener(cfg)
	default:
		return nil, fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
}

// maxHandshakeWorkers bounds the number of carrier handshakes running
// concurrently, so a flood of slow/hostile probes cannot exhaust resources.
const maxHandshakeWorkers = 512

// asyncListener wraps a raw net.Listener and performs the per-connection
// carrier handshake (TLS, obfs, ...) in bounded worker goroutines rather than
// on the accept path. This prevents a single slow or silent peer from stalling
// acceptance of every other client — critical for a server that must survive
// active probing.
type asyncListener struct {
	ln        net.Listener
	setup     func(net.Conn) (net.Conn, error)
	connCh    chan net.Conn
	done      chan struct{}
	sem       chan struct{}
	closeOnce sync.Once
}

func newAsyncListener(ln net.Listener, setup func(net.Conn) (net.Conn, error)) *asyncListener {
	l := &asyncListener{
		ln:     ln,
		setup:  setup,
		connCh: make(chan net.Conn, 256),
		done:   make(chan struct{}),
		sem:    make(chan struct{}, maxHandshakeWorkers),
	}
	go l.acceptLoop()
	return l
}

func (l *asyncListener) acceptLoop() {
	for {
		raw, err := l.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Transient error (e.g. fd exhaustion): back off briefly instead
			// of hot-spinning the accept loop.
			select {
			case <-l.done:
				return
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		select {
		case l.sem <- struct{}{}:
		case <-l.done:
			_ = raw.Close()
			return
		}
		go func(c net.Conn) {
			defer func() { <-l.sem }()
			conn, err := l.setup(c)
			if err != nil {
				_ = c.Close()
				return
			}
			select {
			case l.connCh <- conn:
			case <-l.done:
				_ = conn.Close()
			}
		}(raw)
	}
}

func (l *asyncListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connCh:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *asyncListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
		_ = l.ln.Close()
	})
	return nil
}

func (l *asyncListener) Addr() net.Addr { return l.ln.Addr() }

// serverTLSConfig loads or generates the certificate the server presents.
func serverTLSConfig(t config.TLSConfig) (*tls.Config, error) {
	cert, err := loadOrCreateCert(t)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}

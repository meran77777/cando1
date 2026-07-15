package transport

import (
	"context"
	"crypto/sha256"
	"net"
	"sync"

	kcp "github.com/xtaci/kcp-go/v5"

	"github.com/meran77777/cando1/internal/config"
	"github.com/meran77777/cando1/internal/xlog"
)

// The KCP transport carries the tunnel over UDP with Reed-Solomon forward error
// correction and an aggressive, tunable ARQ. This avoids the TCP-over-TCP
// meltdown that cripples plain-TCP tunnels on lossy international links: a lost
// packet is reconstructed from FEC parity instead of triggering a full TCP
// retransmit/backoff on both the inner and outer connections. The UDP payload
// is always AES-encrypted with a key derived from the token, which both hides
// the traffic and acts as a pre-authentication gate (packets that fail to
// decrypt never create a session). This is the "speed mode": fastest on lossy
// links, but less protocol-camouflaged than tls/wss (use those where UDP is
// blocked).

func kcpBlock(token string) (kcp.BlockCrypt, error) {
	sum := sha256.Sum256([]byte("cando1-kcp\x00" + token))
	return kcp.NewAESBlockCrypt(sum[:])
}

// kcpModeParams maps a friendly mode name to KCP's (nodelay, interval, resend,
// nc) tuple. "fast3" is the most aggressive (lowest latency, congestion control
// disabled for maximum throughput on a dedicated link).
func kcpModeParams(mode string) (nodelay, interval, resend, nc int) {
	switch mode {
	case "normal":
		return 0, 40, 0, 0
	case "fast":
		return 0, 30, 2, 1
	case "fast2":
		return 1, 20, 2, 1
	default: // fast3
		return 1, 10, 2, 1
	}
}

// normKCP fills any zero field with a sensible default and clamps the MTU to a
// range kcp-go accepts (an out-of-range MTU is silently ignored by
// SetMtu, which would desync the two ends). FEC data/parity are defaulted as a
// coupled pair so a partially-specified config never disables or mismatches FEC.
func normKCP(c config.KCPConfig) config.KCPConfig {
	d := config.DefaultKCP()
	if c.Mode == "" {
		c.Mode = d.Mode
	}
	if c.MTU <= 0 {
		c.MTU = d.MTU
	}
	if c.MTU > 1400 {
		c.MTU = 1400
	}
	if c.MTU < 512 {
		c.MTU = 512
	}
	if c.SndWnd <= 0 {
		c.SndWnd = d.SndWnd
	}
	if c.RcvWnd <= 0 {
		c.RcvWnd = d.RcvWnd
	}
	if c.SockBuf <= 0 {
		c.SockBuf = d.SockBuf
	}
	if c.FECData <= 0 || c.FECParity <= 0 {
		c.FECData, c.FECParity = d.FECData, d.FECParity
	}
	return c
}

func tuneKCPSession(s *kcp.UDPSession, c config.KCPConfig) {
	s.SetStreamMode(true)
	s.SetWriteDelay(false)
	nd, iv, rs, nc := kcpModeParams(c.Mode)
	s.SetNoDelay(nd, iv, rs, nc)
	s.SetWindowSize(c.SndWnd, c.RcvWnd)
	s.SetMtu(c.MTU)
	s.SetACKNoDelay(true)
	if c.SockBuf > 0 {
		_ = s.SetReadBuffer(c.SockBuf)
		_ = s.SetWriteBuffer(c.SockBuf)
	}
}

// --- KCP dialer ---

type kcpDialer struct {
	addr  string
	block kcp.BlockCrypt
	cfg   config.KCPConfig
}

func newKCPDialer(c *config.ClientConfig) (Dialer, error) {
	block, err := kcpBlock(c.Token)
	if err != nil {
		return nil, err
	}
	return &kcpDialer{addr: c.ServerAddr, block: block, cfg: normKCP(c.KCP)}, nil
}

func (d *kcpDialer) String() string { return "kcp://" + d.addr }

func (d *kcpDialer) Dial(ctx context.Context) (net.Conn, error) {
	type result struct {
		s   *kcp.UDPSession
		err error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := kcp.DialWithOptions(d.addr, d.block, d.cfg.FECData, d.cfg.FECParity)
		ch <- result{s, err}
	}()
	select {
	case <-ctx.Done():
		// Abandon the dial; close the session if it arrives late.
		go func() {
			if r := <-ch; r.s != nil {
				_ = r.s.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		tuneKCPSession(r.s, d.cfg)
		return r.s, nil
	}
}

// --- KCP listener ---

type kcpListener struct {
	ln        *kcp.Listener
	cfg       config.KCPConfig
	connCh    chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

func newKCPListener(c *config.ServerConfig) (Listener, error) {
	block, err := kcpBlock(c.Token)
	if err != nil {
		return nil, err
	}
	cfg := normKCP(c.KCP)
	ln, err := kcp.ListenWithOptions(c.BindAddr, block, cfg.FECData, cfg.FECParity)
	if err != nil {
		return nil, err
	}
	if cfg.SockBuf > 0 {
		_ = ln.SetReadBuffer(cfg.SockBuf)
		_ = ln.SetWriteBuffer(cfg.SockBuf)
	}
	l := &kcpListener{
		ln:     ln,
		cfg:    cfg,
		connCh: make(chan net.Conn, 128),
		done:   make(chan struct{}),
	}
	go l.acceptLoop()
	return l, nil
}

func (l *kcpListener) acceptLoop() {
	for {
		sess, err := l.ln.AcceptKCP()
		if err != nil {
			select {
			case <-l.done:
				return // normal shutdown
			default:
			}
			// kcp-go latches socket read errors permanently: once AcceptKCP
			// errors it will keep erroring, so the listener is dead. Surface it
			// and shut down cleanly instead of busy-spinning or hanging Accept.
			xlog.Errorf("kcp listener stopped accepting: %v", err)
			l.shutdown()
			return
		}
		tuneKCPSession(sess, l.cfg)
		select {
		case l.connCh <- sess:
		case <-l.done:
			_ = sess.Close()
			return
		}
	}
}

func (l *kcpListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connCh:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *kcpListener) shutdown() {
	l.closeOnce.Do(func() {
		close(l.done)
		_ = l.ln.Close()
		// Close any sessions accepted but not yet consumed, so they do not leak
		// inside the kcp listener's session table.
		for {
			select {
			case c := <-l.connCh:
				_ = c.Close()
			default:
				return
			}
		}
	})
}

func (l *kcpListener) Close() error {
	l.shutdown()
	return nil
}

func (l *kcpListener) Addr() net.Addr { return l.ln.Addr() }

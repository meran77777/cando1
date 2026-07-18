package tunnel

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/xtaci/smux"

	"github.com/meran77777/cando1/internal/config"
	"github.com/meran77777/cando1/internal/mux"
	"github.com/meran77777/cando1/internal/protocol"
	"github.com/meran77777/cando1/internal/transport"
	"github.com/meran77777/cando1/internal/xlog"
)

// Client dials a server, maintains a pool of tunnel sessions with automatic
// reconnect, serves reverse streams into local targets and forwards local
// listeners out through the tunnel.
type Client struct {
	cfg        *config.ClientConfig
	dialer     transport.Dialer
	sessions   *mux.SessionSet
	serviceMap map[string]string // reverse service name -> local target addr

	mu          sync.Mutex
	forwardLns  []net.Listener
	packetConns []net.PacketConn // UDP forward listeners
	done        chan struct{}
	closeOnce   sync.Once
}

// errNoTunnel signals that no live tunnel session was available in time.
var errNoTunnel = errors.New("no tunnel session available")

// NewClient builds a Client from config.
func NewClient(cfg *config.ClientConfig) (*Client, error) {
	d, err := transport.NewClientDialer(cfg)
	if err != nil {
		return nil, err
	}
	sm := make(map[string]string, len(cfg.Services))
	for _, svc := range cfg.Services {
		sm[svc.Name] = svc.LocalAddr
	}
	return &Client{
		cfg:        cfg,
		dialer:     d,
		sessions:   mux.NewSessionSet(),
		serviceMap: sm,
		done:       make(chan struct{}),
	}, nil
}

// Run starts forwarders and the session pool, blocking until Close.
func (c *Client) Run() error {
	for _, f := range c.cfg.Forwards {
		if err := c.serveForward(f); err != nil {
			return err
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < c.cfg.PoolSize; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c.maintainSession(idx)
		}(i)
	}
	xlog.Infof("client connecting to %s (%s), pool=%d, reverse=%d, forwards=%d",
		c.cfg.ServerAddr, c.cfg.Transport, c.cfg.PoolSize, len(c.cfg.Services), len(c.cfg.Forwards))
	wg.Wait()
	return nil
}

func (c *Client) maintainSession(idx int) {
	backoff := time.Duration(c.cfg.Reconnect.MinMillis) * time.Millisecond
	maxBackoff := time.Duration(c.cfg.Reconnect.MaxMillis) * time.Millisecond
	for {
		select {
		case <-c.done:
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		conn, err := c.dialer.Dial(ctx)
		cancel()
		if err != nil {
			xlog.Warnf("[conn %d] dial failed: %v (retry in %s)", idx, err, backoff)
			if !c.sleep(backoff) {
				return
			}
			backoff = grow(backoff, maxBackoff)
			continue
		}
		if err := protocol.ClientHandshake(conn, c.cfg.Token); err != nil {
			xlog.Errorf("[conn %d] handshake failed: %v", idx, err)
			_ = conn.Close()
			if !c.sleep(backoff) {
				return
			}
			backoff = grow(backoff, maxBackoff)
			continue
		}
		sess, err := mux.Client(conn, c.cfg.Mux)
		if err != nil {
			xlog.Errorf("[conn %d] mux failed: %v", idx, err)
			_ = conn.Close()
			if !c.sleep(backoff) {
				return
			}
			backoff = grow(backoff, maxBackoff)
			continue
		}

		id := c.sessions.Add(sess)
		xlog.Infof("[conn %d] tunnel established -> %s (healthy=%d)", idx, c.dialer.String(), c.sessions.Healthy())
		upSince := time.Now()

		c.acceptReverse(sess)

		c.sessions.Remove(id)
		_ = sess.Close()
		select {
		case <-c.done:
			return
		default:
		}

		// Only treat the connection as "good" (reset backoff) if it stayed up
		// long enough. A session that connects then instantly drops (flapping
		// or MITM server) is rate-limited by the growing backoff instead of
		// hammering the server in a tight reconnect loop.
		minStable := 3 * maxBackoff / 10
		if minStable < 5*time.Second {
			minStable = 5 * time.Second
		}
		if time.Since(upSince) >= minStable {
			backoff = time.Duration(c.cfg.Reconnect.MinMillis) * time.Millisecond
			xlog.Warnf("[conn %d] tunnel lost; reconnecting", idx)
		} else {
			xlog.Warnf("[conn %d] tunnel dropped quickly; backing off %s", idx, backoff)
			if !c.sleep(backoff) {
				return
			}
			backoff = grow(backoff, maxBackoff)
		}
	}
}

func (c *Client) acceptReverse(sess *smux.Session) {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		go c.handleReverseStream(stream)
	}
}

func (c *Client) handleReverseStream(stream *smux.Stream) {
	kind, name, err := protocol.ReadStreamHeader(stream)
	if err != nil {
		_ = stream.Close()
		return
	}
	if kind != protocol.KindReverse && kind != protocol.KindReverseUDP {
		_ = stream.Close()
		return
	}
	local, ok := c.serviceMap[name]
	if !ok {
		xlog.Warnf("reverse service %q has no local target on this client", name)
		_ = stream.Close()
		return
	}
	if kind == protocol.KindReverseUDP {
		uc, err := net.DialTimeout("udp", local, tcpDialWait)
		if err != nil {
			xlog.Warnf("reverse %q (udp): dial local %s failed: %v", name, local, err)
			_ = stream.Close()
			return
		}
		xlog.Debugf("reverse udp stream %q -> %s", name, local)
		bridgeUDP(stream, uc)
		return
	}
	dst, err := net.DialTimeout("tcp", local, tcpDialWait)
	if err != nil {
		xlog.Warnf("reverse %q: dial local %s failed: %v", name, local, err)
		_ = stream.Close()
		return
	}
	transport.TuneTCP(dst)
	xlog.Debugf("reverse stream %q -> %s", name, local)
	pipe(stream, dst)
}

func (c *Client) serveForward(f config.Forward) error {
	if config.IsUDP(f.Protocol) {
		return c.serveForwardUDP(f)
	}
	ln, err := net.Listen("tcp", f.LocalAddr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.forwardLns = append(c.forwardLns, ln)
	c.mu.Unlock()
	xlog.Infof("forward %q: %s -> (tunnel) -> %s", f.Name, f.LocalAddr, f.TargetAddr)
	go func() {
		for {
			user, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				// Back off on transient errors (e.g. fd exhaustion).
				select {
				case <-c.done:
					return
				case <-time.After(20 * time.Millisecond):
				}
				continue
			}
			transport.TuneTCP(user)
			go c.dispatchForward(f, user)
		}
	}()
	return nil
}

// serveForwardUDP binds a local UDP socket and tunnels each source address's
// datagrams to the server, which reaches TargetAddr over UDP.
func (c *Client) serveForwardUDP(f config.Forward) error {
	uaddr, err := net.ResolveUDPAddr("udp", f.LocalAddr)
	if err != nil {
		return err
	}
	pc, err := net.ListenUDP("udp", uaddr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.packetConns = append(c.packetConns, pc)
	c.mu.Unlock()
	xlog.Infof("forward %q (udp): %s -> (tunnel) -> %s", f.Name, f.LocalAddr, f.TargetAddr)
	target := f.TargetAddr
	go serveUDPListener(pc, func() (*smux.Stream, error) {
		sess := c.waitSession(udpSessionWait)
		if sess == nil {
			return nil, errNoTunnel
		}
		stream, err := sess.OpenStream()
		if err != nil {
			return nil, err
		}
		_ = stream.SetWriteDeadline(time.Now().Add(udpHeaderWait))
		if err := protocol.WriteStreamHeader(stream, protocol.KindForwardUDP, target); err != nil {
			_ = stream.Close()
			return nil, err
		}
		_ = stream.SetWriteDeadline(time.Time{})
		return stream, nil
	})
	return nil
}

func (c *Client) dispatchForward(f config.Forward, user net.Conn) {
	sess := c.waitSession(5 * time.Second)
	if sess == nil {
		xlog.Warnf("forward %q: no tunnel available, dropping connection", f.Name)
		_ = user.Close()
		return
	}
	stream, err := sess.OpenStream()
	if err != nil {
		_ = user.Close()
		return
	}
	_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := protocol.WriteStreamHeader(stream, protocol.KindForward, f.TargetAddr); err != nil {
		_ = stream.Close()
		_ = user.Close()
		return
	}
	_ = stream.SetWriteDeadline(time.Time{})
	pipe(stream, user)
}

func (c *Client) waitSession(timeout time.Duration) *smux.Session {
	deadline := time.Now().Add(timeout)
	for {
		if sess := c.sessions.Pick(); sess != nil {
			return sess
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-c.done:
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (c *Client) sleep(d time.Duration) bool {
	select {
	case <-c.done:
		return false
	case <-time.After(d):
		return true
	}
}

func grow(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

// Close shuts the client down.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.mu.Lock()
		for _, ln := range c.forwardLns {
			_ = ln.Close()
		}
		for _, pc := range c.packetConns {
			_ = pc.Close()
		}
		c.mu.Unlock()
		c.sessions.CloseAll()
	})
	return nil
}

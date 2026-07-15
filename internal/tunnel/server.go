package tunnel

import (
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

// maxInflightHandshakes bounds how many client connections may be in the
// (pre-session) authentication handshake at once, so a flood of half-open peers
// that connect and then stay silent cannot exhaust goroutines/memory. It caps
// only the short handshake phase — established sessions do not hold a slot.
const maxInflightHandshakes = 1024

// Server is the tunnel anchor. It accepts authenticated client sessions,
// exposes reverse services on public ports, and services client-initiated
// forward streams.
type Server struct {
	cfg       *config.ServerConfig
	auth      *protocol.ServerAuthenticator
	sessions  *mux.SessionSet
	whitelist map[string]bool
	hsSem     chan struct{}

	mu         sync.Mutex
	ln         transport.Listener
	reverseLns []net.Listener
	done       chan struct{}
	closeOnce  sync.Once
}

// NewServer builds a Server from config.
func NewServer(cfg *config.ServerConfig) *Server {
	wl := make(map[string]bool, len(cfg.ForwardWhitelist))
	for _, t := range cfg.ForwardWhitelist {
		wl[t] = true
	}
	return &Server{
		cfg:       cfg,
		auth:      protocol.NewServerAuthenticator(cfg.Token),
		sessions:  mux.NewSessionSet(),
		whitelist: wl,
		hsSem:     make(chan struct{}, maxInflightHandshakes),
		done:      make(chan struct{}),
	}
}

// Run starts the server and blocks until Close is called or a fatal error occurs.
func (s *Server) Run() error {
	ln, err := transport.NewServerListener(s.cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	for _, svc := range s.cfg.Services {
		go s.serveReverse(svc)
	}

	xlog.Infof("server listening on %s (%s), reverse services=%d", s.cfg.BindAddr, s.cfg.Transport, len(s.cfg.Services))
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			// Per-connection carrier failure (e.g. a probe or bad TLS handshake).
			xlog.Debugf("accept: %v", err)
			continue
		}
		// Acquire a handshake slot before spawning, so half-open peers cannot
		// create unbounded goroutines. Released once the session is established.
		select {
		case s.hsSem <- struct{}{}:
		case <-s.done:
			_ = conn.Close()
			return nil
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	slotReleased := false
	releaseSlot := func() {
		if !slotReleased {
			slotReleased = true
			<-s.hsSem
		}
	}
	defer releaseSlot()

	remote := conn.RemoteAddr()
	if err := s.auth.Handshake(conn); err != nil {
		// Stay silent to unauthenticated peers (anti active-probing); just drop.
		xlog.Debugf("auth from %s rejected: %v", remote, err)
		_ = conn.Close()
		return
	}
	sess, err := mux.Server(conn, s.cfg.Mux)
	if err != nil {
		xlog.Warnf("mux from %s failed: %v", remote, err)
		_ = conn.Close()
		return
	}
	// Session established: free the handshake slot so long-lived sessions do not
	// count against the in-flight-handshake bound.
	releaseSlot()
	id := s.sessions.Add(sess)
	xlog.Infof("client %s authenticated (healthy sessions=%d)", remote, s.sessions.Healthy())
	defer func() {
		s.sessions.Remove(id)
		_ = sess.Close()
		xlog.Infof("client %s disconnected (healthy sessions=%d)", remote, s.sessions.Healthy())
	}()

	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		go s.handleForwardStream(stream)
	}
}

// handleForwardStream services a client-initiated forward stream: the payload
// is the target address the server must dial.
func (s *Server) handleForwardStream(stream *smux.Stream) {
	kind, target, err := protocol.ReadStreamHeader(stream)
	if err != nil {
		_ = stream.Close()
		return
	}
	if kind != protocol.KindForward {
		xlog.Warnf("unexpected stream kind %d from client", kind)
		_ = stream.Close()
		return
	}
	if !s.cfg.AllowForward {
		xlog.Warnf("forward to %s denied (server.allow_forward=false)", target)
		_ = stream.Close()
		return
	}
	if len(s.whitelist) > 0 {
		// Strict mode: only explicitly allow-listed targets.
		if !s.whitelist[target] {
			xlog.Warnf("forward to %s denied (not in forward_whitelist)", target)
			_ = stream.Close()
			return
		}
	} else if blockedForwardTarget(target) {
		// Default mode: loopback/private targets are allowed (the intended use
		// is reaching a service on the foreign server), but link-local,
		// multicast, unspecified and cloud-metadata addresses are refused to
		// blunt SSRF via a leaked token. Use forward_whitelist to lock it down.
		xlog.Warnf("forward to %s denied (blocked address range)", target)
		_ = stream.Close()
		return
	}
	dst, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		xlog.Warnf("forward dial %s failed: %v", target, err)
		_ = stream.Close()
		return
	}
	transport.TuneTCP(dst)
	xlog.Debugf("forward stream -> %s", target)
	pipe(stream, dst)
}

func (s *Server) serveReverse(svc config.Service) {
	ln, err := net.Listen("tcp", svc.BindAddr)
	if err != nil {
		xlog.Errorf("reverse service %q cannot listen on %s: %v", svc.Name, svc.BindAddr, err)
		return
	}
	s.trackReverse(ln)
	xlog.Infof("reverse service %q exposed on %s", svc.Name, svc.BindAddr)
	for {
		user, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Back off on transient errors (e.g. fd exhaustion) so we do not
			// hot-spin a CPU core while the condition persists.
			select {
			case <-s.done:
				return
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		transport.TuneTCP(user)
		go s.dispatchReverse(svc.Name, user)
	}
}

// blockedForwardTarget reports whether target is an address range cando1
// refuses to forward to by default (SSRF hardening).
func blockedForwardTarget(target string) bool {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return true // malformed target: refuse
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // hostname: allowed (resolved at dial time)
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	return false
}

func (s *Server) dispatchReverse(name string, user net.Conn) {
	sess := s.waitSession(5 * time.Second)
	if sess == nil {
		xlog.Warnf("reverse %q: no client tunnel available, dropping connection from %s", name, user.RemoteAddr())
		_ = user.Close()
		return
	}
	stream, err := sess.OpenStream()
	if err != nil {
		_ = user.Close()
		return
	}
	// Bound the header write so a peer with a wedged flow-control window cannot
	// pin this goroutine and the user connection indefinitely.
	_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := protocol.WriteStreamHeader(stream, protocol.KindReverse, name); err != nil {
		_ = stream.Close()
		_ = user.Close()
		return
	}
	_ = stream.SetWriteDeadline(time.Time{})
	pipe(stream, user)
}

// waitSession returns a live session, polling until one appears or timeout.
func (s *Server) waitSession(timeout time.Duration) *smux.Session {
	deadline := time.Now().Add(timeout)
	for {
		if sess := s.sessions.Pick(); sess != nil {
			return sess
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-s.done:
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Server) trackReverse(ln net.Listener) {
	s.mu.Lock()
	s.reverseLns = append(s.reverseLns, ln)
	s.mu.Unlock()
}

// Close shuts the server down.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.mu.Lock()
		if s.ln != nil {
			_ = s.ln.Close()
		}
		for _, ln := range s.reverseLns {
			_ = ln.Close()
		}
		s.mu.Unlock()
		s.sessions.CloseAll()
	})
	return nil
}

// Package mux wraps the smux multiplexer: it turns a single carrier connection
// into many cheap, independent streams (so a new user connection costs one
// stream open instead of a full TCP+TLS handshake — the key to low latency),
// and manages a set of parallel sessions to spread load and avoid
// head-of-line blocking.
package mux

import (
	"net"
	"sync"
	"time"

	"github.com/xtaci/smux"

	"github.com/meran77777/cando1/internal/config"
)

// Default buffer sizes tuned for high bandwidth-delay-product links (e.g.
// Iran<->EU). Left-at-zero fields fall back to these rather than to smux's tiny
// 64 KiB stream buffer, which would otherwise silently cap single-stream
// throughput to ~64 KiB per RTT.
const (
	defaultKeepAliveSeconds = 10
	defaultStreamBuffer     = 8 * 1024 * 1024
	defaultReceiveBuffer    = 32 * 1024 * 1024
)

// Config builds a tuned smux configuration from the user's MuxConfig, applying
// good defaults for any field left at zero (even in a partially-specified
// section).
func Config(m config.MuxConfig) *smux.Config {
	c := smux.DefaultConfig()
	c.Version = 2

	keepAlive := m.KeepAliveSeconds
	if keepAlive <= 0 {
		keepAlive = defaultKeepAliveSeconds
	}
	c.KeepAliveInterval = time.Duration(keepAlive) * time.Second
	c.KeepAliveTimeout = 3 * c.KeepAliveInterval

	if m.MaxReceiveBuffer > 0 {
		c.MaxReceiveBuffer = m.MaxReceiveBuffer
	} else {
		c.MaxReceiveBuffer = defaultReceiveBuffer
	}
	if m.MaxStreamBuffer > 0 {
		c.MaxStreamBuffer = m.MaxStreamBuffer
	} else {
		c.MaxStreamBuffer = defaultStreamBuffer
	}
	// smux requires MaxStreamBuffer <= MaxReceiveBuffer.
	if c.MaxStreamBuffer > c.MaxReceiveBuffer {
		c.MaxStreamBuffer = c.MaxReceiveBuffer
	}
	return c
}

// Client wraps conn as the smux client end.
func Client(conn net.Conn, m config.MuxConfig) (*smux.Session, error) {
	return smux.Client(conn, Config(m))
}

// Server wraps conn as the smux server end.
func Server(conn net.Conn, m config.MuxConfig) (*smux.Session, error) {
	return smux.Server(conn, Config(m))
}

// SessionSet is a thread-safe collection of live smux sessions with
// least-loaded stream placement.
type SessionSet struct {
	mu       sync.RWMutex
	sessions map[uint64]*smux.Session
	nextID   uint64
}

// NewSessionSet creates an empty set.
func NewSessionSet() *SessionSet {
	return &SessionSet{sessions: make(map[uint64]*smux.Session)}
}

// Add registers a session and returns its handle id.
func (s *SessionSet) Add(sess *smux.Session) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	s.sessions[id] = sess
	return id
}

// Remove drops a session by id (does not close it).
func (s *SessionSet) Remove(id uint64) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// Pick returns the healthy session currently carrying the fewest streams, or
// nil if none is available.
func (s *SessionSet) Pick() *smux.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *smux.Session
	bestN := int(^uint(0) >> 1)
	for _, sess := range s.sessions {
		if sess.IsClosed() {
			continue
		}
		if n := sess.NumStreams(); n < bestN {
			bestN = n
			best = sess
		}
	}
	return best
}

// Count returns the number of registered (healthy or not) sessions.
func (s *SessionSet) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// Healthy returns the number of open sessions.
func (s *SessionSet) Healthy() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, sess := range s.sessions {
		if !sess.IsClosed() {
			n++
		}
	}
	return n
}

// CloseAll closes and forgets every session.
func (s *SessionSet) CloseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		_ = sess.Close()
		delete(s.sessions, id)
	}
}

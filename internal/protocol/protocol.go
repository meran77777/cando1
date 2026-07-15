// Package protocol implements cando1's small framing layer: a mutual,
// client-speaks-first authentication handshake performed over an already
// established (and usually already encrypted) transport connection, and the
// per-stream metadata header that tells the peer what a freshly opened
// multiplexed stream is for.
//
// Design goals of the handshake:
//   - No cleartext magic or version bytes. Every byte on the wire is either a
//     random nonce or an HMAC tag, so the whole exchange is indistinguishable
//     from random noise to a passive observer.
//   - The client speaks first and the server stays completely silent until the
//     client has proven knowledge of the token. An active prober that opens a
//     connection therefore receives nothing back — the port behaves like a
//     black hole, defeating response-based active probing.
//   - Mutual authentication: the server also proves knowledge of the token, so
//     a man-in-the-middle cannot impersonate the server even when the client
//     runs with certificate verification disabled (self-signed setups).
//   - Replay resistance: the client authenticator is bound to a coarse
//     timestamp and a per-connection nonce, and the server rejects reused
//     nonces within the acceptance window.
package protocol

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Stream kinds.
const (
	// KindReverse: opened by the server towards the client. Payload is the
	// service name; the client maps it to a configured local target.
	KindReverse byte = 1
	// KindForward: opened by the client towards the server. Payload is the
	// target host:port the server must dial.
	KindForward byte = 2
)

const (
	nonceLen       = 32
	macLen         = 32
	tsLen          = 8
	clientHelloLen = tsLen + nonceLen + macLen // 72
	serverHelloLen = nonceLen + macLen         // 64
	handshakeWait  = 15 * time.Second
	maxHeaderLen   = 512

	// clockSkew is the maximum accepted difference between the client's
	// timestamp and the server's clock.
	clockSkew = 45 * time.Second
	// replayTTL is how long a client nonce is remembered to reject replays.
	replayTTL = 2 * clockSkew
)

var (
	labelClient = []byte("cando1/client-hello/v1")
	labelServer = []byte("cando1/server-hello/v1")
)

func mac(token string, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, []byte(token))
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

// ServerAuthenticator performs the server side of the handshake and remembers
// recently used client nonces to reject replays.
type ServerAuthenticator struct {
	token string
	mu    sync.Mutex
	seen  map[string]time.Time
}

// NewServerAuthenticator creates an authenticator for the given token.
func NewServerAuthenticator(token string) *ServerAuthenticator {
	return &ServerAuthenticator{token: token, seen: make(map[string]time.Time)}
}

// Handshake reads and verifies the client hello, then (only on success) sends
// the server hello. On any failure it returns an error WITHOUT writing anything
// to the connection, so an unauthenticated peer observes silence.
func (a *ServerAuthenticator) Handshake(conn net.Conn) error {
	_ = conn.SetDeadline(time.Now().Add(handshakeWait))
	defer conn.SetDeadline(time.Time{})

	hello := make([]byte, clientHelloLen)
	if _, err := io.ReadFull(conn, hello); err != nil {
		return fmt.Errorf("read client hello: %w", err)
	}
	ts := hello[:tsLen]
	nonce := hello[tsLen : tsLen+nonceLen]
	gotMAC := hello[tsLen+nonceLen:]

	// Timestamp window.
	clientTs := int64(binary.BigEndian.Uint64(ts))
	skew := time.Now().Unix() - clientTs
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(clockSkew.Seconds()) {
		return errors.New("client hello timestamp outside acceptance window")
	}

	// MAC.
	wantMAC := mac(a.token, labelClient, ts, nonce)
	if subtle.ConstantTimeCompare(gotMAC, wantMAC) != 1 {
		return errors.New("authentication failed: bad token")
	}

	// Replay.
	if !a.remember(string(nonce)) {
		return errors.New("replayed client nonce rejected")
	}

	// Authenticated: prove the server also knows the token.
	serverNonce := make([]byte, nonceLen)
	if _, err := rand.Read(serverNonce); err != nil {
		return err
	}
	reply := make([]byte, 0, serverHelloLen)
	reply = append(reply, serverNonce...)
	reply = append(reply, mac(a.token, labelServer, nonce, serverNonce)...)
	if _, err := conn.Write(reply); err != nil {
		return fmt.Errorf("write server hello: %w", err)
	}
	return nil
}

// remember records a nonce and returns false if it was already seen (replay).
func (a *ServerAuthenticator) remember(nonce string) bool {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, dup := a.seen[nonce]; dup {
		return false
	}
	// Opportunistic cleanup of expired entries.
	for k, exp := range a.seen {
		if now.After(exp) {
			delete(a.seen, k)
		}
	}
	a.seen[nonce] = now.Add(replayTTL)
	return true
}

// ClientHandshake performs the client side: it sends the client hello and
// verifies the server hello, proving the server also knows the token (mutual
// authentication).
func ClientHandshake(conn net.Conn, token string) error {
	_ = conn.SetDeadline(time.Now().Add(handshakeWait))
	defer conn.SetDeadline(time.Time{})

	ts := make([]byte, tsLen)
	binary.BigEndian.PutUint64(ts, uint64(time.Now().Unix()))
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	hello := make([]byte, 0, clientHelloLen)
	hello = append(hello, ts...)
	hello = append(hello, nonce...)
	hello = append(hello, mac(token, labelClient, ts, nonce)...)
	if _, err := conn.Write(hello); err != nil {
		return fmt.Errorf("write client hello: %w", err)
	}

	reply := make([]byte, serverHelloLen)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("read server hello (bad token, wrong transport, or unreachable): %w", err)
	}
	serverNonce := reply[:nonceLen]
	gotMAC := reply[nonceLen:]
	wantMAC := mac(token, labelServer, nonce, serverNonce)
	if subtle.ConstantTimeCompare(gotMAC, wantMAC) != 1 {
		return errors.New("server authentication failed: token mismatch or man-in-the-middle")
	}
	return nil
}

// WriteStreamHeader writes the per-stream metadata that the opener sends first.
func WriteStreamHeader(w io.Writer, kind byte, payload string) error {
	if len(payload) > maxHeaderLen {
		return fmt.Errorf("stream header payload too long: %d", len(payload))
	}
	buf := make([]byte, 0, 3+len(payload))
	buf = append(buf, kind)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(payload)))
	buf = append(buf, payload...)
	_, err := w.Write(buf)
	return err
}

// ReadStreamHeader reads the metadata written by WriteStreamHeader.
func ReadStreamHeader(r io.Reader) (kind byte, payload string, err error) {
	head := make([]byte, 3)
	if _, err = io.ReadFull(r, head); err != nil {
		return 0, "", err
	}
	kind = head[0]
	n := binary.BigEndian.Uint16(head[1:3])
	if n > maxHeaderLen {
		return 0, "", fmt.Errorf("stream header payload too long: %d", n)
	}
	if n == 0 {
		return kind, "", nil
	}
	body := make([]byte, n)
	if _, err = io.ReadFull(r, body); err != nil {
		return 0, "", err
	}
	return kind, string(body), nil
}

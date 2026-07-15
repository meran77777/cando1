package transport

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/chacha20"
)

// obfs implements a lightweight stream obfuscation layer for the plain-TCP
// transport. Both directions are encrypted with XChaCha20 keyed by the shared
// token. The client sends a single 24-byte random nonce and the server sends
// nothing in reply — both peers derive the per-direction keystreams from that
// nonce plus the token. Because the server never emits a byte in response to
// the opening nonce, an active prober that connects gets no distinguishing
// reply (no fixed-length echo), and the opening bytes are indistinguishable
// from random noise to a passive observer.
//
// This provides confidentiality and camouflage but NOT authenticated
// encryption: XChaCha20 without a MAC is malleable. The higher-level cando1
// handshake authenticates the peer, but for end-to-end integrity of the
// tunnelled data prefer the tls/wss transports. This tradeoff is intentional
// and documented for the TLS-unavailable censorship scenario.

const obfsNonceLen = chacha20.NonceSizeX // 24

// obfsConn wraps a net.Conn with per-direction XChaCha20 keystreams.
type obfsConn struct {
	net.Conn
	rc *chacha20.Cipher
	wc *chacha20.Cipher
}

func deriveKey(token, label string) []byte {
	h := sha256.New()
	h.Write([]byte("cando1-obfs\x00"))
	h.Write([]byte(label))
	h.Write([]byte{0})
	h.Write([]byte(token))
	return h.Sum(nil) // 32 bytes
}

// obfsClient performs the client half: send a random nonce, then set up the
// ciphers. It reads nothing from the server.
func obfsClient(raw net.Conn, token string) (net.Conn, error) {
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	nonce := make([]byte, obfsNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	if _, err := raw.Write(nonce); err != nil {
		return nil, fmt.Errorf("obfs write nonce: %w", err)
	}
	_ = raw.SetDeadline(time.Time{})
	return newObfsConn(raw, token, nonce, true)
}

// obfsServer performs the server half: read the client nonce and set up the
// ciphers. It writes nothing in response.
func obfsServer(raw net.Conn, token string) (net.Conn, error) {
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	nonce := make([]byte, obfsNonceLen)
	if _, err := io.ReadFull(raw, nonce); err != nil {
		return nil, fmt.Errorf("obfs read nonce: %w", err)
	}
	_ = raw.SetDeadline(time.Time{})
	return newObfsConn(raw, token, nonce, false)
}

// newObfsConn builds the cipher pair. Both directions use the same 24-byte
// nonce with different keys (safe for XChaCha20: nonce reuse is only a concern
// under the same key). isClient selects which key is read vs write.
func newObfsConn(raw net.Conn, token string, nonce []byte, isClient bool) (net.Conn, error) {
	keyC2S := deriveKey(token, "c2s")
	keyS2C := deriveKey(token, "s2c")

	var readKey, writeKey []byte
	if isClient {
		writeKey, readKey = keyC2S, keyS2C
	} else {
		readKey, writeKey = keyC2S, keyS2C
	}

	rc, err := chacha20.NewUnauthenticatedCipher(readKey, nonce)
	if err != nil {
		return nil, err
	}
	wc, err := chacha20.NewUnauthenticatedCipher(writeKey, nonce)
	if err != nil {
		return nil, err
	}
	return &obfsConn{Conn: raw, rc: rc, wc: wc}, nil
}

func (c *obfsConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.rc.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (c *obfsConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	buf := bufPool.Get().([]byte)
	defer bufPool.Put(buf)
	total := 0
	for total < len(p) {
		chunk := p[total:]
		if len(chunk) > len(buf) {
			chunk = chunk[:len(buf)]
		}
		enc := buf[:len(chunk)]
		c.wc.XORKeyStream(enc, chunk)
		if _, err := c.Conn.Write(enc); err != nil {
			return total, err
		}
		total += len(chunk)
	}
	return total, nil
}

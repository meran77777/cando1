package protocol

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func doHandshake(t *testing.T, serverToken, clientToken string) (serverErr, clientErr error) {
	t.Helper()
	c1, c2 := net.Pipe()
	auth := NewServerAuthenticator(serverToken)
	done := make(chan error, 1)
	go func() {
		e := auth.Handshake(c2)
		if e != nil {
			// Real server drops the connection on auth failure; closing here
			// unblocks the client's read with EOF instead of waiting for its
			// handshake deadline.
			_ = c2.Close()
		}
		done <- e
	}()
	clientErr = ClientHandshake(c1, clientToken)
	serverErr = <-done
	_ = c1.Close()
	_ = c2.Close()
	return serverErr, clientErr
}

func TestHandshakeSuccess(t *testing.T) {
	se, ce := doHandshake(t, "shared-token", "shared-token")
	if se != nil {
		t.Fatalf("server handshake: %v", se)
	}
	if ce != nil {
		t.Fatalf("client handshake: %v", ce)
	}
}

func TestHandshakeBadTokenRejected(t *testing.T) {
	se, ce := doHandshake(t, "right", "wrong")
	if se == nil {
		t.Fatal("server accepted a bad token")
	}
	if ce == nil {
		t.Fatal("client believed it authenticated with a bad token")
	}
}

// TestServerSilentToProbe verifies the server writes nothing to a peer that
// fails authentication (anti active-probing).
func TestServerSilentToProbe(t *testing.T) {
	c1, c2 := net.Pipe()
	auth := NewServerAuthenticator("secret")
	done := make(chan error, 1)
	go func() { done <- auth.Handshake(c2) }()

	// Send garbage of the exact client-hello length.
	garbage := make([]byte, clientHelloLen)
	for i := range garbage {
		garbage[i] = byte(i)
	}
	go func() { _, _ = c1.Write(garbage) }()

	// The server must reject without replying; reading should never yield data.
	_ = c1.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	n, err := c1.Read(buf)
	if n > 0 {
		t.Fatalf("server leaked %d bytes to an unauthenticated probe", n)
	}
	if err == nil {
		t.Fatal("expected no data / read error")
	}
	if se := <-done; se == nil {
		t.Fatal("server should have rejected the probe")
	}
	_ = c1.Close()
	_ = c2.Close()
}

// TestReplayRejected verifies a captured client hello cannot be replayed.
func TestReplayRejected(t *testing.T) {
	const token = "tok"
	auth := NewServerAuthenticator(token)

	ts := make([]byte, tsLen)
	binary.BigEndian.PutUint64(ts, uint64(time.Now().Unix()))
	nonce := make([]byte, nonceLen)
	for i := range nonce {
		nonce[i] = 0x42
	}
	hello := append(append(append([]byte{}, ts...), nonce...), mac(token, labelClient, ts, nonce)...)

	feed := func() error {
		c1, c2 := net.Pipe()
		done := make(chan error, 1)
		go func() { done <- auth.Handshake(c2) }()
		go func() {
			_, _ = c1.Write(hello)
			io.ReadFull(c1, make([]byte, serverHelloLen)) // drain reply if any
			_ = c1.Close()
		}()
		err := <-done
		_ = c2.Close()
		return err
	}

	if err := feed(); err != nil {
		t.Fatalf("first handshake should succeed: %v", err)
	}
	if err := feed(); err == nil {
		t.Fatal("replayed client hello was accepted")
	}
}

// TestStaleTimestampRejected verifies an old client hello is refused.
func TestStaleTimestampRejected(t *testing.T) {
	const token = "tok"
	auth := NewServerAuthenticator(token)

	ts := make([]byte, tsLen)
	binary.BigEndian.PutUint64(ts, uint64(time.Now().Add(-10*time.Minute).Unix()))
	nonce := make([]byte, nonceLen)
	hello := append(append(append([]byte{}, ts...), nonce...), mac(token, labelClient, ts, nonce)...)

	c1, c2 := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- auth.Handshake(c2) }()
	go func() { _, _ = c1.Write(hello); _ = c1.Close() }()
	if err := <-done; err == nil {
		t.Fatal("stale timestamp was accepted")
	}
	_ = c2.Close()
}

func TestStreamHeaderRoundTrip(t *testing.T) {
	c1, c2 := net.Pipe()
	go func() {
		_ = WriteStreamHeader(c1, KindForward, "127.0.0.1:22")
		_ = c1.Close()
	}()
	kind, payload, err := ReadStreamHeader(c2)
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	if kind != KindForward || payload != "127.0.0.1:22" {
		t.Fatalf("got kind=%d payload=%q", kind, payload)
	}
	_ = c2.Close()
}

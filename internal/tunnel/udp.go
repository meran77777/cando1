package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/xtaci/smux"
)

// UDP is carried over the same multiplexed tunnel as TCP, so it works across
// every carrier (tls/wss/ws/tcp/kcp). A UDP "flow" is one client source address;
// each flow gets its own smux stream, over which datagrams are sent
// length-prefixed:
//
//	[2-byte big-endian length][payload]
//
// The near end (the machine with the public/local UDP listener) demultiplexes
// datagrams by source address into per-flow streams; the far end bridges each
// stream to a connected UDP socket toward the real target.
const (
	maxDatagram = 65535 // fits the 2-byte length prefix; also >= max UDP payload

	// udpIdleTimeout garbage-collects a UDP flow after it has been silent this
	// long in BOTH directions. It is an idle reaper, not a blocking wait: active
	// flows refresh it on every datagram. Kept modest so dead flows (and their
	// streams) are reclaimed promptly.
	udpIdleTimeout = 60 * time.Second

	udpWriteWait   = 2 * time.Second // bound a single socket write
	udpSessionWait = 3 * time.Second // wait for a live tunnel before dropping a new flow
	udpHeaderWait  = 5 * time.Second // bound the per-stream header write
)

// writeDatagram frames payload into scratch (which must be >= len(payload)+2)
// and writes it as a single stream write.
func writeDatagram(w io.Writer, scratch, payload []byte) error {
	n := len(payload)
	if n > maxDatagram {
		return fmt.Errorf("datagram too large: %d", n)
	}
	binary.BigEndian.PutUint16(scratch[:2], uint16(n))
	copy(scratch[2:2+n], payload)
	_, err := w.Write(scratch[:2+n])
	return err
}

// readDatagram reads one length-prefixed datagram into buf (which must be >=
// maxDatagram) and returns its length.
func readDatagram(r io.Reader, buf []byte) (int, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n > len(buf) {
		return 0, fmt.Errorf("datagram length %d exceeds buffer", n)
	}
	if n == 0 {
		return 0, nil
	}
	if _, err := io.ReadFull(r, buf[:n]); err != nil {
		return 0, err
	}
	return n, nil
}

// bridgeUDP is the far-end handler: it relays datagrams between a tunnel stream
// and a connected UDP socket (uc), tearing both down when either goes idle or
// errors. Read deadlines enforce the idle timeout without a background reaper.
func bridgeUDP(stream *smux.Stream, uc net.Conn) {
	done := make(chan struct{})
	go func() { // uc -> stream
		defer close(done)
		buf := make([]byte, maxDatagram)
		scratch := make([]byte, maxDatagram+2)
		for {
			_ = uc.SetReadDeadline(time.Now().Add(udpIdleTimeout))
			n, err := uc.Read(buf)
			if err != nil {
				return
			}
			if err := writeDatagram(stream, scratch, buf[:n]); err != nil {
				return
			}
		}
	}()

	buf := make([]byte, maxDatagram)
	for { // stream -> uc
		_ = stream.SetReadDeadline(time.Now().Add(udpIdleTimeout))
		n, err := readDatagram(stream, buf)
		if err != nil {
			break
		}
		_ = uc.SetWriteDeadline(time.Now().Add(udpWriteWait))
		if _, err := uc.Write(buf[:n]); err != nil {
			break
		}
	}
	_ = stream.Close()
	_ = uc.Close()
	<-done
}

// udpFlows maps a UDP source address to the tunnel stream carrying its flow.
type udpFlows struct {
	mu sync.Mutex
	m  map[string]*smux.Stream
}

func newUDPFlows() *udpFlows { return &udpFlows{m: make(map[string]*smux.Stream)} }

func (f *udpFlows) get(key string) *smux.Stream {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[key]
}

func (f *udpFlows) put(key string, s *smux.Stream) {
	f.mu.Lock()
	f.m[key] = s
	f.mu.Unlock()
}

// del removes key only if it still maps to stream (identity check), so a stale
// reader goroutine cannot evict a freshly re-opened flow for the same source.
// It always closes the given stream.
func (f *udpFlows) del(key string, stream *smux.Stream) {
	f.mu.Lock()
	if cur, ok := f.m[key]; ok && cur == stream {
		delete(f.m, key)
	}
	f.mu.Unlock()
	_ = stream.Close()
}

func (f *udpFlows) closeAll() {
	f.mu.Lock()
	for k, s := range f.m {
		_ = s.Close()
		delete(f.m, k)
	}
	f.mu.Unlock()
}

// serveUDPListener is the near-end handler. It reads datagrams from a public or
// local UDP socket, and for each distinct source address opens a tunnel stream
// via open() and relays that source's datagrams over it. Replies read back from
// the stream are written to the matching source. It returns when pc is closed.
func serveUDPListener(pc *net.UDPConn, open func() (*smux.Stream, error)) {
	flows := newUDPFlows()
	defer flows.closeAll()

	buf := make([]byte, maxDatagram)
	scratch := make([]byte, maxDatagram+2)
	for {
		n, src, err := pc.ReadFromUDP(buf)
		if err != nil {
			return // listener closed
		}
		key := src.String()
		stream := flows.get(key)
		if stream == nil {
			s, err := open()
			if err != nil {
				continue // no tunnel yet; drop this datagram (UDP is lossy anyway)
			}
			stream = s
			flows.put(key, stream)
			go func(key string, src *net.UDPAddr, stream *smux.Stream) {
				defer flows.del(key, stream)
				rbuf := make([]byte, maxDatagram)
				for {
					_ = stream.SetReadDeadline(time.Now().Add(udpIdleTimeout))
					m, err := readDatagram(stream, rbuf)
					if err != nil {
						return
					}
					_ = pc.SetWriteDeadline(time.Now().Add(udpWriteWait))
					_, _ = pc.WriteToUDP(rbuf[:m], src)
				}
			}(key, src, stream)
		}
		// Refresh the idle timer on upstream activity too, so a flow that only
		// sends (e.g. one-way telemetry) is not reaped while it is still active.
		_ = stream.SetReadDeadline(time.Now().Add(udpIdleTimeout))
		if err := writeDatagram(stream, scratch, buf[:n]); err != nil {
			flows.del(key, stream)
		}
	}
}

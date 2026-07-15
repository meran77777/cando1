package tunnel

import (
	"io"
	"net"

	"github.com/meran77777/cando1/internal/transport"
)

// pipe copies data bidirectionally between a and b until either side closes,
// then tears both down. Buffers come from the shared pool to keep the hot path
// allocation-free.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		copyOneWay(a, b)
		done <- struct{}{}
	}()
	go func() {
		copyOneWay(b, a)
		done <- struct{}{}
	}()
	<-done
	// One direction ended; closing both unblocks the other copy goroutine.
	_ = a.Close()
	_ = b.Close()
	<-done
}

func copyOneWay(dst, src net.Conn) {
	buf := transport.GetBuffer()
	defer transport.PutBuffer(buf)
	_, _ = io.CopyBuffer(dst, src, buf)
}

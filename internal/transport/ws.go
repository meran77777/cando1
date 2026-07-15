package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	utls "github.com/refraction-networking/utls"

	"github.com/meran77777/cando1/internal/config"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

const notFoundPage = "<html>\r\n<head><title>404 Not Found</title></head>\r\n" +
	"<body>\r\n<center><h1>404 Not Found</h1></center>\r\n" +
	"<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

// isWebSocketUpgrade reports whether r is a WebSocket upgrade request.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// wsNetConn adapts a *websocket.Conn to net.Conn by carrying the byte stream in
// binary WebSocket frames. Message boundaries are irrelevant to the smux layer
// riding on top, so reads simply reassemble the contiguous stream.
type wsNetConn struct {
	ws     *websocket.Conn
	reader io.Reader
	wmu    sync.Mutex
}

func newWSNetConn(ws *websocket.Conn) *wsNetConn {
	ws.SetReadLimit(0)
	return &wsNetConn{ws: ws}
}

func (c *wsNetConn) Read(p []byte) (int, error) {
	for {
		if c.reader != nil {
			n, err := c.reader.Read(p)
			if err == io.EOF {
				c.reader = nil
				if n > 0 {
					return n, nil
				}
				continue
			}
			return n, err
		}
		mt, r, err := c.ws.NextReader()
		if err != nil {
			return 0, err
		}
		if mt != websocket.BinaryMessage {
			continue // ignore text/control frames
		}
		c.reader = r
	}
}

func (c *wsNetConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsNetConn) Close() error                       { return c.ws.Close() }
func (c *wsNetConn) LocalAddr() net.Addr                { return c.ws.LocalAddr() }
func (c *wsNetConn) RemoteAddr() net.Addr               { return c.ws.RemoteAddr() }
func (c *wsNetConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsNetConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }
func (c *wsNetConn) SetDeadline(t time.Time) error {
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}

// --- WebSocket dialer ---

type wsDialer struct {
	urlStr string
	dialer *websocket.Dialer
	header http.Header
}

func newWSDialer(cfg *config.ClientConfig) (Dialer, error) {
	scheme := "ws"
	if cfg.Transport == config.TransportWSS {
		scheme = "wss"
	}
	sni := cfg.SNI
	if sni == "" {
		sni = hostOnly(cfg.ServerAddr)
	}
	host := cfg.Host
	if host == "" {
		host = sni
	}
	u := url.URL{Scheme: scheme, Host: cfg.ServerAddr, Path: cfg.WSPath}

	d := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		ReadBufferSize:   32 * 1024,
		WriteBufferSize:  32 * 1024,
	}
	if scheme == "wss" {
		fp := fingerprintID(cfg.Fingerprint)
		d.NetDialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			var nd net.Dialer
			raw, err := nd.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			tuneTCP(raw)
			uconn := utls.UClient(raw, &utls.Config{
				ServerName:         sni,
				InsecureSkipVerify: cfg.Insecure,
				NextProtos:         []string{"http/1.1"},
			}, fp)
			_ = uconn.SetDeadline(time.Now().Add(15 * time.Second))
			if err := uconn.HandshakeContext(ctx); err != nil {
				raw.Close()
				return nil, fmt.Errorf("uTLS handshake: %w", err)
			}
			_ = uconn.SetDeadline(time.Time{})
			return uconn, nil
		}
	} else {
		d.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			var nd net.Dialer
			raw, err := nd.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			tuneTCP(raw)
			return raw, nil
		}
	}

	header := http.Header{}
	header.Set("User-Agent", browserUA)
	header.Set("Host", host) // gorilla applies this as the request Host header

	return &wsDialer{urlStr: u.String(), dialer: d, header: header}, nil
}

func (d *wsDialer) String() string { return d.urlStr }

func (d *wsDialer) Dial(ctx context.Context) (net.Conn, error) {
	ws, resp, err := d.dialer.DialContext(ctx, d.urlStr, d.header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("ws dial %s: %w (http %s)", d.urlStr, err, resp.Status)
		}
		return nil, fmt.Errorf("ws dial %s: %w", d.urlStr, err)
	}
	return newWSNetConn(ws), nil
}

// --- WebSocket listener ---

type wsListener struct {
	ln       net.Listener
	httpSrv  *http.Server
	connCh   chan net.Conn
	done     chan struct{}
	closeOne sync.Once
	path     string
	up       websocket.Upgrader
}

func newWSListener(cfg *config.ServerConfig) (Listener, error) {
	ln, err := net.Listen("tcp", cfg.BindAddr)
	if err != nil {
		return nil, err
	}
	if cfg.Transport == config.TransportWSS {
		tc, err := serverTLSConfig(cfg.TLS)
		if err != nil {
			ln.Close()
			return nil, err
		}
		tc.NextProtos = []string{"http/1.1"}
		ln = tls.NewListener(ln, tc)
	}
	l := &wsListener{
		ln:     ln,
		connCh: make(chan net.Conn, 128),
		done:   make(chan struct{}),
		path:   cfg.WSPath,
		up: websocket.Upgrader{
			ReadBufferSize:  32 * 1024,
			WriteBufferSize: 32 * 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", l.handle)
	l.httpSrv = &http.Server{
		Handler:  mux,
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() { _ = l.httpSrv.Serve(ln) }()
	return l, nil
}

func (l *wsListener) handle(w http.ResponseWriter, r *http.Request) {
	// Present a generic nginx-like face to anything that is not a valid tunnel
	// WebSocket upgrade, so active probers and crawlers see a boring web server
	// instead of Go's default responses.
	if (l.path != "" && r.URL.Path != l.path) || !isWebSocketUpgrade(r) {
		w.Header().Set("Server", "nginx")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, notFoundPage)
		return
	}
	ws, err := l.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	nc := newWSNetConn(ws)
	select {
	case l.connCh <- nc:
	case <-l.done:
		nc.Close()
	}
}

func (l *wsListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connCh:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *wsListener) Close() error {
	l.closeOne.Do(func() {
		close(l.done)
		_ = l.httpSrv.Close()
		_ = l.ln.Close()
	})
	return nil
}

func (l *wsListener) Addr() net.Addr { return l.ln.Addr() }

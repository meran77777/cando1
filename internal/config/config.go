// Package config defines the TOML configuration schema for cando1 and the
// helpers used to load, validate and normalise it.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrNoRole is returned when a config has neither a server nor a client role.
var ErrNoRole = errors.New("config has no [server] or [client] role")

// Transport names.
const (
	TransportTCP = "tcp"
	TransportTLS = "tls"
	TransportWS  = "ws"
	TransportWSS = "wss"
	TransportKCP = "kcp"
)

// Config is the root document. Exactly one of Server / Client must be present.
type Config struct {
	Log    LogConfig     `toml:"log"`
	Server *ServerConfig `toml:"server"`
	Client *ClientConfig `toml:"client"`
}

// LogConfig controls logging.
type LogConfig struct {
	Level string `toml:"level"` // debug|info|warn|error|silent
}

// TLSConfig holds certificate material for the server side of a TLS/WSS transport.
type TLSConfig struct {
	Cert     string `toml:"cert"`      // path to PEM cert; empty => self-signed generated at runtime
	Key      string `toml:"key"`       // path to PEM key
	SelfName string `toml:"self_name"` // CN/SAN to embed in the auto-generated self-signed cert
}

// MuxConfig tunes the smux multiplexer.
type MuxConfig struct {
	Enable           bool `toml:"enable"`
	KeepAliveSeconds int  `toml:"keepalive_seconds"`
	MaxStreamBuffer  int  `toml:"max_stream_buffer"` // bytes
	MaxReceiveBuffer int  `toml:"max_receive_buffer"`
}

// ReconnectConfig tunes client auto-reconnect backoff.
type ReconnectConfig struct {
	MinMillis int `toml:"min_millis"`
	MaxMillis int `toml:"max_millis"`
}

// KCPConfig tunes the KCP (UDP + FEC) transport — the high-speed carrier that
// avoids TCP-over-TCP meltdown on lossy international links. The token doubles
// as the AES key for the UDP payload.
type KCPConfig struct {
	Mode      string `toml:"mode"`       // fast3|fast2|fast|normal (aggressiveness)
	FECData   int    `toml:"fec_data"`   // Reed-Solomon data shards (must match the peer)
	FECParity int    `toml:"fec_parity"` // parity shards for loss recovery (must match the peer)
	MTU       int    `toml:"mtu"`        // UDP payload MTU (clamped to a safe range)
	SndWnd    int    `toml:"snd_wnd"`    // send window (packets)
	RcvWnd    int    `toml:"rcv_wnd"`    // receive window (packets)
	SockBuf   int    `toml:"sock_buf"`   // UDP socket buffer bytes
}

// DefaultKCP returns fast, high-throughput defaults tuned for lossy
// Iran<->Europe links (aggressive, FEC on, large windows).
func DefaultKCP() KCPConfig {
	return KCPConfig{
		Mode:      "fast3",
		FECData:   10,
		FECParity: 3,
		MTU:       1350,
		SndWnd:    2048,
		RcvWnd:    2048,
		SockBuf:   16 * 1024 * 1024,
	}
}

// ServerConfig is the anchor that accepts tunnel connections.
type ServerConfig struct {
	BindAddr         string    `toml:"bind_addr"`         // e.g. 0.0.0.0:443
	Transport        string    `toml:"transport"`         // tcp|tls|ws|wss
	Token            string    `toml:"token"`             // shared secret
	Obfs             bool      `toml:"obfs"`              // chacha20 obfuscation (tcp transport only)
	WSPath           string    `toml:"ws_path"`           // ws/wss carrier path, e.g. /cando
	Host             string    `toml:"host"`              // expected Host header for ws/wss (cosmetic camouflage)
	TLS              TLSConfig `toml:"tls"`               // cert material for tls/wss
	KCP              KCPConfig `toml:"kcp"`               // tuning for the kcp transport
	Mux              MuxConfig `toml:"mux"`               //
	AllowForward     bool      `toml:"allow_forward"`     // permit client-initiated forward streams
	ForwardWhitelist []string  `toml:"forward_whitelist"` // if non-empty, only these host:port targets are allowed
	Services         []Service `toml:"services"`          // reverse tunnels exposed publicly by the server
}

// ClientConfig dials a server and maintains the tunnel.
type ClientConfig struct {
	ServerAddr  string          `toml:"server_addr"` // host:port of the server
	Transport   string          `toml:"transport"`
	Token       string          `toml:"token"`
	Obfs        bool            `toml:"obfs"`
	WSPath      string          `toml:"ws_path"`
	SNI         string          `toml:"sni"`         // TLS SNI / camouflage domain (tls,wss)
	Host        string          `toml:"host"`        // Host header for ws/wss (defaults to SNI)
	Fingerprint string          `toml:"fingerprint"` // chrome|firefox|safari|edge|randomized|random
	Insecure    bool            `toml:"insecure"`    // skip server cert verification (needed for self-signed)
	PoolSize    int             `toml:"pool_size"`   // number of parallel physical connections
	KCP         KCPConfig       `toml:"kcp"`         // tuning for the kcp transport
	Mux         MuxConfig       `toml:"mux"`
	Reconnect   ReconnectConfig `toml:"reconnect"`
	Services    []ClientTarget  `toml:"services"` // reverse: local target for each server-exposed service
	Forwards    []Forward       `toml:"forwards"` // forward: local listeners tunneled out to server-side targets
}

// Service is a reverse tunnel: the server binds a public port and hands each
// accepted connection to the client, which pipes it to its matching local target.
type Service struct {
	Name     string `toml:"name"`
	BindAddr string `toml:"bind_addr"` // public listen addr on the server, e.g. 0.0.0.0:8443
}

// ClientTarget is the client side of a reverse Service.
type ClientTarget struct {
	Name      string `toml:"name"`
	LocalAddr string `toml:"local_addr"` // where the client forwards traffic to, e.g. 127.0.0.1:8443
}

// Forward is a forward tunnel: the client binds a local port and every accepted
// connection is tunneled to the server, which dials TargetAddr.
type Forward struct {
	Name       string `toml:"name"`
	LocalAddr  string `toml:"local_addr"`  // client-side listen addr, e.g. 0.0.0.0:1080
	TargetAddr string `toml:"target_addr"` // addr the server dials, e.g. 127.0.0.1:1080
}

// DefaultMux returns tuned defaults for the multiplexer.
func DefaultMux() MuxConfig {
	return MuxConfig{
		Enable:           true,
		KeepAliveSeconds: 10,
		MaxStreamBuffer:  8 * 1024 * 1024,
		MaxReceiveBuffer: 32 * 1024 * 1024,
	}
}

// Load reads and validates a TOML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.normalize()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) normalize() {
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Server != nil {
		s := c.Server
		s.Transport = strings.ToLower(strings.TrimSpace(s.Transport))
		if s.Transport == "" {
			s.Transport = TransportTLS
		}
		if s.WSPath == "" {
			s.WSPath = "/cando"
		}
		if !s.Mux.Enable && s.Mux.KeepAliveSeconds == 0 {
			s.Mux = DefaultMux()
		}
		if s.Transport == TransportKCP {
			fillKCPDefaults(&s.KCP)
		}
	}
	if c.Client != nil {
		cl := c.Client
		cl.Transport = strings.ToLower(strings.TrimSpace(cl.Transport))
		if cl.Transport == "" {
			cl.Transport = TransportTLS
		}
		if cl.WSPath == "" {
			cl.WSPath = "/cando"
		}
		if cl.Fingerprint == "" {
			cl.Fingerprint = "chrome"
		}
		if cl.PoolSize <= 0 {
			cl.PoolSize = 2
		}
		if cl.Host == "" {
			cl.Host = cl.SNI
		}
		if !cl.Mux.Enable && cl.Mux.KeepAliveSeconds == 0 {
			cl.Mux = DefaultMux()
		}
		if cl.Reconnect.MinMillis == 0 {
			cl.Reconnect.MinMillis = 500
		}
		if cl.Reconnect.MaxMillis == 0 {
			cl.Reconnect.MaxMillis = 30000
		}
		if cl.Transport == TransportKCP {
			fillKCPDefaults(&cl.KCP)
		}
	}
}

func validTransport(t string) bool {
	switch t {
	case TransportTCP, TransportTLS, TransportWS, TransportWSS, TransportKCP:
		return true
	}
	return false
}

// fillKCPDefaults applies DefaultKCP for any field left at zero.
func fillKCPDefaults(k *KCPConfig) {
	d := DefaultKCP()
	if k.Mode == "" {
		k.Mode = d.Mode
	}
	// FEC data/parity are a coupled pair (both must be positive for Reed-Solomon
	// to engage, and both ends must agree). If either is unset, apply both
	// defaults so a half-specified section can never silently disable FEC or
	// break wire framing against a default peer.
	if k.FECData <= 0 || k.FECParity <= 0 {
		k.FECData, k.FECParity = d.FECData, d.FECParity
	}
	if k.MTU <= 0 {
		k.MTU = d.MTU
	}
	if k.SndWnd <= 0 {
		k.SndWnd = d.SndWnd
	}
	if k.RcvWnd <= 0 {
		k.RcvWnd = d.RcvWnd
	}
	if k.SockBuf <= 0 {
		k.SockBuf = d.SockBuf
	}
}

// Validate checks the configuration for consistency.
func (c *Config) Validate() error {
	if c.Server == nil && c.Client == nil {
		return errors.New("config must contain a [server] or [client] section")
	}
	if c.Server != nil && c.Client != nil {
		return errors.New("config must contain only one of [server] or [client]")
	}
	if s := c.Server; s != nil {
		if s.BindAddr == "" {
			return errors.New("server.bind_addr is required")
		}
		if !validTransport(s.Transport) {
			return fmt.Errorf("server.transport %q is invalid (tcp|tls|ws|wss|kcp)", s.Transport)
		}
		if s.Token == "" {
			return errors.New("server.token is required")
		}
		seen := map[string]bool{}
		for i, svc := range s.Services {
			if svc.Name == "" || svc.BindAddr == "" {
				return fmt.Errorf("server.services[%d] needs name and bind_addr", i)
			}
			if seen[svc.Name] {
				return fmt.Errorf("server.services duplicate name %q", svc.Name)
			}
			seen[svc.Name] = true
		}
	}
	if cl := c.Client; cl != nil {
		if cl.ServerAddr == "" {
			return errors.New("client.server_addr is required")
		}
		if !validTransport(cl.Transport) {
			return fmt.Errorf("client.transport %q is invalid (tcp|tls|ws|wss|kcp)", cl.Transport)
		}
		if cl.Token == "" {
			return errors.New("client.token is required")
		}
		if len(cl.Services) == 0 && len(cl.Forwards) == 0 {
			return errors.New("client must define at least one [[client.services]] or [[client.forwards]]")
		}
		for i, svc := range cl.Services {
			if svc.Name == "" || svc.LocalAddr == "" {
				return fmt.Errorf("client.services[%d] needs name and local_addr", i)
			}
		}
		for i, f := range cl.Forwards {
			if f.LocalAddr == "" || f.TargetAddr == "" {
				return fmt.Errorf("client.forwards[%d] needs local_addr and target_addr", i)
			}
		}
	}
	return nil
}

// IsTLS reports whether the transport carries a TLS layer.
func IsTLS(transport string) bool {
	return transport == TransportTLS || transport == TransportWSS
}

// IsWS reports whether the transport uses a WebSocket carrier.
func IsWS(transport string) bool {
	return transport == TransportWS || transport == TransportWSS
}

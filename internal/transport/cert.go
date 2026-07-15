package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/meran77777/cando1/internal/config"
)

// loadOrCreateCert returns the TLS certificate the server presents. If the
// config points at PEM files they are loaded; otherwise a self-signed
// certificate is generated in memory using the configured SAN so the handshake
// looks like a connection to that domain.
func loadOrCreateCert(t config.TLSConfig) (tls.Certificate, error) {
	if t.Cert != "" && t.Key != "" {
		return tls.LoadX509KeyPair(t.Cert, t.Key)
	}
	name := t.SelfName
	if name == "" {
		name = "localhost"
	}
	return generateSelfSigned(name)
}

// generateSelfSigned builds an honest self-signed certificate for cn. It does
// NOT impersonate any real brand (a self-signed cert claiming to be a public
// CDN is itself a strong fingerprint, and never matches the genuine chain that
// a prober can compare against). For real camouflage, supply a genuine
// certificate for a domain you control via [server.tls] cert/key, or front the
// server behind a real CDN. The SAN contains exactly the configured name.
func generateSelfSigned(cn string) (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     time.Now().Add(825 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(cn); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{cn}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("assemble self-signed cert: %w", err)
	}
	return cert, nil
}

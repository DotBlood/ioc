package runtime

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// Optional mTLS for the runtime protocol (off by default). HONEST FRAMING: the
// daemon is loopback-only today, so mTLS is largely TM2-prep — it authenticates
// the channel but does NOT replace the bearer token, and on loopback it adds
// little over the token + 0o600 runtime.json. Enable only when preparing a
// non-loopback / multi-host deployment.
const (
	tlsEnabledEnv = "IOC_RUNTIME_TLS"
	tlsCertEnv    = "IOC_RUNTIME_TLS_CERT"
	tlsKeyEnv     = "IOC_RUNTIME_TLS_KEY"
	tlsCAEnv      = "IOC_RUNTIME_TLS_CA"
)

// TLSEnabled reports whether runtime mTLS is switched on via the environment.
func TLSEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(tlsEnabledEnv))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func loadCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("runtime tls: read CA %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("runtime tls: no certificates in CA %q", path)
	}
	return pool, nil
}

// ServerTLSConfig builds the daemon's TLS config from the environment: its own
// cert/key plus a CA against which it REQUIRES and verifies client certs.
func ServerTLSConfig() (*tls.Config, error) {
	cert, key, ca := os.Getenv(tlsCertEnv), os.Getenv(tlsKeyEnv), os.Getenv(tlsCAEnv)
	if cert == "" || key == "" || ca == "" {
		return nil, fmt.Errorf("runtime tls: set %s, %s and %s", tlsCertEnv, tlsKeyEnv, tlsCAEnv)
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("runtime tls: server cert: %w", err)
	}
	pool, err := loadCAPool(ca)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{pair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}, nil
}

// clientTLSConfig builds a client TLS config from the environment: its client
// cert/key plus the CA that signs the server, verified under serverName.
func clientTLSConfig(serverName string) (*tls.Config, error) {
	cert, key, ca := os.Getenv(tlsCertEnv), os.Getenv(tlsKeyEnv), os.Getenv(tlsCAEnv)
	if cert == "" || key == "" || ca == "" {
		return nil, fmt.Errorf("runtime tls: daemon requires mTLS; set %s, %s and %s", tlsCertEnv, tlsKeyEnv, tlsCAEnv)
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("runtime tls: client cert: %w", err)
	}
	pool, err := loadCAPool(ca)
	if err != nil {
		return nil, err
	}
	if serverName == "" {
		serverName = "localhost"
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{pair},
		RootCAs:      pool,
		ServerName:   serverName,
	}, nil
}

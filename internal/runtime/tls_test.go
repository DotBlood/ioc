package runtime

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

// genTLSEnv generates a CA and a leaf cert (valid for both server and client auth,
// SANs localhost + 127.0.0.1), writes them to dir, and points the runtime TLS env
// vars at them. Returns the CA pool for building an INDEPENDENT (bad-CA) client.
func genTLSEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	caCert, caKey := makeCA(t)
	leafPEM, leafKeyPEM := makeLeaf(t, caCert, caKey)

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	caPath := filepath.Join(dir, "ca.pem")
	certPath := filepath.Join(dir, "leaf.pem")
	keyPath := filepath.Join(dir, "leaf.key")
	mustWrite(t, caPath, caPEM)
	mustWrite(t, certPath, leafPEM)
	mustWrite(t, keyPath, leafKeyPEM)

	t.Setenv(tlsEnabledEnv, "1")
	t.Setenv(tlsCertEnv, certPath)
	t.Setenv(tlsKeyEnv, keyPath)
	t.Setenv(tlsCAEnv, caPath)
}

func makeCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ioc-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert, key
}

func makeLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// startTLSDaemon starts a daemon with mTLS using the env-configured certs.
func startTLSDaemon(t *testing.T, dir string) *Server {
	t.Helper()
	e, err := engine.Open(context.Background(), dir, embed.NewMockEmbedder(16))
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	cfg, err := ServerTLSConfig()
	if err != nil {
		t.Fatalf("server tls config: %v", err)
	}
	srv := NewServerTLS(e, dir, cfg)
	go func() { _ = srv.Serve() }()
	<-srv.Ready()
	return srv
}

// mTLS round-trip: a properly-certed client connects and operates; the bearer
// token is still enforced on top of the channel.
func TestTLS_RoundTripAndTokenEnforced(t *testing.T) {
	ctx := context.Background()
	genTLSEnv(t)
	dir := t.TempDir()
	srv := startTLSDaemon(t, dir)
	defer srv.Stop()

	info, _ := Info(dir)
	if !info.TLS {
		t.Fatal("runtime.json should advertise TLS")
	}

	c, err := Dial(dir) // dials TLS (info.TLS) with the env client cert
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer c.Close()
	root, err := c.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	if err != nil {
		t.Fatalf("create over mTLS: %v", err)
	}
	if _, _, err := c.Query(ctx, core.Query{Scope: root.ID, Text: "x", TopK: 5}); err != nil {
		t.Fatalf("query over mTLS: %v", err)
	}

	// Valid channel + WRONG token → codeAuth (mTLS authenticates the channel, the
	// token authorizes the principal).
	bad, err := DialWithToken(dir, "wrong-token")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer bad.Close()
	if _, err := bad.ListScopes(ctx); err == nil {
		t.Fatal("wrong token over mTLS must still fail")
	}
}

// A plaintext client cannot talk to a TLS daemon.
func TestTLS_PlaintextClientRejected(t *testing.T) {
	genTLSEnv(t)
	dir := t.TempDir()
	srv := startTLSDaemon(t, dir)
	defer srv.Stop()

	info, _ := Info(dir)
	conn, err := net.Dial(info.Net, info.Addr) // raw TCP, no TLS handshake
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	b, _ := json.Marshal(request{ID: 1, V: ProtoVersion, Method: mStats, Token: info.Token})
	_ = writeFrame(conn, b)
	if _, err := readFrame(conn, maxDataFrame); err == nil {
		t.Fatal("plaintext request to a TLS daemon should fail")
	}
}

// A client cert from a DIFFERENT CA is rejected at the handshake.
func TestTLS_BadClientCertRejected(t *testing.T) {
	genTLSEnv(t)
	dir := t.TempDir()
	srv := startTLSDaemon(t, dir)
	defer srv.Stop()
	info, _ := Info(dir)

	// Build a client cert from an unrelated CA.
	otherCA, otherKey := makeCA(t)
	leafPEM, leafKeyPEM := makeLeaf(t, otherCA, otherKey)
	pair, err := tls.X509KeyPair(leafPEM, leafKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	// Trust the server's real CA so the SERVER cert verifies; only the CLIENT cert
	// is from the wrong CA → server rejects it.
	realCA, _ := os.ReadFile(os.Getenv(tlsCAEnv))
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(realCA)
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, RootCAs: pool, ServerName: "localhost"}

	// In TLS 1.3 the server's client-cert rejection surfaces on the first
	// application read, not necessarily at Dial — so attempt a full request and
	// require the round-trip to fail.
	conn, derr := tls.Dial(info.Net, info.Addr, cfg)
	if derr != nil {
		return // rejected already at handshake — also acceptable
	}
	defer conn.Close()
	b, _ := json.Marshal(request{ID: 1, V: ProtoVersion, Method: mStats, Token: info.Token})
	_ = writeFrame(conn, b)
	if _, err := readFrame(conn, maxDataFrame); err == nil {
		t.Fatal("a client cert from the wrong CA must be rejected")
	}
}

// Default OFF: a plain NewServer advertises no TLS and round-trips as before.
func TestTLS_DefaultOff(t *testing.T) {
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()
	info, _ := Info(dir)
	if info.TLS {
		t.Fatal("default daemon must not advertise TLS")
	}
}

package federation

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newIdentity generates a self-signed ECDSA P-256 federation identity valid
// over [notBefore, notAfter] — the shape ADR-0002 §3 pins.
func newIdentity(t *testing.T, notBefore, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "torque-federation-test"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// validIdentity is a one-year-valid identity.
func validIdentity(t *testing.T) tls.Certificate {
	t.Helper()
	now := time.Now()
	return newIdentity(t, now.Add(-time.Hour), now.Add(365*24*time.Hour))
}

// expiredIdentity is an identity whose validity window is entirely in the past.
func expiredIdentity(t *testing.T) tls.Certificate {
	t.Helper()
	now := time.Now()
	return newIdentity(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
}

// fingerprintOf returns the SHA-256 leaf fingerprint of an identity.
func fingerprintOf(c tls.Certificate) string {
	return Fingerprint(c.Leaf)
}

// writeIdentityPEM writes an identity's certificate and PKCS#8 key as PEM
// files <name>.crt / <name>.key in dir.
func writeIdentityPEM(t *testing.T, dir, name string, id tls.Certificate) {
	t.Helper()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.Certificate[0]})
	keyDER, err := x509.MarshalPKCS8PrivateKey(id.PrivateKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(dir, name+".crt"), certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".key"), keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

package federation

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
)

// fingerprintHexLen is the hex length of a SHA-256 digest: 32 bytes → 64 hex
// chars. A pin (ADR-0002 §3) is exactly this.
const fingerprintHexLen = sha256.Size * 2

// Fingerprint returns the lower-case hex SHA-256 of a certificate's
// DER-encoded leaf (cert.Raw). This is the value ADR-0002 §3 pins peer
// identity on — both for an inbound client cert and an outbound server cert.
//
// It is computed over cert.Raw, which is exactly the bytes presented on the
// wire (and exactly what tls.Config.VerifyPeerCertificate receives as
// rawCerts[0]), so a pin computed here matches a pin computed at handshake.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// fingerprintBytes is Fingerprint over a raw DER blob — used inside the TLS
// verify hooks, which receive [][]byte and have no parsed certificate yet.
func fingerprintBytes(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// NormalizeFingerprint canonicalizes an operator-supplied fingerprint so that
// the colon-separated form `openssl x509 -fingerprint` prints and a bare hex
// string compare equal: ':' and whitespace are stripped, the result is
// lower-cased.
func NormalizeFingerprint(s string) string {
	s = strings.ReplaceAll(s, ":", "")
	s = strings.Join(strings.Fields(s), "")
	return strings.ToLower(s)
}

// ValidateFingerprint normalizes s and verifies it is a syntactically valid
// SHA-256 fingerprint (64 hex chars). It returns the normalized form so a
// registry stores one canonical representation.
func ValidateFingerprint(s string) (string, error) {
	n := NormalizeFingerprint(s)
	if len(n) != fingerprintHexLen {
		return "", fmt.Errorf("fingerprint %q: want %d hex chars (SHA-256), got %d", s, fingerprintHexLen, len(n))
	}
	if _, err := hex.DecodeString(n); err != nil {
		return "", fmt.Errorf("fingerprint %q: not valid hex: %w", s, err)
	}
	return n, nil
}

package federation

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// federationHTTPTimeout bounds a single outbound federation request. It
// matches messaging.HTTPStore's own default — every federated call is a short
// request/response exchange (Subscribe is not federated), so a fixed ceiling
// is safe and keeps a hung peer from pinning a caller.
const federationHTTPTimeout = 30 * time.Second

// ServerTLSConfig builds the *tls.Config for the federation listener
// (ADR-0002 §3, §8.3). Peer identity is established by direct leaf-certificate
// pinning, not a CA chain:
//
//   - ClientAuth is RequireAnyClientCert — a client certificate is mandatory,
//     but Go is told NOT to chain-verify it. ADR-0002 §8.3 nominally calls for
//     RequireAndVerifyClientCert; that mode verifies the presented cert against
//     ClientCAs and would fail the handshake before our hook runs, because a
//     pinned self-signed cert has no chain. Pinning IS the verification, and
//     it is stricter than a CA. We therefore require a cert and do every check
//     ourselves in VerifyPeerCertificate.
//   - VerifyPeerCertificate pins the presented leaf against the peer registry
//     and honors NotBefore/NotAfter, so an unpinned or expired cert fails the
//     handshake (fail-closed).
func ServerTLSConfig(identity tls.Certificate, peers *PeerRegistry) *tls.Config {
	return &tls.Config{
		Certificates:          []tls.Certificate{identity},
		MinVersion:            tls.VersionTLS12,
		ClientAuth:            tls.RequireAnyClientCert,
		VerifyPeerCertificate: pinVerifier(peers.lookupFunc(), "client"),
	}
}

// ClientTLSConfig builds the *tls.Config an outbound federation HTTPStore
// dials a peer with (ADR-0002 §3, §8.7). It presents this install's identity
// as the client certificate and pins the peer's self-signed server cert by
// SHA-256 leaf fingerprint.
//
// InsecureSkipVerify disables Go's default CA-chain + hostname verifier — the
// peer cert is self-signed and intentionally has no chain. It is NOT a
// downgrade: VerifyPeerCertificate replaces that verifier with an exact-leaf
// pin check plus expiry, which is stricter than chain validation. serverPins
// must be non-empty; an empty pin set is rejected so a route can never silently
// become unauthenticated.
func ClientTLSConfig(identity tls.Certificate, serverPins []string) (*tls.Config, error) {
	pins := make(map[string]struct{}, len(serverPins))
	for _, p := range serverPins {
		norm, err := ValidateFingerprint(p)
		if err != nil {
			return nil, err
		}
		pins[norm] = struct{}{}
	}
	if len(pins) == 0 {
		return nil, errors.New("federation: client TLS config requires at least one server pin")
	}
	lookup := func(fp string) bool {
		_, ok := pins[fp]
		return ok
	}
	return &tls.Config{
		Certificates: []tls.Certificate{identity},
		MinVersion:   tls.VersionTLS12,
		//nolint:gosec // G402: the default verifier is intentionally replaced
		// by VerifyPeerCertificate, which pins the exact leaf — stricter than
		// CA-chain verification (ADR-0002 §3).
		InsecureSkipVerify:    true,
		VerifyPeerCertificate: pinVerifier(lookup, "server"),
	}, nil
}

// ClientHTTPClient builds the mTLS *http.Client a federation HTTPStore uses to
// reach one peer. It is the concrete value injected via messaging.HTTPStore's
// WithHTTPClient seam (ADR-0002 §8.7).
func ClientHTTPClient(identity tls.Certificate, serverPins []string) (*http.Client, error) {
	tc, err := ClientTLSConfig(identity, serverPins)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: federationHTTPTimeout,
		Transport: &http.Transport{
			TLSClientConfig:     tc,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        16,
			IdleConnTimeout:     90 * time.Second,
		},
	}, nil
}

// pinVerifier returns a tls.Config.VerifyPeerCertificate hook that accepts a
// presented certificate iff its SHA-256 leaf fingerprint satisfies pinned
// (the registry lookup) AND the certificate is within its validity window.
// role ("client" / "server") only shapes the error message.
//
// It deliberately works off rawCerts[0] — the DER exactly as presented — so
// the fingerprint it computes is the same value an operator pins.
func pinVerifier(pinned func(fp string) bool, role string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("federation: no %s certificate presented", role)
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("federation: parse %s certificate: %w", role, err)
		}
		now := time.Now()
		if now.Before(leaf.NotBefore) {
			return fmt.Errorf("federation: %s certificate not yet valid (NotBefore %s)", role, leaf.NotBefore)
		}
		if now.After(leaf.NotAfter) {
			return fmt.Errorf("federation: %s certificate expired (NotAfter %s)", role, leaf.NotAfter)
		}
		fp := fingerprintBytes(rawCerts[0])
		if !pinned(fp) {
			return fmt.Errorf("federation: %s certificate %s is not pinned", role, fp)
		}
		return nil
	}
}

// lookupFunc adapts a PeerRegistry to the bare fingerprint→bool predicate
// pinVerifier needs (the verify hook only decides accept/reject; the request
// middleware re-resolves the *Peer for authorization).
func (r *PeerRegistry) lookupFunc() func(fp string) bool {
	return func(fp string) bool {
		_, ok := r.Lookup(fp)
		return ok
	}
}

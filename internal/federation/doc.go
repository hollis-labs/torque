// Package federation secures the cross-host messaging hop — the
// foreign-authority leg of Torque's authority-routing messaging stack.
//
// Federation in Torque is delivered by routing, never by forking the envelope
// schema (ADR-0001 §4): the authority-routing Router (internal/messaging)
// serves a local authority from the SQLite Store and a *foreign* authority
// from an HTTP-backed Store that talks to the peer install that homes it.
// That hop crosses a real trust boundary the rest of the stack does not
// defend. This package is the defense, implementing ADR-0002:
//
//   - Identity & transport — mutual TLS. Both peers present a self-signed
//     X.509 certificate, pinned by the other install by SHA-256 leaf
//     fingerprint (no CA, no CRL/OCSP). See tls.go, fingerprint.go.
//   - Authorization & trust boundary — an authority allowlist keyed on the
//     pinned peer certificate. A peer may only originate mail for authorities
//     it is registered authoritative for (no impersonation) and may only have
//     mail delivered for authorities this install homes (no relay abuse).
//     See peer.go, authz.go.
//   - Surface — a dedicated mTLS listener, physically separate from the
//     plaintext /api/v1/* GUI surface, exposing push-delivery + reply
//     correlation only: Send/Get/Thread/Consume/Cancel. Inbox and Subscribe
//     are never federated. See server.go.
//
// Standalone guarantee (ADR-0001/0002): when no federation config file is
// present, Enable returns a nil *Federation and nothing in this package runs
// — no listener, no surface, zero extra configuration, zero new attack
// surface. Federation is purely additive and inert until an operator opts in
// on both ends.
package federation

// ReservedMetadataPrefix is the Envelope.Metadata key namespace ADR-0002 §6
// reserves for federation use. Federation v1 sets none of these keys and
// ignores them if present; a future signed-envelope ADR (only if the
// point-to-point topology is relaxed) populates fed.sig / fed.sig.alg /
// fed.sig.kid / fed.sig.ts / fed.nonce without any schema change. Application
// code MUST NOT use keys under this prefix.
const ReservedMetadataPrefix = "fed."

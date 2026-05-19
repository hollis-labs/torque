package federation

import (
	"errors"
	"fmt"

	gomsg "github.com/hollis-labs/go-messaging"
)

// Op is a federated operation, used in authorization and audit logs.
type Op string

const (
	OpSend    Op = "send"
	OpGet     Op = "get"
	OpThread  Op = "thread"
	OpConsume Op = "consume"
	OpCancel  Op = "cancel"
)

// errNotHomed — the governing authority of the call is not one this install
// homes (ADR-0002 §4 routing invariant). A peer is attempting to use this
// install as a relay. Maps to HTTP 404 (unroutable, ErrNotFound-class).
var errNotHomed = errors.New("federation: governing authority not homed by this install")

// errForbidden — the calling peer is not authorized to act for the authority
// in question (ADR-0002 §4 trust boundary). Impersonation, or access to a
// thread the peer is not a party to. Maps to HTTP 403.
var errForbidden = errors.New("federation: peer not authorized for authority")

// authorizer enforces the ADR-0002 §4 authorization model server-side. It is
// immutable after construction and safe for concurrent use.
type authorizer struct {
	local map[string]struct{} // localAuthorities(S)
}

func newAuthorizer(localAuthorities []string) *authorizer {
	m := make(map[string]struct{}, len(localAuthorities))
	for _, a := range localAuthorities {
		if a != "" {
			m[a] = struct{}{}
		}
	}
	return &authorizer{local: m}
}

// homes reports whether authority is one this install homes.
func (a *authorizer) homes(authority string) bool {
	if authority == "" {
		return false
	}
	_, ok := a.local[authority]
	return ok
}

// count reports how many local authorities are declared (for the startup log).
func (a *authorizer) count() int { return len(a.local) }

// authorizeSend enforces ADR-0002 §4 for a Send:
//
//   - Routing invariant — the recipient (To) authority MUST be one this
//     install homes; otherwise the peer is trying to relay mail through us.
//   - Trust boundary — the envelope's From.Authority MUST be one the calling
//     peer is registered authoritative for; otherwise the peer is forging
//     mail that appears to originate from an authority it does not own.
//
// Both checks run before any Store mutation; either failure rejects the call.
func (a *authorizer) authorizeSend(peer *Peer, env gomsg.Envelope) error {
	if !a.homes(env.To.Authority) {
		return fmt.Errorf("%w: to=%q", errNotHomed, env.To.Authority)
	}
	if !peer.IsAuthoritative(env.From.Authority) {
		return fmt.Errorf("%w: peer %q may not originate mail from=%q (authoritative for %v)",
			errForbidden, peer.Label, env.From.Authority, peer.Authorities())
	}
	return nil
}

// authorizeEnvelopeAccess enforces ADR-0002 §4 for Get/Thread/Consume/Cancel
// against one target envelope:
//
//   - Routing invariant — this install must home at least one party of the
//     envelope; otherwise it is not the home for it and the call is
//     unroutable.
//   - Trust boundary — the calling peer must itself be a party: authoritative
//     for the From or the To authority. A peer may inspect and manage
//     lifecycle only for envelopes it is a party to, and no others.
func (a *authorizer) authorizeEnvelopeAccess(op Op, peer *Peer, env gomsg.Envelope) error {
	if !a.homes(env.From.Authority) && !a.homes(env.To.Authority) {
		return fmt.Errorf("%w: %s envelope %q (from=%q to=%q)",
			errNotHomed, op, env.ID, env.From.Authority, env.To.Authority)
	}
	if !peer.IsAuthoritative(env.From.Authority) && !peer.IsAuthoritative(env.To.Authority) {
		return fmt.Errorf("%w: peer %q is not a party to %s envelope %q",
			errForbidden, peer.Label, op, env.ID)
	}
	return nil
}

// governingAuthority returns the authority a call is logged against
// (ADR-0002 §5 audit record): the recipient authority for a Send, otherwise
// whichever party of the target envelope this install homes.
func (a *authorizer) governingAuthority(op Op, env gomsg.Envelope) string {
	if op == OpSend {
		return env.To.Authority
	}
	if a.homes(env.To.Authority) {
		return env.To.Authority
	}
	return env.From.Authority
}

package federation

import (
	"errors"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
)

// peerFor builds a Peer authoritative for the given authorities (white-box).
func peerFor(label string, authorities ...string) *Peer {
	m := make(map[string]struct{}, len(authorities))
	for _, a := range authorities {
		m[a] = struct{}{}
	}
	return &Peer{Label: label, authorities: m}
}

func envFromTo(from, to string) gomsg.Envelope {
	return gomsg.Envelope{
		ID:   "env-1",
		Kind: gomsg.MsgKindRequest,
		From: gomsg.Address{Kind: gomsg.KindAgent, Authority: from, ID: "a"},
		To:   gomsg.Address{Kind: gomsg.KindAgent, Authority: to, ID: "b"},
	}
}

func TestAuthorizeSend(t *testing.T) {
	a := newAuthorizer([]string{"torque-a"})
	peerB := peerFor("peer-b", "torque-b")

	if err := a.authorizeSend(peerB, envFromTo("torque-b", "torque-a")); err != nil {
		t.Errorf("legitimate Send rejected: %v", err)
	}
	// Impersonation: peer-b forges From=torque-c → trust boundary, 403.
	err := a.authorizeSend(peerB, envFromTo("torque-c", "torque-a"))
	if !errors.Is(err, errForbidden) {
		t.Errorf("From-forgery: got %v, want errForbidden", err)
	}
	// Relay abuse: To=torque-x is not homed here → routing invariant, 404.
	err = a.authorizeSend(peerB, envFromTo("torque-b", "torque-x"))
	if !errors.Is(err, errNotHomed) {
		t.Errorf("relay attempt: got %v, want errNotHomed", err)
	}
}

func TestAuthorizeEnvelopeAccess(t *testing.T) {
	a := newAuthorizer([]string{"torque-a"})
	peerB := peerFor("peer-b", "torque-b")
	peerD := peerFor("peer-d", "torque-d")

	// peer-b is a party (From) to an envelope this install homes (To).
	if err := a.authorizeEnvelopeAccess(OpGet, peerB, envFromTo("torque-b", "torque-a")); err != nil {
		t.Errorf("party access rejected: %v", err)
	}
	// peer-d is not a party → 403.
	err := a.authorizeEnvelopeAccess(OpGet, peerD, envFromTo("torque-b", "torque-a"))
	if !errors.Is(err, errForbidden) {
		t.Errorf("non-party access: got %v, want errForbidden", err)
	}
	// Neither party is homed here → not-homed, 404.
	err = a.authorizeEnvelopeAccess(OpCancel, peerB, envFromTo("torque-b", "torque-x"))
	if !errors.Is(err, errNotHomed) {
		t.Errorf("non-homed envelope: got %v, want errNotHomed", err)
	}
}

func TestGoverningAuthority(t *testing.T) {
	a := newAuthorizer([]string{"torque-a"})
	env := envFromTo("torque-b", "torque-a")
	if got := a.governingAuthority(OpSend, env); got != "torque-a" {
		t.Errorf("Send governing authority = %q, want torque-a", got)
	}
	if got := a.governingAuthority(OpGet, env); got != "torque-a" {
		t.Errorf("Get governing authority = %q, want the locally-homed party torque-a", got)
	}
}

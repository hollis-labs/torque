package federation

import (
	"strings"
	"testing"
)

func fp(n byte) string { return strings.Repeat(string("0123456789abcdef"[n%16]), 64) }

func TestNewPeerRegistryLookupAndAuthority(t *testing.T) {
	reg, err := NewPeerRegistry([]PeerConfig{
		{Label: "peer-b", Fingerprints: []string{fp(1)}, Authorities: []string{"torque-b"}},
	})
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	peer, ok := reg.Lookup(fp(1))
	if !ok {
		t.Fatal("Lookup miss for a registered fingerprint")
	}
	if !peer.IsAuthoritative("torque-b") {
		t.Error("peer should be authoritative for torque-b")
	}
	if peer.IsAuthoritative("torque-x") {
		t.Error("peer must NOT be authoritative for an unregistered authority")
	}
	if _, ok := reg.Lookup(fp(9)); ok {
		t.Error("Lookup should miss for an unregistered fingerprint")
	}
}

func TestNewPeerRegistryRotationOverlap(t *testing.T) {
	// ADR-0002 §3: a peer mid-rotation pins two fingerprints; both resolve to
	// the same peer.
	reg, err := NewPeerRegistry([]PeerConfig{
		{Label: "peer-b", Fingerprints: []string{fp(1), fp(2)}, Authorities: []string{"torque-b"}},
	})
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	p1, _ := reg.Lookup(fp(1))
	p2, _ := reg.Lookup(fp(2))
	if p1 == nil || p2 == nil || p1 != p2 {
		t.Fatal("both rotation fingerprints must resolve to the same *Peer")
	}
	if reg.PeerCount() != 1 || reg.PinCount() != 2 {
		t.Errorf("PeerCount=%d PinCount=%d, want 1 and 2", reg.PeerCount(), reg.PinCount())
	}
}

func TestNewPeerRegistryRejectsBadConfig(t *testing.T) {
	cases := map[string][]PeerConfig{
		"no label":        {{Fingerprints: []string{fp(1)}, Authorities: []string{"a"}}},
		"no fingerprint":  {{Label: "x", Authorities: []string{"a"}}},
		"no authority":    {{Label: "x", Fingerprints: []string{fp(1)}}},
		"bad fingerprint": {{Label: "x", Fingerprints: []string{"nothex"}, Authorities: []string{"a"}}},
		"fingerprint claimed twice": {
			{Label: "x", Fingerprints: []string{fp(1)}, Authorities: []string{"a"}},
			{Label: "y", Fingerprints: []string{fp(1)}, Authorities: []string{"b"}},
		},
	}
	for name, peers := range cases {
		if _, err := NewPeerRegistry(peers); err == nil {
			t.Errorf("%s: NewPeerRegistry accepted invalid config", name)
		}
	}
}

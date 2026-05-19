package federation

import (
	"fmt"
	"sort"
)

// Peer is a registered federation peer (ADR-0002 §4): a human label and the
// set of authorities this install permits the peer to act for —
// peerAuthorities(S, P). A peer with an empty authority set is unauthorized
// for every federated call.
type Peer struct {
	// Label is the operator-facing name of the peer, used in audit logs.
	Label string

	authorities map[string]struct{}
}

// IsAuthoritative reports whether this peer is registered as authoritative
// for authority. This is the predicate behind the ADR-0002 §4 trust boundary:
// a peer may only originate mail for — and inspect threads of — authorities
// it is authoritative for.
func (p *Peer) IsAuthoritative(authority string) bool {
	if p == nil || authority == "" {
		return false
	}
	_, ok := p.authorities[authority]
	return ok
}

// Authorities returns the peer's authorities, sorted — for audit logs and
// tests.
func (p *Peer) Authorities() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.authorities))
	for a := range p.authorities {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// PeerRegistry maps a pinned client-certificate SHA-256 fingerprint to the
// Peer it identifies (ADR-0002 §8.2). A peer may pin more than one
// fingerprint — that is how zero-downtime certificate rotation works (§3):
// every fingerprint a peer pins resolves to the same *Peer.
//
// It is built once from config and is read-only thereafter, hence safe for
// concurrent use.
type PeerRegistry struct {
	byFingerprint map[string]*Peer
	peers         []*Peer
}

// NewPeerRegistry builds a PeerRegistry from peer config entries. It fails if
// a peer has no label, no fingerprint, or no authority, if a fingerprint is
// malformed, or if one fingerprint is claimed by two peers (an ambiguous pin
// is a configuration error, not something to resolve arbitrarily).
func NewPeerRegistry(peers []PeerConfig) (*PeerRegistry, error) {
	reg := &PeerRegistry{byFingerprint: make(map[string]*Peer)}
	for i, pc := range peers {
		if pc.Label == "" {
			return nil, fmt.Errorf("peer #%d: label is required", i)
		}
		if len(pc.Fingerprints) == 0 {
			return nil, fmt.Errorf("peer %q: at least one pinned fingerprint is required", pc.Label)
		}
		if len(pc.Authorities) == 0 {
			return nil, fmt.Errorf("peer %q: at least one authority is required", pc.Label)
		}

		authorities := make(map[string]struct{}, len(pc.Authorities))
		for _, a := range pc.Authorities {
			if a == "" {
				return nil, fmt.Errorf("peer %q: authority entries must be non-empty", pc.Label)
			}
			authorities[a] = struct{}{}
		}
		peer := &Peer{Label: pc.Label, authorities: authorities}

		for _, fp := range pc.Fingerprints {
			norm, err := ValidateFingerprint(fp)
			if err != nil {
				return nil, fmt.Errorf("peer %q: %w", pc.Label, err)
			}
			if existing, dup := reg.byFingerprint[norm]; dup {
				return nil, fmt.Errorf("peer %q: fingerprint %s is already pinned to peer %q",
					pc.Label, norm, existing.Label)
			}
			reg.byFingerprint[norm] = peer
		}
		reg.peers = append(reg.peers, peer)
	}
	return reg, nil
}

// Lookup resolves a presented-certificate fingerprint to its Peer. The
// fingerprint is normalized first, so a caller may pass either form.
func (r *PeerRegistry) Lookup(fingerprint string) (*Peer, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.byFingerprint[NormalizeFingerprint(fingerprint)]
	return p, ok
}

// PeerCount reports the number of distinct registered peers.
func (r *PeerRegistry) PeerCount() int {
	if r == nil {
		return 0
	}
	return len(r.peers)
}

// PinCount reports the number of pinned fingerprints across all peers (a peer
// mid-rotation contributes more than one).
func (r *PeerRegistry) PinCount() int {
	if r == nil {
		return 0
	}
	return len(r.byFingerprint)
}

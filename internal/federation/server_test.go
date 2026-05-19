package federation

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/memstore"

	tqmsg "github.com/hollis-labs/torque/internal/messaging"
)

// startServer brings up the federation mTLS surface over a fresh in-memory
// Store, behind an httptest TLS listener configured with ServerTLSConfig.
func startServer(t *testing.T, serverID tls.Certificate, peers *PeerRegistry, localAuths []string) (*httptest.Server, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := NewServer(store, peers, localAuths)
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.TLS = ServerTLSConfig(serverID, peers)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts, store
}

// clientStore builds the outbound federation HTTPStore: an mTLS client
// presenting clientID, pinning the listed server fingerprints.
func clientStore(t *testing.T, url string, clientID tls.Certificate, serverPins ...string) *tqmsg.HTTPStore {
	t.Helper()
	c, err := ClientHTTPClient(clientID, serverPins)
	if err != nil {
		t.Fatalf("ClientHTTPClient: %v", err)
	}
	hs, err := tqmsg.NewHTTPStore(url, tqmsg.WithHTTPClient(c))
	if err != nil {
		t.Fatalf("NewHTTPStore: %v", err)
	}
	return hs
}

func registry(t *testing.T, entries ...PeerConfig) *PeerRegistry {
	t.Helper()
	r, err := NewPeerRegistry(entries)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	return r
}

func msg(from, to string) gomsg.Envelope {
	return gomsg.Envelope{
		Kind:    gomsg.MsgKindRequest,
		From:    gomsg.Address{Kind: gomsg.KindAgent, Authority: from, ID: "agent-1"},
		To:      gomsg.Address{Kind: gomsg.KindAgent, Authority: to, ID: "agent-2"},
		Payload: json.RawMessage(`{"hello":"world"}`),
	}
}

// TestFederationHopHappyPath — a pinned peer sends mail it is authoritative
// for, to an authority this install homes, and reads it back. ADR-0002 §8.10.
func TestFederationHopHappyPath(t *testing.T) {
	serverID, clientID := validIdentity(t), validIdentity(t)
	peers := registry(t, PeerConfig{
		Label: "peer-b", Fingerprints: []string{fingerprintOf(clientID)}, Authorities: []string{"torque-b"},
	})
	ts, _ := startServer(t, serverID, peers, []string{"torque-a"})
	cs := clientStore(t, ts.URL, clientID, fingerprintOf(serverID))
	ctx := context.Background()

	out, err := cs.Send(ctx, msg("torque-b", "torque-a"))
	if err != nil {
		t.Fatalf("Send over a pinned hop: %v", err)
	}
	if out.ID == "" {
		t.Fatal("Send returned an envelope with no Store-assigned ID")
	}
	got, err := cs.Get(ctx, out.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != out.ID || got.From.Authority != "torque-b" {
		t.Errorf("Get round-trip mismatch: %+v", got)
	}
}

// TestFederationRejectsImpersonation — a peer may not originate mail claiming
// a From.Authority it is not registered for (trust boundary, 403). ADR §8.10.
func TestFederationRejectsImpersonation(t *testing.T) {
	serverID, clientID := validIdentity(t), validIdentity(t)
	peers := registry(t, PeerConfig{
		Label: "peer-b", Fingerprints: []string{fingerprintOf(clientID)}, Authorities: []string{"torque-b"},
	})
	ts, _ := startServer(t, serverID, peers, []string{"torque-a"})
	cs := clientStore(t, ts.URL, clientID, fingerprintOf(serverID))

	_, err := cs.Send(context.Background(), msg("torque-c", "torque-a"))
	if !errors.Is(err, gomsg.ErrStoreUnavailable) {
		t.Fatalf("forged From.Authority: got %v, want ErrStoreUnavailable (403)", err)
	}
}

// TestFederationRejectsRelay — a peer may not push mail for an authority this
// install does not home (routing invariant, 404). ADR §8.10.
func TestFederationRejectsRelay(t *testing.T) {
	serverID, clientID := validIdentity(t), validIdentity(t)
	peers := registry(t, PeerConfig{
		Label: "peer-b", Fingerprints: []string{fingerprintOf(clientID)}, Authorities: []string{"torque-b"},
	})
	ts, _ := startServer(t, serverID, peers, []string{"torque-a"})
	cs := clientStore(t, ts.URL, clientID, fingerprintOf(serverID))

	_, err := cs.Send(context.Background(), msg("torque-b", "torque-x"))
	if !errors.Is(err, gomsg.ErrNotFound) {
		t.Fatalf("relay attempt: got %v, want ErrNotFound (404)", err)
	}
}

// TestFederationRejectsUnpinnedCert — a client whose certificate is not in the
// peer registry fails the mTLS handshake. ADR §8.10.
func TestFederationRejectsUnpinnedCert(t *testing.T) {
	serverID, pinnedClient, strangerClient := validIdentity(t), validIdentity(t), validIdentity(t)
	peers := registry(t, PeerConfig{
		Label: "peer-b", Fingerprints: []string{fingerprintOf(pinnedClient)}, Authorities: []string{"torque-b"},
	})
	ts, _ := startServer(t, serverID, peers, []string{"torque-a"})
	cs := clientStore(t, ts.URL, strangerClient, fingerprintOf(serverID))

	_, err := cs.Send(context.Background(), msg("torque-b", "torque-a"))
	if !errors.Is(err, gomsg.ErrStoreUnavailable) {
		t.Fatalf("unpinned client cert: got %v, want ErrStoreUnavailable", err)
	}
}

// TestFederationRejectsExpiredCert — a pinned but expired client certificate
// is rejected at the handshake (NotAfter honored). ADR §8.10.
func TestFederationRejectsExpiredCert(t *testing.T) {
	serverID, expired := validIdentity(t), expiredIdentity(t)
	peers := registry(t, PeerConfig{
		Label: "peer-b", Fingerprints: []string{fingerprintOf(expired)}, Authorities: []string{"torque-b"},
	})
	ts, _ := startServer(t, serverID, peers, []string{"torque-a"})
	cs := clientStore(t, ts.URL, expired, fingerprintOf(serverID))

	_, err := cs.Send(context.Background(), msg("torque-b", "torque-a"))
	if !errors.Is(err, gomsg.ErrStoreUnavailable) {
		t.Fatalf("expired client cert: got %v, want ErrStoreUnavailable", err)
	}
}

// TestFederationCertRotationOverlap — a peer pinning two fingerprints (old +
// new) is reachable on either certificate, the zero-downtime rotation window.
// ADR-0002 §3, §8.10.
func TestFederationCertRotationOverlap(t *testing.T) {
	serverID, oldClient, newClient := validIdentity(t), validIdentity(t), validIdentity(t)
	peers := registry(t, PeerConfig{
		Label:        "peer-b",
		Fingerprints: []string{fingerprintOf(oldClient), fingerprintOf(newClient)},
		Authorities:  []string{"torque-b"},
	})
	ts, _ := startServer(t, serverID, peers, []string{"torque-a"})
	ctx := context.Background()

	for name, id := range map[string]tls.Certificate{"old": oldClient, "new": newClient} {
		cs := clientStore(t, ts.URL, id, fingerprintOf(serverID))
		if _, err := cs.Send(ctx, msg("torque-b", "torque-a")); err != nil {
			t.Errorf("rotation overlap: %s certificate rejected: %v", name, err)
		}
	}
}

// TestFederationRejectsNonPartyAccess — a peer may not Get an envelope it is
// not a party to (trust boundary on reads, 403).
func TestFederationRejectsNonPartyAccess(t *testing.T) {
	serverID, clientB, clientD := validIdentity(t), validIdentity(t), validIdentity(t)
	peers := registry(t,
		PeerConfig{Label: "peer-b", Fingerprints: []string{fingerprintOf(clientB)}, Authorities: []string{"torque-b"}},
		PeerConfig{Label: "peer-d", Fingerprints: []string{fingerprintOf(clientD)}, Authorities: []string{"torque-d"}},
	)
	ts, _ := startServer(t, serverID, peers, []string{"torque-a"})
	ctx := context.Background()

	csB := clientStore(t, ts.URL, clientB, fingerprintOf(serverID))
	out, err := csB.Send(ctx, msg("torque-b", "torque-a"))
	if err != nil {
		t.Fatalf("peer-b Send: %v", err)
	}
	csD := clientStore(t, ts.URL, clientD, fingerprintOf(serverID))
	if _, err := csD.Get(ctx, out.ID); !errors.Is(err, gomsg.ErrStoreUnavailable) {
		t.Fatalf("non-party Get: got %v, want ErrStoreUnavailable (403)", err)
	}
}

// TestFederationThreadConsumeCancel — the reply-correlation lifecycle calls
// work for a party peer.
func TestFederationThreadConsumeCancel(t *testing.T) {
	serverID, clientID := validIdentity(t), validIdentity(t)
	peers := registry(t, PeerConfig{
		Label: "peer-b", Fingerprints: []string{fingerprintOf(clientID)}, Authorities: []string{"torque-b"},
	})
	ts, _ := startServer(t, serverID, peers, []string{"torque-a"})
	cs := clientStore(t, ts.URL, clientID, fingerprintOf(serverID))
	ctx := context.Background()

	first := msg("torque-b", "torque-a")
	first.ThreadID = "thread-1"
	sent, err := cs.Send(ctx, first)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	second := msg("torque-b", "torque-a")
	second.ThreadID = "thread-1"
	if _, err := cs.Send(ctx, second); err != nil {
		t.Fatalf("Send #2: %v", err)
	}

	thread, err := cs.Thread(ctx, "thread-1", gomsg.Filter{})
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(thread) != 2 {
		t.Errorf("Thread returned %d envelopes, want 2", len(thread))
	}
	if err := cs.Consume(ctx, sent.ID, first.To); err != nil {
		t.Errorf("Consume: %v", err)
	}
	if err := cs.Cancel(ctx, sent.ID); err != nil {
		t.Errorf("Cancel: %v", err)
	}
}

// TestEnableStandalone — with no config file, Enable returns a nil
// *Federation: federation is fully disabled, no listener (ADR-0002 standalone
// guarantee).
func TestEnableStandalone(t *testing.T) {
	fed, err := Enable(filepath.Join(t.TempDir(), "absent.json"), memstore.New())
	if err != nil {
		t.Fatalf("Enable with no config: %v", err)
	}
	if fed != nil {
		t.Fatal("absent config must yield a nil *Federation (no listener)")
	}
}

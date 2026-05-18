package messaging_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/memstore"

	"github.com/hollis-labs/torque/internal/messaging"
)

// TestNewRegistryValidation rejects malformed routes at construction.
func TestNewRegistryValidation(t *testing.T) {
	cases := []struct {
		name   string
		routes []messaging.ForeignRoute
	}{
		{"empty authority", []messaging.ForeignRoute{{Endpoint: "https://peer.example"}}},
		{"empty endpoint", []messaging.ForeignRoute{{Authority: "peer-a"}}},
		{"bad endpoint", []messaging.ForeignRoute{{Authority: "peer-a", Endpoint: "peer.example:8443"}}},
		{"duplicate authority", []messaging.ForeignRoute{
			{Authority: "peer-a", Endpoint: "https://a.example"},
			{Authority: "peer-a", Endpoint: "https://a2.example"},
		}},
	}
	for _, tc := range cases {
		if _, err := messaging.NewRegistry(tc.routes...); err == nil {
			t.Errorf("%s: want error, got nil", tc.name)
		}
	}

	if _, err := messaging.NewRegistry(
		messaging.ForeignRoute{Authority: "peer-a", Endpoint: "https://a.example:8443"},
		messaging.ForeignRoute{Authority: "peer-b", Endpoint: "https://b.example:8443"},
	); err != nil {
		t.Fatalf("NewRegistry(valid): unexpected error %v", err)
	}
}

// TestRegistryEmptyIsStandalone: an empty Registry is the standalone default —
// no routes, an empty foreign-store map.
func TestRegistryEmptyIsStandalone(t *testing.T) {
	reg, err := messaging.NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry(): %v", err)
	}
	if reg.Len() != 0 {
		t.Errorf("Len: want 0, got %d", reg.Len())
	}
	if got := reg.ForeignStores(); len(got) != 0 {
		t.Errorf("ForeignStores: want empty, got %v", got)
	}
}

// TestRegistryLookup: Authorities is sorted; Route resolves registered
// authorities and reports misses.
func TestRegistryLookup(t *testing.T) {
	reg, err := messaging.NewRegistry(
		messaging.ForeignRoute{Authority: "peer-z", Endpoint: "https://z.example"},
		messaging.ForeignRoute{Authority: "peer-a", Endpoint: "https://a.example", Pins: []string{"sha256:abc"}},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	if got := reg.Authorities(); !reflect.DeepEqual(got, []string{"peer-a", "peer-z"}) {
		t.Errorf("Authorities: want sorted [peer-a peer-z], got %v", got)
	}
	rt, ok := reg.Route("peer-a")
	if !ok || rt.Endpoint != "https://a.example" || len(rt.Pins) != 1 {
		t.Errorf("Route(peer-a): unexpected %+v ok=%v", rt, ok)
	}
	if _, ok := reg.Route("peer-unknown"); ok {
		t.Error("Route(peer-unknown): want miss, got hit")
	}

	stores := reg.ForeignStores()
	if len(stores) != 2 {
		t.Errorf("ForeignStores: want 2, got %d", len(stores))
	}
	// The returned map is a fresh copy — mutating it must not affect the registry.
	delete(stores, "peer-a")
	if len(reg.ForeignStores()) != 2 {
		t.Error("ForeignStores returned a map aliased to registry state")
	}
}

// TestNewRouterWithRegistry is the consumption-seam test: a Router built from a
// Registry routes a foreign authority to its peer endpoint and a local
// authority to the local Store.
func TestNewRouterWithRegistry(t *testing.T) {
	ctx := context.Background()

	localMem := memstore.New()
	peerMem := memstore.New()
	peerSrv := fedSurface(t, peerMem)

	reg, err := messaging.NewRegistry(
		messaging.ForeignRoute{Authority: "peer-auth", Endpoint: peerSrv.URL},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	router, err := messaging.NewRouterWithRegistry(localMem, []string{"local-auth"}, reg)
	if err != nil {
		t.Fatalf("NewRouterWithRegistry: %v", err)
	}

	// A Send to the foreign authority lands on the peer, not locally.
	foreign, err := router.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("local-auth", "agent-1"),
		To:   addr("peer-auth", "agent-2"),
	})
	if err != nil {
		t.Fatalf("Send to foreign authority: %v", err)
	}
	if _, err := peerMem.Get(ctx, foreign.ID); err != nil {
		t.Errorf("foreign envelope not on the peer Store: %v", err)
	}
	if _, err := localMem.Get(ctx, foreign.ID); !errors.Is(err, gomsg.ErrNotFound) {
		t.Errorf("foreign envelope leaked into the local Store: %v", err)
	}

	// A Send to a local authority stays local.
	local, err := router.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("local-auth", "agent-1"),
		To:   addr("local-auth", "agent-3"),
	})
	if err != nil {
		t.Fatalf("Send to local authority: %v", err)
	}
	if _, err := localMem.Get(ctx, local.ID); err != nil {
		t.Errorf("local envelope not on the local Store: %v", err)
	}
}

// TestNewRouterWithRegistryNil: a nil Registry yields a standalone catch-all
// Router — every authority resolves locally.
func TestNewRouterWithRegistryNil(t *testing.T) {
	ctx := context.Background()
	localMem := memstore.New()

	router, err := messaging.NewRouterWithRegistry(localMem, nil, nil)
	if err != nil {
		t.Fatalf("NewRouterWithRegistry(nil): %v", err)
	}
	sent, err := router.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("anywhere", "a"),
		To:   addr("anywhere", "b"),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := localMem.Get(ctx, sent.ID); err != nil {
		t.Errorf("standalone Router did not route to the local Store: %v", err)
	}
}

// TestRegistryRouteClientIsUsed: a per-route *http.Client supplied on the
// ForeignRoute is the transport the route's Store dials with — this is the
// seam the federation-auth mTLS layer (ADR-0002) plugs an mTLS client into.
func TestRegistryRouteClientIsUsed(t *testing.T) {
	probe := &probeTransport{}
	reg, err := messaging.NewRegistry(messaging.ForeignRoute{
		Authority: "peer-auth",
		Endpoint:  "https://peer.example:8443",
		Client:    &http.Client{Transport: probe},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	_, _ = reg.ForeignStores()["peer-auth"].Get(context.Background(), "id")
	if !probe.called {
		t.Error("per-route Client was not used by the route's Store")
	}
}

// probeTransport records that RoundTrip was invoked, then fails the request.
type probeTransport struct{ called bool }

func (p *probeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	p.called = true
	return nil, errors.New("probe transport: no real peer")
}

package messaging_test

import (
	"context"
	"errors"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/memstore"
	"github.com/hollis-labs/go-messaging/messagingtest"

	"github.com/hollis-labs/torque/internal/messaging"
)

// addr is a terse Address constructor for tests.
func addr(authority, id string) gomsg.Address {
	return gomsg.Address{Kind: gomsg.KindAgent, Authority: authority, ID: id}
}

// TestRouterContract runs the canonical messaging.Store contract suite
// against a standalone Router (no foreign routes). It proves the standalone
// guarantee: with an empty route registry the Router is a transparent
// passthrough and behaves exactly as the bare SQLite Store.
func TestRouterContract(t *testing.T) {
	messagingtest.RunContract(t, func(t *testing.T) gomsg.Store {
		r, err := messaging.NewRouter(messaging.RouterConfig{
			Local: messaging.NewStore(newDB(t)),
		})
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}
		return r
	})
}

// TestRouterSendRoutesByAuthority: a Send is dispatched by the recipient
// (To) authority — local authority to the local Store, foreign authority to
// the registered foreign route.
func TestRouterSendRoutesByAuthority(t *testing.T) {
	ctx := context.Background()
	local := messaging.NewStore(newDB(t))
	peer := memstore.New()

	r, err := messaging.NewRouter(messaging.RouterConfig{
		Local:            local,
		LocalAuthorities: []string{"torque"},
		ForeignRoutes:    map[string]gomsg.Store{"peer": peer},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	toPeer, err := r.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("torque", "a"),
		To:   addr("peer", "b"),
	})
	if err != nil {
		t.Fatalf("Send to peer: %v", err)
	}
	toLocal, err := r.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("torque", "a"),
		To:   addr("torque", "c"),
	})
	if err != nil {
		t.Fatalf("Send to local: %v", err)
	}

	// The peer-addressed envelope landed in the foreign Store only.
	if _, err := peer.Get(ctx, toPeer.ID); err != nil {
		t.Errorf("peer-addressed envelope not in foreign Store: %v", err)
	}
	if _, err := local.Get(ctx, toPeer.ID); !errors.Is(err, gomsg.ErrNotFound) {
		t.Errorf("peer-addressed envelope leaked into local Store: %v", err)
	}

	// The local-addressed envelope landed in the local Store only.
	if _, err := local.Get(ctx, toLocal.ID); err != nil {
		t.Errorf("local-addressed envelope not in local Store: %v", err)
	}
	if _, err := peer.Get(ctx, toLocal.ID); !errors.Is(err, gomsg.ErrNotFound) {
		t.Errorf("local-addressed envelope leaked into foreign Store: %v", err)
	}
}

// TestRouterInboxRoutesByRecipientAuthority: Inbox is dispatched by the
// recipient authority.
func TestRouterInboxRoutesByRecipientAuthority(t *testing.T) {
	ctx := context.Background()
	peer := memstore.New()
	r, err := messaging.NewRouter(messaging.RouterConfig{
		Local:         messaging.NewStore(newDB(t)),
		ForeignRoutes: map[string]gomsg.Store{"peer": peer},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	want, err := peer.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("peer", "src"),
		To:   addr("peer", "dst"),
	})
	if err != nil {
		t.Fatalf("seed peer: %v", err)
	}

	got, err := r.Inbox(ctx, addr("peer", "dst"), gomsg.Filter{})
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(got) != 1 || got[0].ID != want.ID {
		t.Fatalf("Inbox did not route to foreign Store: got %d rows", len(got))
	}
}

// TestRouterConsumeRoutesByRecipientAuthority: Consume is dispatched by the
// recipient authority — proven by Consuming an ID that exists only in the
// foreign Store, and by an unknown ID surfacing the foreign Store's
// ErrNotFound (not the local Store's).
func TestRouterConsumeRoutesByRecipientAuthority(t *testing.T) {
	ctx := context.Background()
	peer := memstore.New()
	r, err := messaging.NewRouter(messaging.RouterConfig{
		Local:         messaging.NewStore(newDB(t)),
		ForeignRoutes: map[string]gomsg.Store{"peer": peer},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sent, err := peer.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("peer", "src"),
		To:   addr("peer", "dst"),
	})
	if err != nil {
		t.Fatalf("seed peer: %v", err)
	}
	if err := r.Consume(ctx, sent.ID, addr("peer", "dst")); err != nil {
		t.Errorf("Consume of foreign envelope: %v", err)
	}
	if err := r.Consume(ctx, "no-such-id", addr("peer", "dst")); !errors.Is(err, gomsg.ErrNotFound) {
		t.Errorf("Consume of unknown foreign id: want ErrNotFound, got %v", err)
	}
}

// TestRouterGetFallsBackToForeign: Get carries no authority, so the Router
// resolves it local-first then falls back to foreign routes.
func TestRouterGetFallsBackToForeign(t *testing.T) {
	ctx := context.Background()
	local := messaging.NewStore(newDB(t))
	peer := memstore.New()
	r, err := messaging.NewRouter(messaging.RouterConfig{
		Local:         local,
		ForeignRoutes: map[string]gomsg.Store{"peer": peer},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	foreign, err := peer.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("peer", "src"),
		To:   addr("peer", "dst"),
	})
	if err != nil {
		t.Fatalf("seed peer: %v", err)
	}

	got, err := r.Get(ctx, foreign.ID)
	if err != nil {
		t.Fatalf("Get of foreign envelope: %v", err)
	}
	if got.ID != foreign.ID {
		t.Errorf("Get returned wrong envelope: %s", got.ID)
	}
	if _, err := r.Get(ctx, "no-such-id"); !errors.Is(err, gomsg.ErrNotFound) {
		t.Errorf("Get of unknown id: want ErrNotFound, got %v", err)
	}
}

// TestRouterCancelFallsBackToForeign: Cancel also resolves local-first then
// foreign, and reports ErrNotFound only when no Store holds the envelope.
func TestRouterCancelFallsBackToForeign(t *testing.T) {
	ctx := context.Background()
	peer := memstore.New()
	r, err := messaging.NewRouter(messaging.RouterConfig{
		Local:         messaging.NewStore(newDB(t)),
		ForeignRoutes: map[string]gomsg.Store{"peer": peer},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sent, err := peer.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("peer", "src"),
		To:   addr("peer", "dst"),
	})
	if err != nil {
		t.Fatalf("seed peer: %v", err)
	}
	if err := r.Cancel(ctx, sent.ID); err != nil {
		t.Errorf("Cancel of foreign envelope: %v", err)
	}
	if err := r.Cancel(ctx, "no-such-id"); !errors.Is(err, gomsg.ErrNotFound) {
		t.Errorf("Cancel of unknown id: want ErrNotFound, got %v", err)
	}
}

// TestRouterThreadMergesLocalAndForeign: a thread may span Stores; Thread
// merges the local Store and every foreign route, de-duplicated and ordered.
func TestRouterThreadMergesLocalAndForeign(t *testing.T) {
	ctx := context.Background()
	peer := memstore.New()
	r, err := messaging.NewRouter(messaging.RouterConfig{
		Local:            messaging.NewStore(newDB(t)),
		LocalAuthorities: []string{"torque"},
		ForeignRoutes:    map[string]gomsg.Store{"peer": peer},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	const thread = "T-merge"
	mk := func(to gomsg.Address) {
		if _, err := r.Send(ctx, gomsg.Envelope{
			Kind:     gomsg.MsgKindNotice,
			ThreadID: thread,
			From:     addr("torque", "a"),
			To:       to,
		}); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	mk(addr("torque", "x")) // local
	mk(addr("peer", "y"))   // foreign
	mk(addr("torque", "z")) // local

	got, err := r.Thread(ctx, thread, gomsg.Filter{})
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Thread merge: want 3 envelopes, got %d", len(got))
	}
	seen := map[string]struct{}{}
	for i, e := range got {
		if _, dup := seen[e.ID]; dup {
			t.Errorf("Thread returned duplicate id %s", e.ID)
		}
		seen[e.ID] = struct{}{}
		if i > 0 && e.CreatedAt.Before(got[i-1].CreatedAt) {
			t.Errorf("Thread not chronologically ordered at index %d", i)
		}
	}
}

// TestRouterStrictModeRejectsUnroutableAuthority: with declared local
// authorities, an authority that is neither local nor foreign-routed is a
// hard error — no silent drop.
func TestRouterStrictModeRejectsUnroutableAuthority(t *testing.T) {
	ctx := context.Background()
	r, err := messaging.NewRouter(messaging.RouterConfig{
		Local:            messaging.NewStore(newDB(t)),
		LocalAuthorities: []string{"torque"},
		ForeignRoutes:    map[string]gomsg.Store{"peer": memstore.New()},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	_, err = r.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("torque", "a"),
		To:   addr("ghost", "b"),
	})
	if !errors.Is(err, messaging.ErrUnroutableAuthority) {
		t.Errorf("Send to unknown authority: want ErrUnroutableAuthority, got %v", err)
	}
	if _, err := r.Inbox(ctx, addr("ghost", "b"), gomsg.Filter{}); !errors.Is(err, messaging.ErrUnroutableAuthority) {
		t.Errorf("Inbox for unknown authority: want ErrUnroutableAuthority, got %v", err)
	}
}

// TestRouterCatchAllAcceptsAnyAuthority: with no declared local authorities
// (the standalone default) every non-foreign authority resolves local — the
// Router never produces ErrUnroutableAuthority.
func TestRouterCatchAllAcceptsAnyAuthority(t *testing.T) {
	ctx := context.Background()
	local := messaging.NewStore(newDB(t))
	r, err := messaging.NewRouter(messaging.RouterConfig{
		Local:         local,
		ForeignRoutes: map[string]gomsg.Store{"peer": memstore.New()},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sent, err := r.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: addr("torque", "a"),
		To:   addr("some-undeclared-authority", "b"),
	})
	if err != nil {
		t.Fatalf("Send under catch-all: %v", err)
	}
	if _, err := local.Get(ctx, sent.ID); err != nil {
		t.Errorf("catch-all envelope not served locally: %v", err)
	}
}

// TestRouterConstructionValidation covers the NewRouter guard rails.
func TestRouterConstructionValidation(t *testing.T) {
	t.Run("nil local", func(t *testing.T) {
		if _, err := messaging.NewRouter(messaging.RouterConfig{}); err == nil {
			t.Error("want error for nil Local")
		}
	})
	t.Run("nil foreign store", func(t *testing.T) {
		_, err := messaging.NewRouter(messaging.RouterConfig{
			Local:         messaging.NewStore(newDB(t)),
			ForeignRoutes: map[string]gomsg.Store{"peer": nil},
		})
		if err == nil {
			t.Error("want error for nil foreign Store")
		}
	})
	t.Run("authority both local and foreign", func(t *testing.T) {
		_, err := messaging.NewRouter(messaging.RouterConfig{
			Local:            messaging.NewStore(newDB(t)),
			LocalAuthorities: []string{"torque"},
			ForeignRoutes:    map[string]gomsg.Store{"torque": memstore.New()},
		})
		if err == nil {
			t.Error("want error for an authority that is both local and foreign")
		}
	})
}

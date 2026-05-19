package messaging_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/memstore"

	"github.com/hollis-labs/torque/internal/messaging"
)

// fedSurface is a test double for a peer install's federation HTTP surface
// (ADR-0002 §5). It mounts the five federated routes at
// /federation/v1/messages and backs them with an in-memory Store, so an
// HTTPStore can be exercised end-to-end without a real peer.
func fedSurface(t *testing.T, backing gomsg.Store) *httptest.Server {
	t.Helper()
	const prefix = "/federation/v1/messages"

	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	writeErr := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, gomsg.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case errors.Is(err, gomsg.ErrPresetLifecycle):
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		switch {
		case rest == "" || rest == "/": // Send
			var env gomsg.Envelope
			if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			out, err := backing.Send(r.Context(), env)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, out)
		case strings.HasPrefix(rest, "/thread/"): // Thread
			threadID := strings.TrimPrefix(rest, "/thread/")
			envs, err := backing.Thread(r.Context(), threadID, gomsg.Filter{})
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"messages": envs})
		case strings.HasSuffix(rest, "/consume"): // Consume
			id := strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/consume")
			var body struct {
				Recipient string `json:"recipient"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			addr, err := gomsg.ParseURN(body.Recipient)
			if err != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
			if err := backing.Consume(r.Context(), id, addr); err != nil {
				writeErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(rest, "/cancel"): // Cancel
			id := strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/cancel")
			if err := backing.Cancel(r.Context(), id); err != nil {
				writeErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default: // Get /{id}
			id := strings.TrimPrefix(rest, "/")
			env, err := backing.Get(r.Context(), id)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, env)
		}
	})

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func newHTTPStore(t *testing.T, endpoint string) *messaging.HTTPStore {
	t.Helper()
	s, err := messaging.NewHTTPStore(endpoint)
	if err != nil {
		t.Fatalf("NewHTTPStore(%q): %v", endpoint, err)
	}
	return s
}

// TestHTTPStoreRoundTrip exercises the federated operations against a peer
// surface: Send → Get, Thread, Consume, Cancel.
func TestHTTPStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	srv := fedSurface(t, memstore.New())
	store := newHTTPStore(t, srv.URL)

	env := gomsg.Envelope{
		Kind:     gomsg.MsgKindNotice,
		From:     addr("peer-a", "agent-1"),
		To:       addr("peer-b", "agent-2"),
		ThreadID: "thread-xyz",
	}
	sent, err := store.Send(ctx, env)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.ID == "" {
		t.Fatal("Send: peer did not assign an ID")
	}

	got, err := store.Get(ctx, sent.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != sent.ID || got.ThreadID != "thread-xyz" {
		t.Fatalf("Get: round-trip mismatch: %+v", got)
	}

	thread, err := store.Thread(ctx, "thread-xyz", gomsg.Filter{})
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(thread) != 1 || thread[0].ID != sent.ID {
		t.Fatalf("Thread: want 1 envelope %s, got %+v", sent.ID, thread)
	}

	if err := store.Consume(ctx, sent.ID, env.To); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if err := store.Cancel(ctx, sent.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
}

// TestHTTPStoreGetNotFound: a 404 from the peer maps to messaging.ErrNotFound.
func TestHTTPStoreGetNotFound(t *testing.T) {
	srv := fedSurface(t, memstore.New())
	store := newHTTPStore(t, srv.URL)

	_, err := store.Get(context.Background(), "does-not-exist")
	if !errors.Is(err, gomsg.ErrNotFound) {
		t.Fatalf("Get of absent envelope: want ErrNotFound, got %v", err)
	}
}

// TestHTTPStoreSendPresetLifecycle: a caller-set lifecycle field is rejected
// client-side with ErrPresetLifecycle — no request reaches the peer.
func TestHTTPStoreSendPresetLifecycle(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	store := newHTTPStore(t, srv.URL)

	now := time.Now()
	_, err := store.Send(context.Background(), gomsg.Envelope{
		Kind:        gomsg.MsgKindNotice,
		From:        addr("peer-a", "x"),
		To:          addr("peer-b", "y"),
		DeliveredAt: &now,
	})
	if !errors.Is(err, gomsg.ErrPresetLifecycle) {
		t.Fatalf("Send with preset DeliveredAt: want ErrPresetLifecycle, got %v", err)
	}
	if reached {
		t.Fatal("Send with preset lifecycle field must not reach the peer")
	}
}

// TestHTTPStoreErrorMapping checks the ADR-0002 §5 status→sentinel table.
func TestHTTPStoreErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, gomsg.ErrNotFound},
		{http.StatusUnprocessableEntity, gomsg.ErrPresetLifecycle},
		{http.StatusForbidden, gomsg.ErrStoreUnavailable},
		{http.StatusUnauthorized, gomsg.ErrStoreUnavailable},
		{http.StatusServiceUnavailable, gomsg.ErrStoreUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "boom"})
		}))
		store := newHTTPStore(t, srv.URL)
		_, err := store.Get(context.Background(), "id")
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: want %v, got %v", tc.status, tc.want, err)
		}
		srv.Close()
	}
}

// TestHTTPStoreUnreachablePeer: a transport failure surfaces as
// ErrStoreUnavailable, not an opaque dial error.
func TestHTTPStoreUnreachablePeer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := srv.URL
	srv.Close() // nothing is listening now

	store := newHTTPStore(t, endpoint)
	_, err := store.Get(context.Background(), "id")
	if !errors.Is(err, gomsg.ErrStoreUnavailable) {
		t.Fatalf("Get against a dead peer: want ErrStoreUnavailable, got %v", err)
	}
}

// TestHTTPStoreInboxSubscribeNotFederated: Inbox and Subscribe are never
// federated (ADR-0002 §4); both fail with ErrStoreUnavailable.
func TestHTTPStoreInboxSubscribeNotFederated(t *testing.T) {
	srv := fedSurface(t, memstore.New())
	store := newHTTPStore(t, srv.URL)

	if _, err := store.Inbox(context.Background(), addr("peer-b", "y"), gomsg.Filter{}); !errors.Is(err, gomsg.ErrStoreUnavailable) {
		t.Errorf("Inbox: want ErrStoreUnavailable, got %v", err)
	}
	if _, err := store.Subscribe(context.Background(), addr("peer-b", "y"), gomsg.Filter{}); !errors.Is(err, gomsg.ErrStoreUnavailable) {
		t.Errorf("Subscribe: want ErrStoreUnavailable, got %v", err)
	}
}

// TestNewHTTPStoreValidation rejects malformed endpoints at construction.
func TestNewHTTPStoreValidation(t *testing.T) {
	bad := []string{"", "   ", "peer.example:8443", "ftp://peer.example", "https://"}
	for _, e := range bad {
		if _, err := messaging.NewHTTPStore(e); err == nil {
			t.Errorf("NewHTTPStore(%q): want error, got nil", e)
		}
	}
	if _, err := messaging.NewHTTPStore("https://peer.example:8443"); err != nil {
		t.Errorf("NewHTTPStore(valid): unexpected error %v", err)
	}
}

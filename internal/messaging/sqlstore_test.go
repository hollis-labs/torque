package messaging_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/messagingtest"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/messaging"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "msg.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=temp_store(memory)&_pragma=mmap_size(30000000000)&_pragma=journal_size_limit(67108864)&_pragma=cache_size(-64000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := migrations.Run(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestSQLStoreContract runs the canonical messaging.Store contract suite
// against the SQLite-backed Store.
func TestSQLStoreContract(t *testing.T) {
	messagingtest.RunContract(t, func(t *testing.T) gomsg.Store {
		return messaging.NewStore(newDB(t))
	})
}

// TestSQLStore_GetReturnsCanceledFlag covers a clarification beyond the
// shared contract suite: Cancel sets canceled_at, but Get is still a
// valid call returning the envelope.
func TestSQLStore_CancelThenGetSucceeds(t *testing.T) {
	store := messaging.NewStore(newDB(t))
	ctx := context.Background()

	sent, err := store.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "a"},
		To:   gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(ctx, sent.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := store.Get(ctx, sent.ID); err != nil {
		t.Fatalf("get after cancel: %v", err)
	}
}

// TestSQLStore_CancelHidesFromInbox: a canceled envelope must not appear
// on a fresh recipient's Inbox, mirroring the lib's broker-side semantic.
func TestSQLStore_CancelHidesFromInbox(t *testing.T) {
	store := messaging.NewStore(newDB(t))
	ctx := context.Background()
	to := gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "victim"}

	sent, err := store.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice,
		From: gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "src"},
		To:   to,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(ctx, sent.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got, err := store.Inbox(ctx, to, gomsg.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("canceled envelope leaked into Inbox: %d rows", len(got))
	}
}

// TestSQLStore_PayloadAndMetadataRoundTrip confirms opaque payload bytes
// and metadata maps survive a Send/Get round-trip unchanged.
func TestSQLStore_PayloadAndMetadataRoundTrip(t *testing.T) {
	store := messaging.NewStore(newDB(t))
	ctx := context.Background()

	want := gomsg.Envelope{
		Kind:        gomsg.MsgKindRequest,
		Channel:     "ops",
		ThreadID:    "T-1",
		From:        gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "a", SubID: "x"},
		To:          gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "b"},
		Payload:     json.RawMessage(`{"hello":"world"}`),
		ContentType: "application/json",
		Metadata:    map[string]string{"trace": "abc", "tenant": "main"},
	}
	sent, err := store.Send(ctx, want)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, sent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != sent.ID || got.Kind != want.Kind || got.Channel != want.Channel {
		t.Errorf("scalar fields drift: got %+v", got)
	}
	if got.From != want.From || got.To != want.To {
		t.Errorf("address drift: got from=%v to=%v", got.From, got.To)
	}
	if string(got.Payload) != string(want.Payload) {
		t.Errorf("payload drift: %s", string(got.Payload))
	}
	if got.ContentType != want.ContentType {
		t.Errorf("content_type drift: %q", got.ContentType)
	}
	if got.Metadata["trace"] != "abc" || got.Metadata["tenant"] != "main" {
		t.Errorf("metadata drift: %v", got.Metadata)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at zero on Get")
	}
}

// TestSQLStore_ConcurrentSend exercises the -race gate: many goroutines
// Sending in parallel must produce N rows with distinct IDs and no Go
// data-race or SQLite write contention errors.
func TestSQLStore_ConcurrentSend(t *testing.T) {
	store := messaging.NewStore(newDB(t))
	ctx := context.Background()

	const writers = 8
	const perWriter = 16

	var wg sync.WaitGroup
	wg.Add(writers)
	idsCh := make(chan string, writers*perWriter)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				env := gomsg.Envelope{
					Kind: gomsg.MsgKindNotice,
					From: gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "writer"},
					To:   gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "sink"},
				}
				sent, err := store.Send(ctx, env)
				if err != nil {
					t.Errorf("writer %d send: %v", w, err)
					return
				}
				idsCh <- sent.ID
			}
		}(w)
	}
	wg.Wait()
	close(idsCh)

	seen := map[string]bool{}
	for id := range idsCh {
		if seen[id] {
			t.Errorf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != writers*perWriter {
		t.Errorf("got %d distinct IDs, want %d", len(seen), writers*perWriter)
	}
}

// TestSQLStore_ConcurrentInboxNoDoubleDeliver: when two goroutines both
// call Inbox for the same recipient, every envelope is returned at most
// once (atomic-delivery contract under contention).
func TestSQLStore_ConcurrentInboxNoDoubleDeliver(t *testing.T) {
	store := messaging.NewStore(newDB(t))
	ctx := context.Background()
	to := gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "lone"}

	const n = 32
	for i := 0; i < n; i++ {
		_, err := store.Send(ctx, gomsg.Envelope{
			Kind: gomsg.MsgKindNotice,
			From: gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "src"},
			To:   to,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan []string, 2)
	for r := 0; r < 2; r++ {
		go func() {
			defer wg.Done()
			envs, err := store.Inbox(ctx, to, gomsg.Filter{})
			if err != nil {
				t.Errorf("inbox: %v", err)
				return
			}
			ids := make([]string, len(envs))
			for i, e := range envs {
				ids[i] = e.ID
			}
			results <- ids
		}()
	}
	wg.Wait()
	close(results)

	seen := map[string]int{}
	total := 0
	for batch := range results {
		total += len(batch)
		for _, id := range batch {
			seen[id]++
		}
	}
	if total != n {
		t.Errorf("inbox returned %d total, want %d (atomic delivery violated)", total, n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("envelope %s delivered %d times, want 1", id, c)
		}
	}
}

// TestSQLStore_ContextCancelClosesSubscribe verifies the live channel
// closes promptly when the subscriber's context is canceled.
func TestSQLStore_ContextCancelClosesSubscribe(t *testing.T) {
	store := messaging.NewStore(newDB(t))
	ctx, cancel := context.WithCancel(context.Background())
	to := gomsg.Address{Kind: gomsg.KindAgent, Authority: "test", ID: "sub"}

	ch, err := store.Subscribe(ctx, to, gomsg.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel close, got value")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription channel did not close within 1s")
	}
}

// TestSQLStore_CancelMissingReturnsNotFound covers the contract sentinel
// path explicitly (the shared suite only tests the missing case once).
func TestSQLStore_CancelMissingReturnsNotFound(t *testing.T) {
	store := messaging.NewStore(newDB(t))
	if err := store.Cancel(context.Background(), "no-such"); !errors.Is(err, gomsg.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

package hubrelay_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/mesh/eventbus"
	"github.com/hollis-labs/libs/mesh/eventbus/relay"
	"github.com/hollis-labs/torque/internal/hubrelay"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTasks(t *testing.T, store *sqlstore.Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: id, Title: "task " + id, Priority: 2}))
		require.NoError(t, store.TransitionTask(id, "doing"))
	}
}

func TestItemClearsWhatTheHubOwnsOnACopy(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	newTasks(t, store, "CW-HUB-1")
	events, err := store.OutboxAfter(t.Context(), 0, 10)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	ev := events[0]
	ev.Event.Actor = &runtimeevents.Actor{URN: "msg://agent/x/y", Kind: "agent"}
	item, err := hubrelay.Item(ev)
	require.NoError(t, err)
	assert.Equal(t, ev.Event.ID, item.ID)
	assert.Equal(t, eventbus.PriorityHigh, item.Priority)

	var sent map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(item.Body, &sent))
	for _, f := range []string{"actor", "recorded_at", "cursor"} {
		assert.NotContains(t, sent, f, "%s is the hub's, a producer never sends it", f)
	}
	assert.JSONEq(t, `"torque"`, string(sent["app"]))
	assert.NotNil(t, ev.Event.Actor, "the outbox event was not changed")
}

func TestSourceReadsInOrderPastACursorAndReportsTheHead(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	newTasks(t, store, "CW-HUB-2", "CW-HUB-3")
	src := hubrelay.Source{Store: store}
	all, err := src.After(t.Context(), 0, 100)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Less(t, all[0].Seq, all[1].Seq)
	rest, err := src.After(t.Context(), all[0].Seq, 100)
	require.NoError(t, err)
	require.Len(t, rest, 1)
	assert.Equal(t, all[1].Seq, rest[0].Seq)
	head, err := src.Head(t.Context())
	require.NoError(t, err)
	assert.Equal(t, all[1].Seq, head)
}

// A relay that was down past the retention resumes from the trim point and says
// what is gone, instead of failing for ever.
func TestSourceResumesFromTheTrimPointAndReportsTheGap(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	newTasks(t, store, "CW-HUB-4")
	time.Sleep(15 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(15 * time.Millisecond)
	require.NoError(t, store.TransitionTask("CW-HUB-4", "review"))
	n, err := store.OutboxTrimBefore(t.Context(), cutoff)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	var gotFrom, gotTo int64
	src := hubrelay.Source{Store: store, OnGap: func(from, to int64) { gotFrom, gotTo = from, to }}
	entries, err := src.After(t.Context(), 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.EqualValues(t, 1, gotFrom)
	assert.EqualValues(t, 1, gotTo)
	assert.Greater(t, entries[0].Seq, gotTo)
}

func TestCursorIsPerGenerationAndNeverMovesBack(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	c1 := hubrelay.Cursor{Store: store, Consumer: hubrelay.Consumer(1)}
	c2 := hubrelay.Cursor{Store: store, Consumer: hubrelay.Consumer(2)}
	got, err := c1.Load(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 0, got)
	require.NoError(t, c1.Save(t.Context(), 7))
	require.NoError(t, c1.Save(t.Context(), 3), "a late save is ignored, not an error")
	got, _ = c1.Load(t.Context())
	assert.EqualValues(t, 7, got)
	got, _ = c2.Load(t.Context())
	assert.EqualValues(t, 0, got, "another generation starts from its beginning")
}

// fakeHub stands in for the hub's ingest route and accepts everything. Nothing
// here touches a real hub.
type fakeHub struct {
	mu  sync.Mutex
	ids []string
}

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	if json.Unmarshal(raw, &req) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	type result struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Cursor string `json:"cursor"`
	}
	var results []result
	h.mu.Lock()
	for i, e := range req.Events {
		h.ids = append(h.ids, e.ID)
		results = append(results, result{ID: e.ID, Status: "accepted", Cursor: "c" + string(rune('a'+i))})
	}
	h.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "accepted": len(results), "duplicate": 0, "rejected": 0})
}

func (h *fakeHub) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.ids...)
}

// The whole path against a fake hub: events committed by real task changes are
// shipped once, the cursor survives a restart, and only what is new is sent.
func TestRelayShipsTheOutboxOnceAndResumesAfterARestart(t *testing.T) {
	hub := &fakeHub{}
	srv := httptest.NewServer(hub)
	defer srv.Close()
	store := sqlitetest.OpenStore(t)
	spool := t.TempDir()
	newTasks(t, store, "CW-HUB-5", "CW-HUB-6")
	meta, err := store.OutboxMeta(t.Context())
	require.NoError(t, err)

	run := func() (stop func(), r *relay.Relay) {
		client, err := eventbus.New(eventbus.Config{URL: srv.URL, SpoolDir: spool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		require.NoError(t, err)
		r, err = relay.New(relay.Config{Source: hubrelay.Source{Store: store},
			Cursor: hubrelay.Cursor{Store: store, Consumer: hubrelay.Consumer(meta.Generation)}, Deliverer: client, Poll: 5 * time.Millisecond})
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { defer close(done); _ = r.Run(ctx) }()
		return func() { cancel(); <-done; _ = client.Close(context.Background()) }, r
	}

	stop, r := run()
	require.Eventually(t, func() bool { return len(hub.seen()) == 2 && r.Stats().Cursor > 0 }, 10*time.Second, 5*time.Millisecond)
	stop()

	require.NoError(t, store.TransitionTask("CW-HUB-5", "review"))
	stop, r = run() // a restart: the cursor is in the database
	require.Eventually(t, func() bool { return len(hub.seen()) == 3 }, 10*time.Second, 5*time.Millisecond)
	head, err := store.OutboxHead(t.Context())
	require.NoError(t, err)
	require.Eventually(t, func() bool { return r.Stats().Cursor == head }, 10*time.Second, 5*time.Millisecond)
	stop()

	all, err := store.OutboxAfter(t.Context(), 0, 10)
	require.NoError(t, err)
	var want []string
	for _, e := range all {
		want = append(want, e.Event.ID)
	}
	assert.Equal(t, want, hub.seen(), "every event once, in order, none repeated after the restart")
}

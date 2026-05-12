package broker_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
	clockmsg "github.com/hollis-labs/clockwork-manifold/internal/messaging"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// memoryHub captures Broadcast calls so SSE coverage can assert which
// envelope.* events fired. Mirrors the SSEHub.Broadcast contract without
// dragging the http server into the test surface.
type memoryHub struct {
	mu     sync.Mutex
	events []hubEvent
}

type hubEvent struct {
	Type string
	Data map[string]interface{}
}

func (h *memoryHub) Broadcast(t string, d map[string]interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, hubEvent{Type: t, Data: d})
}

func (h *memoryHub) types() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.events))
	for i, e := range h.events {
		out[i] = e.Type
	}
	return out
}

func setupBroker(t *testing.T) (*broker.Broker, *memoryHub, gomsg.Store) {
	t.Helper()
	// File-backed SQLite with WAL — `:memory:` per-connection DBs lose
	// data when the pool opens a second connection mid-test (concurrent
	// fan-in tripped over this), mirroring messaging/sqlstore_test.
	dir := t.TempDir()
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(dir, "broker.db"), sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	t.Cleanup(func() { db.Close() })

	store := clockmsg.NewStore(db)
	hub := &memoryHub{}
	return broker.New(store, hub), hub, store
}

func addr(kind gomsg.AddressKind, id string) gomsg.Address {
	return gomsg.Address{Kind: kind, Authority: "test", ID: id}
}

// AC7: round-trip per envelope kind. Notice / Handoff / StatusUpdate go
// through the same Send pipeline; Escalation has its own helper.
// Request/Response are exercised by the dedicated tests below.
func TestBroker_TypedHelpersRoundTrip(t *testing.T) {
	b, hub, _ := setupBroker(t)
	ctx := context.Background()
	from := addr(gomsg.KindAgent, "alice")
	to := addr(gomsg.KindAgent, "bob")

	notice, err := b.Notice(ctx, from, to, json.RawMessage(`{"hello":"world"}`))
	require.NoError(t, err)
	assert.Equal(t, gomsg.MsgKindNotice, notice.Kind)
	assert.NotEmpty(t, notice.ID)

	handoff, err := b.Handoff(ctx, from, to, broker.HandoffPayload{
		TaskID: "CW-X", From: "alice", To: "bob", Note: "you take it",
	})
	require.NoError(t, err)
	assert.Equal(t, gomsg.MsgKindHandoff, handoff.Kind)

	status, err := b.StatusUpdate(ctx, from, to, broker.StatusPayload{
		State: "running", Progress: 42, Note: "midway",
	})
	require.NoError(t, err)
	assert.Equal(t, gomsg.MsgKindStatusUpdate, status.Kind)
	var decoded broker.StatusPayload
	require.NoError(t, json.Unmarshal(status.Payload, &decoded))
	assert.Equal(t, 42, decoded.Progress)

	esc, err := b.Escalation(ctx, from, to, broker.EscalationPayload{
		Severity: broker.SeverityWarn, Reason: "rate limited",
	})
	require.NoError(t, err)
	assert.Equal(t, gomsg.MsgKindEscalation, esc.Kind)

	// SSE stream order: created/created/created/(created+escalated).
	// Three plain helpers each emit envelope.created; the escalation
	// emits both envelope.created and envelope.escalated.
	got := hub.types()
	assert.Equal(t, []string{
		"envelope.created",
		"envelope.created",
		"envelope.created",
		"envelope.created",
		"envelope.escalated",
	}, got)
}

// AC3 + AC7: Request blocks until a correlated response arrives; the
// dispatcher matches by InReplyTo.
func TestBroker_RequestReplyRoundTrip(t *testing.T) {
	b, hub, _ := setupBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requester := addr(gomsg.KindAgent, "requester")
	responder := addr(gomsg.KindAgent, "responder")

	// Responder side: subscribe, wait for request, reply.
	sub, err := b.Subscribe(ctx, responder, gomsg.Filter{Kind: []gomsg.Kind{gomsg.MsgKindRequest}})
	require.NoError(t, err)

	replyDone := make(chan struct{})
	go func() {
		defer close(replyDone)
		req, ok := <-sub
		require.True(t, ok, "expected to receive a request")
		_, err := b.Reply(ctx, req, json.RawMessage(`{"result":"ok"}`))
		require.NoError(t, err)
	}()

	resp, err := b.Request(ctx, gomsg.Envelope{
		From:        requester,
		To:          responder,
		Payload:     json.RawMessage(`{"q":"ping"}`),
		ContentType: "application/json",
	})
	require.NoError(t, err)
	assert.Equal(t, gomsg.MsgKindResponse, resp.Kind)

	var body map[string]string
	require.NoError(t, json.Unmarshal(resp.Payload, &body))
	assert.Equal(t, "ok", body["result"])

	<-replyDone

	// SSE: at least envelope.responded twice (Reply and Request paths
	// both publish responded — Request publishes when the reply lands;
	// Reply publishes when it sends. Order isn't guaranteed across
	// goroutines, so we count rather than assert sequence).
	respondedCount := 0
	for _, ev := range hub.types() {
		if ev == "envelope.responded" {
			respondedCount++
		}
	}
	assert.GreaterOrEqual(t, respondedCount, 2)
}

// AC3 explicit: Request blocks until ctx-deadline elapses when no
// correlated response is sent. Returns gomsg.ErrRequestTimeout.
func TestBroker_RequestReplyTimeout(t *testing.T) {
	b, _, _ := setupBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	from := addr(gomsg.KindAgent, "asker")
	to := addr(gomsg.KindAgent, "silent")

	start := time.Now()
	_, err := b.Request(ctx, gomsg.Envelope{
		From:    from,
		To:      to,
		Payload: json.RawMessage(`{}`),
	})
	elapsed := time.Since(start)
	require.Error(t, err)
	assert.ErrorIs(t, err, gomsg.ErrRequestTimeout)
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond, "should respect ctx deadline")
}

// AC7: concurrent request fan-in — N concurrent Request calls, one
// responder serving each. Verifies the dispatcher's per-request
// correlation under -race.
func TestBroker_ConcurrentRequestFanIn(t *testing.T) {
	b, _, _ := setupBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const N = 8
	requester := addr(gomsg.KindAgent, "requester")
	responder := addr(gomsg.KindAgent, "responder")

	sub, err := b.Subscribe(ctx, responder, gomsg.Filter{Kind: []gomsg.Kind{gomsg.MsgKindRequest}})
	require.NoError(t, err)

	// Responder loop: replies as fast as it sees a request.
	go func() {
		for {
			select {
			case req, ok := <-sub:
				if !ok {
					return
				}
				// Echo: payload back as result.
				body, _ := json.Marshal(map[string]json.RawMessage{"echo": req.Payload})
				_, _ = b.Reply(ctx, req, body)
			case <-ctx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	var ok int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, _ := json.Marshal(map[string]int{"n": i})
			resp, err := b.Request(ctx, gomsg.Envelope{
				From:    requester,
				To:      responder,
				Payload: payload,
			})
			if err != nil {
				t.Errorf("request %d: %v", i, err)
				return
			}
			var body map[string]map[string]int
			if err := json.Unmarshal(resp.Payload, &body); err != nil {
				t.Errorf("decode %d: %v", i, err)
				return
			}
			if body["echo"]["n"] != i {
				t.Errorf("request %d: got n=%d", i, body["echo"]["n"])
				return
			}
			atomic.AddInt64(&ok, 1)
		}(i)
	}
	wg.Wait()
	assert.Equal(t, int64(N), atomic.LoadInt64(&ok))
}

// AC4: Inbox surfaces undelivered envelopes and publishes
// envelope.delivered for each. Subsequent Inbox calls don't re-deliver.
func TestBroker_InboxEmitsDeliveredEvents(t *testing.T) {
	b, hub, _ := setupBroker(t)
	ctx := context.Background()
	from := addr(gomsg.KindAgent, "alice")
	to := addr(gomsg.KindAgent, "bob")

	for i := 0; i < 3; i++ {
		_, err := b.Notice(ctx, from, to, json.RawMessage(`{}`))
		require.NoError(t, err)
	}

	got, err := b.Inbox(ctx, to, gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, got, 3)

	// Three created, three delivered.
	delivered := 0
	for _, ev := range hub.types() {
		if ev == "envelope.delivered" {
			delivered++
		}
	}
	assert.Equal(t, 3, delivered)

	// Drain — no more undelivered for `to`.
	again, err := b.Inbox(ctx, to, gomsg.Filter{})
	require.NoError(t, err)
	assert.Len(t, again, 0)
}

// AC6: validation rules — kind enum, from/to non-zero, payload size,
// escalation severity. Each rule is independently regression-guarded.
func TestBroker_Validation(t *testing.T) {
	b, _, _ := setupBroker(t)
	ctx := context.Background()
	from := addr(gomsg.KindAgent, "from")
	to := addr(gomsg.KindAgent, "to")

	// Unknown kind.
	_, err := b.Send(ctx, gomsg.Envelope{
		Kind: "bogus", From: from, To: to,
	})
	assert.ErrorIs(t, err, broker.ErrValidation)

	// From address zero.
	_, err = b.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice, To: to,
	})
	assert.ErrorIs(t, err, broker.ErrValidation)

	// To address zero.
	_, err = b.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice, From: from,
	})
	assert.ErrorIs(t, err, broker.ErrValidation)

	// Payload too large.
	big := make([]byte, broker.MaxPayloadBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	// JSON-marshal bumps length above the cap — wrap in a quoted string.
	jsonBig := []byte(`"` + strings.Repeat("a", broker.MaxPayloadBytes+1) + `"`)
	_, err = b.Send(ctx, gomsg.Envelope{
		Kind: gomsg.MsgKindNotice, From: from, To: to, Payload: jsonBig,
	})
	assert.ErrorIs(t, err, broker.ErrPayloadTooLarge)

	// Escalation: unknown severity.
	_, err = b.Escalation(ctx, from, to, broker.EscalationPayload{
		Severity: "super-critical", Reason: "x",
	})
	assert.ErrorIs(t, err, broker.ErrValidation)

	// Escalation: missing reason.
	_, err = b.Escalation(ctx, from, to, broker.EscalationPayload{
		Severity: broker.SeverityWarn,
	})
	assert.ErrorIs(t, err, broker.ErrValidation)
}

// Broker.publish is a no-op when sse is nil. Ensures construction with
// New(store, nil) doesn't crash on Send.
func TestBroker_NilHubIsSafe(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(dir, "broker.db"), sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	defer db.Close()

	b := broker.New(clockmsg.NewStore(db), nil)
	from := addr(gomsg.KindAgent, "a")
	to := addr(gomsg.KindAgent, "b")

	_, err = b.Notice(context.Background(), from, to, json.RawMessage(`{}`))
	require.NoError(t, err)
}

// Cancel + Get pass-through. Enough coverage to lock in the contract;
// the underlying store_test owns the deeper semantics.
func TestBroker_CancelAndGetPassthrough(t *testing.T) {
	b, _, _ := setupBroker(t)
	ctx := context.Background()
	from := addr(gomsg.KindAgent, "a")
	to := addr(gomsg.KindAgent, "b")

	sent, err := b.Notice(ctx, from, to, json.RawMessage(`{}`))
	require.NoError(t, err)

	got, err := b.Get(ctx, sent.ID)
	require.NoError(t, err)
	assert.Equal(t, sent.ID, got.ID)

	require.NoError(t, b.Cancel(ctx, sent.ID))

	got, err = b.Get(ctx, sent.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.ID, "Get still returns the canceled envelope")
}

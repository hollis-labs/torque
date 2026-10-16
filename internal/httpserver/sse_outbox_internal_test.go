package httpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/util/otel/propagation"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingSource is an outbox whose first read fails.
type failingSource struct{}

func (failingSource) OutboxMeta(context.Context) (sqlstore.OutboxMeta, error) {
	return sqlstore.OutboxMeta{}, errors.New("db gone")
}
func (failingSource) OutboxHead(context.Context) (int64, error) { return 0, errors.New("db gone") }
func (failingSource) OutboxAfter(context.Context, int64, int) ([]sqlstore.OutboxEvent, error) {
	return nil, errors.New("db gone")
}

// If the outbox cannot be read when a client connects, the response ends so
// EventSource reconnects. A stream that stays open with heartbeats but never
// carries a state change looks exactly like a quiet system.
func TestSSE_OutboxStartFailureEndsTheResponse(t *testing.T) {
	hub := NewSSEHub()
	hub.EnableOutbox(failingSource{})
	ts := httptest.NewServer(hub)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream stayed open after the outbox failed to start")
	}
}

// plainWriter hides the connection the way propagation.HTTPMiddleware's
// statusWriter does: it forwards Write/Flush/WriteHeader but has no Unwrap.
type plainWriter struct{ http.ResponseWriter }

func (p plainWriter) Flush() { p.ResponseWriter.(http.Flusher).Flush() }

func wrapHidingConn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(plainWriter{w}, r)
	})
}

// The write deadline must work through the middleware Torque really serves
// behind: propagation.HTTPMiddleware wraps the writer, and since libs core/util
// v0.5.0 that wrapper has Unwrap, so http.ResponseController reaches the
// connection. (With v0.4.0 it did not, and the deadline was a silent no-op.)
func TestSSE_WriteDeadlineWorksThroughThePropagationMiddleware(t *testing.T) {
	var deadlineErr, flushErr error
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		deadlineErr = rc.SetWriteDeadline(time.Now().Add(time.Minute))
		flushErr = rc.Flush()
	})
	ts := httptest.NewServer(propagation.HTTPMiddleware(probe))
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	require.NoError(t, err)
	resp.Body.Close()
	assert.NoError(t, deadlineErr)
	assert.NoError(t, flushErr)
}

// The durable stream, served through the real middleware, comes up (it is not
// refused for lack of a write deadline) and sends its connected frame.
func TestSSE_DurableStreamStartsBehindThePropagationMiddleware(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	hub := NewSSEHub()
	hub.EnableOutbox(store)
	ts := httptest.NewServer(propagation.HTTPMiddleware(hub))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	buf := make([]byte, 256)
	n, err := resp.Body.Read(buf)
	require.NoError(t, err)
	assert.Contains(t, string(buf[:n]), `"type":"connected"`)
}

// A durable stream refuses to run on a connection that cannot take a write
// deadline, instead of serving one that can pin a goroutine indefinitely.
func TestSSE_OutboxStreamRefusedWithoutAWriteDeadline(t *testing.T) {
	hub := NewSSEHub()
	hub.EnableOutbox(failingSource{})
	ts := httptest.NewServer(wrapHidingConn(hub))
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// If the log is trimmed past a connected client's position, drain tells the
// client (a gap whose id is the trim point) instead of silently skipping rows,
// then carries on with what remains.
func TestSSE_DrainSendsAGapWhenTheLogWasTrimmedPastTheCursor(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	ctx := context.Background()
	for _, id := range []string{"CW-DR-1", "CW-DR-2", "CW-DR-3"} {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: id, Title: id, Priority: 2}))
	}
	require.NoError(t, store.TransitionTask("CW-DR-1", "doing"))
	require.NoError(t, store.TransitionTask("CW-DR-2", "doing"))
	time.Sleep(15 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(15 * time.Millisecond)
	require.NoError(t, store.TransitionTask("CW-DR-3", "doing"))
	n, err := store.OutboxTrimBefore(ctx, cutoff)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)

	rec := httptest.NewRecorder()
	feed := newOutboxFeed(store)
	cur := int64(0) // the client last saw nothing: its cursor is behind the trim point
	require.NoError(t, feed.drain(ctx, rec, http.NewResponseController(rec), rec, &cur))

	body := rec.Body.String()
	assert.Contains(t, body, `"type":"gap"`)
	assert.Contains(t, body, `"reason":"pruned"`)
	assert.Contains(t, body, "id: 1:2\n", "the gap carries the trim point as its id, so a reconnect does not repeat it")
	assert.Contains(t, body, `"task_id":"CW-DR-3"`, "what remains is delivered after the gap")
	assert.NotContains(t, body, "CW-DR-1")
	assert.EqualValues(t, 3, cur)
}

// A gap frame's id moves the client's Last-Event-ID past the position the log
// could not honour: reconnecting with it does not produce the same gap again.
func TestSSE_GapFrameIDStopsRepeatedGaps(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	feed := newOutboxFeed(store)
	cur, gap, err := feed.start(context.Background(), "99:5")
	require.NoError(t, err)
	require.NotNil(t, gap)
	require.NotEmpty(t, gap.id)
	_ = cur

	_, gap2, err := feed.start(context.Background(), gap.id)
	require.NoError(t, err)
	assert.Nil(t, gap2, "resuming from the gap's id is a normal resume")
}

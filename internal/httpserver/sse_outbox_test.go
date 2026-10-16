package httpserver_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// frame is one SSE message as the GUI sees it, plus the SSE id line.
type frame struct {
	ID       string
	Type     string                 `json:"type"`
	Data     map[string]interface{} `json:"data"`
	Envelope map[string]interface{} `json:"envelope"`
}

type outboxEnv struct {
	ts    *httptest.Server
	store *sqlstore.Store
	svc   *service.Service
}

func setupOutboxServer(t *testing.T) *outboxEnv {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	handler := httpserver.New(svc, nil)
	handler.SSEHub().EnableOutbox(store)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); handler.SSEHub().RunOutbox(ctx, store.OutboxWake()) }()
	ts := httptest.NewServer(handler)
	t.Cleanup(func() { cancel(); ts.Close(); wg.Wait() })
	return &outboxEnv{ts: ts, store: store, svc: svc}
}

// stream opens /events and sends parsed frames until the test ends.
func (e *outboxEnv) stream(t *testing.T, lastID string) <-chan frame {
	t.Helper()
	ch, _ := e.streamCancel(t, lastID)
	return ch
}

func (e *outboxEnv) streamCancel(t *testing.T, lastID string) (<-chan frame, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ts.URL+"/api/v1/events", nil)
	require.NoError(t, err)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	out := make(chan frame, 256)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var id string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				var f frame
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f) == nil {
					f.ID = id
					out <- f
				}
				id = ""
			}
		}
	}()
	return out, cancel
}

// next returns the next frame of type typ, skipping others (connected, pings).
func next(t *testing.T, ch <-chan frame, typ string) frame {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-ch:
			require.True(t, ok, "stream closed while waiting for %s", typ)
			if f.Type == typ {
				return f
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s frame", typ)
		}
	}
}

func noFrame(t *testing.T, ch <-chan frame, typ string, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case f := <-ch:
			if f.Type == typ {
				t.Fatalf("unexpected %s frame %+v", typ, f)
			}
		case <-deadline:
			return
		}
	}
}

func seedTask(t *testing.T, e *outboxEnv, id string) {
	t.Helper()
	require.NoError(t, e.store.CreateTask(&sqlstore.TaskRecord{ID: id, Title: id, Priority: 2}))
}

func TestEventsOutbox_OneFrameWithIDAndBothShapes(t *testing.T) {
	e := setupOutboxServer(t)
	seedTask(t, e, "CW-SSE-0001")
	ch := e.stream(t, "")
	next(t, ch, "connected")

	require.NoError(t, e.svc.Task.Transition(context.Background(), "CW-SSE-0001", "doing"))

	f := next(t, ch, "task.transitioned")
	assert.Regexp(t, `^\d+:\d+$`, f.ID, "outbox frames carry an SSE id")
	// legacy shape at the top level: what the GUI reads
	assert.Equal(t, "CW-SSE-0001", f.Data["task_id"])
	assert.Equal(t, "doing", f.Data["status"])
	assert.Equal(t, "todo", f.Data["from"])
	assert.Equal(t, "doing", f.Data["payload"].(map[string]interface{})["to"])
	// the v1.1 event rides in the same frame
	assert.Equal(t, "task.state_changed", f.Envelope["kind"])
	noFrame(t, ch, "task.transitioned", 300*time.Millisecond) // exactly one frame, not two
}

func TestEventsOutbox_ResumeFromLastEventID(t *testing.T) {
	e := setupOutboxServer(t)
	for _, id := range []string{"CW-SSE-0010", "CW-SSE-0011", "CW-SSE-0012", "CW-SSE-0013"} {
		seedTask(t, e, id)
	}
	ctx := context.Background()
	ch, cancel := e.streamCancel(t, "")
	next(t, ch, "connected")
	require.NoError(t, e.svc.Task.Transition(ctx, "CW-SSE-0010", "doing"))
	first := next(t, ch, "task.transitioned")
	require.Equal(t, "CW-SSE-0010", first.Data["task_id"])
	cancel() // the client drops

	// Three transitions happen while it is away.
	for _, id := range []string{"CW-SSE-0011", "CW-SSE-0012", "CW-SSE-0013"} {
		require.NoError(t, e.svc.Task.Transition(ctx, id, "doing"))
	}

	ch2 := e.stream(t, first.ID)
	var got []string
	for range 3 {
		got = append(got, next(t, ch2, "task.transitioned").Data["task_id"].(string))
	}
	assert.Equal(t, []string{"CW-SSE-0011", "CW-SSE-0012", "CW-SSE-0013"}, got, "exactly what was missed, in order, nothing earlier")
	noFrame(t, ch2, "task.transitioned", 300*time.Millisecond)
}

func TestEventsOutbox_NoIDMeansLiveOnly(t *testing.T) {
	e := setupOutboxServer(t)
	seedTask(t, e, "CW-SSE-0020")
	seedTask(t, e, "CW-SSE-0021")
	require.NoError(t, e.svc.Task.Transition(context.Background(), "CW-SSE-0020", "doing"))

	ch := e.stream(t, "")
	next(t, ch, "connected")
	noFrame(t, ch, "task.transitioned", 300*time.Millisecond) // history is not replayed without an id
	require.NoError(t, e.svc.Task.Transition(context.Background(), "CW-SSE-0021", "doing"))
	assert.Equal(t, "CW-SSE-0021", next(t, ch, "task.transitioned").Data["task_id"])
}

func TestEventsOutbox_OrderedUnderConcurrency(t *testing.T) {
	e := setupOutboxServer(t)
	const n = 60
	for i := range n {
		seedTask(t, e, fmt.Sprintf("CW-SSE-1%03d", i))
	}
	ch := e.stream(t, "")
	next(t, ch, "connected")

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, e.svc.Task.Transition(context.Background(), fmt.Sprintf("CW-SSE-1%03d", i), "doing"))
		}()
	}
	wg.Wait()

	seen := map[string]bool{}
	var last int64
	for range n {
		f := next(t, ch, "task.transitioned")
		var gen, seq int64
		_, err := fmt.Sscanf(f.ID, "%d:%d", &gen, &seq)
		require.NoError(t, err)
		assert.Greater(t, seq, last, "ids strictly increase")
		last = seq
		id := f.Data["task_id"].(string)
		assert.False(t, seen[id], "%s delivered twice", id)
		seen[id] = true
	}
	assert.Len(t, seen, n)
}

func TestEventsOutbox_GapFrames(t *testing.T) {
	e := setupOutboxServer(t)
	seedTask(t, e, "CW-SSE-0030")
	require.NoError(t, e.svc.Task.Transition(context.Background(), "CW-SSE-0030", "doing"))

	cases := map[string]string{
		"bad cursor":       "not-a-cursor",
		"other generation": "99:1",
		"cursor ahead":     "1:99999",
	}
	reasons := map[string]string{
		"bad cursor": "bad_cursor", "other generation": "generation_changed", "cursor ahead": "cursor_ahead",
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			ch := e.stream(t, id)
			g := next(t, ch, "gap")
			assert.Equal(t, reasons[name], g.Data["reason"])
			assert.NotEmpty(t, g.Data["earliest_available"])
			assert.NotEmpty(t, g.Data["high_water"])
		})
	}
}

func TestEventsOutbox_PrunedCursorGetsGapThenWhatRemains(t *testing.T) {
	e := setupOutboxServer(t)
	for _, id := range []string{"CW-SSE-0040", "CW-SSE-0041"} {
		seedTask(t, e, id)
	}
	ctx := context.Background()
	require.NoError(t, e.svc.Task.Transition(ctx, "CW-SSE-0040", "doing"))
	gen := strings.Split(e.lastID(t), ":")[0]
	time.Sleep(20 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, e.svc.Task.Transition(ctx, "CW-SSE-0041", "doing"))
	n, err := e.store.OutboxTrimBefore(ctx, cutoff)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	// A client whose cursor predates the trim point is told, then gets the rest.
	ch := e.stream(t, gen+":0")
	g := next(t, ch, "gap")
	assert.Equal(t, "pruned", g.Data["reason"])
	assert.Equal(t, "CW-SSE-0041", next(t, ch, "task.transitioned").Data["task_id"])
}

func (e *outboxEnv) lastID(t *testing.T) string {
	t.Helper()
	meta, err := e.store.OutboxMeta(context.Background())
	require.NoError(t, err)
	head, err := e.store.OutboxHead(context.Background())
	require.NoError(t, err)
	return fmt.Sprintf("%d:%d", meta.Generation, head)
}

func TestEventsOutbox_HTTPTransitionEmitsOnceNoDirectBroadcast(t *testing.T) {
	e := setupOutboxServer(t)
	seedTask(t, e, "CW-SSE-0050")
	ch := e.stream(t, "")
	next(t, ch, "connected")

	resp, err := http.Post(e.ts.URL+"/api/v1/tasks/CW-SSE-0050/transition", "application/json",
		strings.NewReader(`{"status":"doing"}`))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	f := next(t, ch, "task.transitioned")
	env, _ := f.Envelope["payload"].(map[string]interface{})
	trig, _ := env["trigger"].(map[string]interface{})
	assert.Equal(t, "human", trig["kind"], "the HTTP handler stamps the trigger")
	noFrame(t, ch, "task.transitioned", 400*time.Millisecond) // no second, un-id'd copy
}

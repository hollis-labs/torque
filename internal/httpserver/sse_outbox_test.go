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
	seedTask(t, e, "CW-SSE-0051")
	ch := e.stream(t, "")
	next(t, ch, "connected")

	do := func(id string, gui bool) {
		req, err := http.NewRequest(http.MethodPost, e.ts.URL+"/api/v1/tasks/"+id+"/transition", strings.NewReader(`{"status":"doing"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if gui {
			req.Header.Set("X-Client", "browser")
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	triggerKind := func(f frame) string {
		env, _ := f.Envelope["payload"].(map[string]interface{})
		trig, _ := env["trigger"].(map[string]interface{})
		kind, _ := trig["kind"].(string)
		return kind
	}

	do("CW-SSE-0050", true)
	f := next(t, ch, "task.transitioned")
	assert.Equal(t, "human", triggerKind(f), "a request the GUI marked is human")
	noFrame(t, ch, "task.transitioned", 400*time.Millisecond) // no second, un-id'd copy

	do("CW-SSE-0051", false)
	f = next(t, ch, "task.transitioned")
	assert.Equal(t, "agent", triggerKind(f), "an unmarked API call is an agent call")
}

func TestEventsOutbox_CheckpointFramesKeepTheNamesTheBannerListensFor(t *testing.T) {
	e := setupOutboxServer(t)
	require.NoError(t, e.store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-SSE-0060", Title: "t", Priority: 2, Kind: "decision", Status: "doing",
		CheckpointMode: "blocking", OnCheckpointResponse: "resume", Manual: true,
		OnDone: "review", OnFail: "retry", OnReview: "pause", OnDoneMerge: "none", SourceType: "user", Trust: "normal",
	}))
	ch := e.stream(t, "")
	next(t, ch, "connected")

	resp, err := http.Post(e.ts.URL+"/api/v1/checkpoints", "application/json",
		strings.NewReader(`{"task_id":"CW-SSE-0060","type":"approval","payload_json":"{}"}`))
	require.NoError(t, err)
	var cp struct {
		CorrelationID string `json:"correlation_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&cp))
	resp.Body.Close()
	require.NotEmpty(t, cp.CorrelationID)

	emitted := next(t, ch, "checkpoint.emitted")
	assert.Equal(t, cp.CorrelationID, emitted.Data["correlation_id"])
	assert.Equal(t, "CW-SSE-0060", emitted.Data["task_id"])
	assert.Regexp(t, `^\d+:\d+$`, emitted.ID)
	assert.Equal(t, "checkpoint.state_changed", emitted.Envelope["kind"])
	parked := next(t, ch, "task.transitioned") // the park, from the same emit
	assert.Equal(t, "review", parked.Data["status"])
	noFrame(t, ch, "checkpoint.emitted", 300*time.Millisecond) // once, not twice

	resp, err = http.Post(e.ts.URL+"/api/v1/checkpoints/"+cp.CorrelationID+"/respond", "application/json",
		strings.NewReader(`{"response_json":"{\"answer\":\"yes\"}","responder_source_type":"user"}`))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	responded := next(t, ch, "checkpoint.responded")
	assert.Equal(t, cp.CorrelationID, responded.Data["correlation_id"])
	noFrame(t, ch, "checkpoint.responded", 300*time.Millisecond)
}

func TestEventsOutbox_RunFramesUseTheEventKind(t *testing.T) {
	e := setupOutboxServer(t)
	seedTask(t, e, "CW-SSE-0070")
	ch := e.stream(t, "")
	next(t, ch, "connected")
	_, err := e.store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-SSE-0070", Executor: "cli"})
	require.NoError(t, err)
	f := next(t, ch, "run.state_changed")
	assert.Equal(t, "running", f.Data["to"])
	assert.Equal(t, "run.state_changed", f.Envelope["kind"])
}

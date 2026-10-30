package tetherexec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/tetherclient"
)

// fakeTether is a minimal Tether: sessions keyed by idempotency key, a scripted
// launch refusal count, scripted events, and a recorded stop.
type fakeTether struct {
	mu          sync.Mutex
	sessions    map[string]*fakeSession // by id
	byKey       map[string]string       // idempotency key -> id
	creates     []tetherclient.CreateRequest
	refuseLaunc int    // remaining launch refusals
	refuseCode  string // error code for refusals
	launches    int
	stops       int
	exitOnStop  bool // stop moves the session to killed
}

type fakeSession struct {
	id     string
	state  string
	exit   *int
	events []tetherclient.Event
}

func newFake() *fakeTether {
	return &fakeTether{sessions: map[string]*fakeSession{}, byKey: map[string]string{}, refuseCode: "environment_cap_reached", exitOnStop: true}
}

func (f *fakeTether) emit(id, kind string, payload map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	b, _ := json.Marshal(payload)
	s.events = append(s.events, tetherclient.Event{Seq: int64(len(s.events) + 1), Kind: kind, SessionID: id, PayloadJSON: string(b)})
}

func (f *fakeTether) setState(id, state string, exit int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[id].state = state
	f.sessions[id].exit = &exit
}

func (f *fakeTether) only() *fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		return s
	}
	return nil
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func (f *fakeTether) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/sessions":
		var req tetherclient.CreateRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Launch == "nope" {
			writeErr(w, 404, "not_found", "unknown launch")
			return
		}
		if id, ok := f.byKey[req.IdempotencyKey]; ok && req.IdempotencyKey != "" {
			prev := f.creates[0]
			if prev.PromptAppend != req.PromptAppend {
				writeErr(w, 409, "idempotency_conflict", "key reused")
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "replayed": true})
			return
		}
		f.creates = append(f.creates, req)
		id := fmt.Sprintf("sess-%d", len(f.sessions)+1)
		f.sessions[id] = &fakeSession{id: id, state: "created"}
		if req.IdempotencyKey != "" {
			f.byKey[req.IdempotencyKey] = id
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "replayed": false})
	case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "launch":
		s := f.sessions[parts[1]]
		if f.refuseLaunc > 0 {
			f.refuseLaunc--
			writeErr(w, 409, f.refuseCode, "at capacity")
			return
		}
		f.launches++
		s.state = "running"
		_ = json.NewEncoder(w).Encode(map[string]any{"id": s.id})
	case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "stop":
		f.stops++
		if f.exitOnStop {
			z := -1
			f.sessions[parts[1]].state = "killed"
			f.sessions[parts[1]].exit = &z
		}
		w.WriteHeader(204)
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "sessions":
		s := f.sessions[parts[1]]
		_ = json.NewEncoder(w).Encode(map[string]any{"id": s.id, "launch_id": "L", "state": s.state, "exit_code": s.exit, "workstream_id": "ws-9"})
	case r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "events":
		s := f.sessions[parts[1]]
		evs := make([]tetherclient.Event, 0, len(s.events))
		for i := len(s.events) - 1; i >= 0; i-- { // newest first, like Tether
			evs = append(evs, s.events[i])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"events": evs})
	case r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "wait":
		_ = json.NewEncoder(w).Encode(map[string]any{"exit_code": 0, "output_kind": "final", "output_text": "from wait"})
	default:
		writeErr(w, 404, "not_found", r.URL.Path)
	}
}

type recBinder struct {
	mu    sync.Mutex
	calls []string
}

func (b *recBinder) BindRun(_ context.Context, runID int64, sid, launch, ws string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, fmt.Sprintf("%d|%s|%s|%s", runID, sid, launch, ws))
	return nil
}

func setup(t *testing.T, cfg Config) (*Executor, *fakeTether, *recBinder) {
	t.Helper()
	f := newFake()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg.PollInterval = 10 * time.Millisecond
	cfg.DeferInterval = 10 * time.Millisecond
	b := &recBinder{}
	return New(tetherclient.New(srv.URL, ""), b, cfg), f, b
}

func job() *executor.ExecutionJob {
	return &executor.ExecutionJob{TaskID: "CW-1", TaskTitle: "Do it", Description: "details", RunID: 7,
		Metadata: map[string]any{"tether_launch": "general-worker"}}
}

func finalEvent(f *fakeTether, id string) {
	f.emit(id, "session.turn_output", map[string]any{"turn_id": "t1", "kind": "final", "text": "all done"})
}

// waitSession waits for the fake to have a session.
func waitSession(t *testing.T, f *fakeTether) *fakeSession {
	t.Helper()
	var s *fakeSession
	require.Eventually(t, func() bool { s = f.only(); return s != nil }, 2*time.Second, 5*time.Millisecond)
	return s
}

func TestSuccess_FinalTurnMarksDone_BindsRun_StopsIdleSession(t *testing.T) {
	ex, f, b := setup(t, Config{})
	done := make(chan *executor.ExecutionResult, 1)
	go func() {
		r, err := ex.Run(context.Background(), job(), nil)
		require.NoError(t, err)
		done <- r
	}()
	s := waitSession(t, f)
	finalEvent(f, s.id)
	r := <-done
	require.Equal(t, "done", r.Status)
	require.Contains(t, r.Reason, "all done")
	require.NotNil(t, r.TaskComment)
	require.Equal(t, 1, f.stops, "no reaper configured: Torque stops the idle session")
	require.Equal(t, "torque/CW-1/run-7", f.creates[0].IdempotencyKey)
	require.Equal(t, "general-worker", f.creates[0].Launch)
	require.Contains(t, f.creates[0].PromptAppend, "CW-1")
	require.Empty(t, f.creates[0].Override)
	// run bound at create (no workstream yet), then workstream filled from the session.
	require.Contains(t, b.calls, "7|sess-1|general-worker|")
	require.Contains(t, b.calls, "7|sess-1|L|ws-9")
}

func TestReaperEndsSession_NoExplicitStop(t *testing.T) {
	ex, f, _ := setup(t, Config{StopAfterFinalTurn: "30s", StopGrace: time.Minute})
	done := make(chan *executor.ExecutionResult, 1)
	go func() {
		r, _ := ex.Run(context.Background(), job(), nil)
		done <- r
	}()
	s := waitSession(t, f)
	require.Eventually(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return s.state == "running" }, time.Second, 5*time.Millisecond)
	finalEvent(f, s.id)
	time.Sleep(50 * time.Millisecond)
	f.setState(s.id, "killed", 0) // the reaper stops it
	r := <-done
	require.Equal(t, "done", r.Status)
	require.Equal(t, 0, f.stops)
	require.JSONEq(t, `{"lifecycle":{"stop_after_final_turn":"30s"}}`, f.creates[0].Override)
}

func TestReaperMissing_TorqueStopsAfterGrace(t *testing.T) {
	ex, f, _ := setup(t, Config{StopAfterFinalTurn: "20ms", StopGrace: 30 * time.Millisecond})
	done := make(chan *executor.ExecutionResult, 1)
	go func() {
		r, _ := ex.Run(context.Background(), job(), nil)
		done <- r
	}()
	s := waitSession(t, f)
	finalEvent(f, s.id)
	r := <-done
	require.Equal(t, "done", r.Status)
	require.Equal(t, 1, f.stops)
}

func TestFailure_ExitBeforeFinal(t *testing.T) {
	ex, f, _ := setup(t, Config{})
	done := make(chan *executor.ExecutionResult, 1)
	go func() {
		r, _ := ex.Run(context.Background(), job(), nil)
		done <- r
	}()
	s := waitSession(t, f)
	f.setState(s.id, "failed", 3)
	r := <-done
	require.Equal(t, "failed", r.Status)
	require.Contains(t, r.Reason, "exit 3")
	require.Equal(t, 3, *r.ExitCode)
	require.Equal(t, 0, f.stops, "session already over")
}

func TestFailure_FailureTurnStopsSession(t *testing.T) {
	ex, f, _ := setup(t, Config{})
	done := make(chan *executor.ExecutionResult, 1)
	go func() {
		r, _ := ex.Run(context.Background(), job(), nil)
		done <- r
	}()
	s := waitSession(t, f)
	f.emit(s.id, "session.turn_output", map[string]any{"kind": "failure", "text": "boom"})
	r := <-done
	require.Equal(t, "failed", r.Status)
	require.Contains(t, r.Reason, "boom")
	require.Equal(t, 1, f.stops)
}

func TestExitZeroWithoutFinalIsDone_KilledWithoutFinalIsFailed(t *testing.T) {
	for state, want := range map[string]string{"completed": "done", "killed": "failed"} {
		ex, f, _ := setup(t, Config{})
		done := make(chan *executor.ExecutionResult, 1)
		go func() {
			r, _ := ex.Run(context.Background(), job(), nil)
			done <- r
		}()
		s := waitSession(t, f)
		f.setState(s.id, state, 0)
		require.Equal(t, want, (<-done).Status, state)
	}
}

func TestCapDeferral_RetriesLaunchAndSucceeds(t *testing.T) {
	ex, f, _ := setup(t, Config{})
	f.refuseLaunc = 3
	done := make(chan *executor.ExecutionResult, 1)
	var events []string
	var mu sync.Mutex
	go func() {
		r, err := ex.Run(context.Background(), job(), func(e executor.ExecutionEvent) {
			mu.Lock()
			events = append(events, e.Content)
			mu.Unlock()
		})
		require.NoError(t, err)
		done <- r
	}()
	s := waitSession(t, f)
	require.Eventually(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.launches == 1 }, 2*time.Second, 5*time.Millisecond)
	finalEvent(f, s.id)
	require.Equal(t, "done", (<-done).Status)
	require.Len(t, f.creates, 1, "deferral relaunches the same session, no new create")
	mu.Lock()
	defer mu.Unlock()
	deferred := 0
	for _, e := range events {
		if strings.Contains(e, "launch deferred") {
			deferred++
		}
	}
	require.Equal(t, 3, deferred)
}

func TestCapDeferral_AllCodesDeferAndGiveUpBlocksNotFails(t *testing.T) {
	for _, code := range []string{"execution_paused", "environment_cap_reached", "project_cap_reached"} {
		ex, f, _ := setup(t, Config{DeferMax: 60 * time.Millisecond})
		f.refuseLaunc, f.refuseCode = 1000, code
		r, err := ex.Run(context.Background(), job(), nil)
		require.NoError(t, err, code)
		require.Equal(t, "blocked", r.Status, code)
		require.Contains(t, r.Reason, code)
		require.Equal(t, 1, f.stops, "the never-launched session is stopped")
	}
}

func TestIdempotentReplay_SameRunSameSession(t *testing.T) {
	ex, f, _ := setup(t, Config{})
	c := ex.sessions.(*tetherclient.Client)
	req := tetherclient.CreateRequest{Launch: "L", IdempotencyKey: IdempotencyKey("CW-1", 7), PromptAppend: "p"}
	a, err := c.CreateSession(context.Background(), req)
	require.NoError(t, err)
	require.False(t, a.Replayed)
	b, err := c.CreateSession(context.Background(), req)
	require.NoError(t, err)
	require.True(t, b.Replayed)
	require.Equal(t, a.ID, b.ID)
	require.Len(t, f.sessions, 1)

	req.PromptAppend = "different"
	_, err = c.CreateSession(context.Background(), req)
	var ae *tetherclient.APIError
	require.ErrorAs(t, err, &ae)
	require.Equal(t, tetherclient.CodeIdempotencyConflict, ae.Code)
	require.NotEqual(t, IdempotencyKey("CW-1", 7), IdempotencyKey("CW-1", 8), "a new attempt gets a new key")
}

func TestCancel_StopsTetherSession(t *testing.T) {
	ex, f, _ := setup(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	type out struct {
		r   *executor.ExecutionResult
		err error
	}
	done := make(chan out, 1)
	go func() {
		r, err := ex.Run(ctx, job(), nil)
		done <- out{r, err}
	}()
	s := waitSession(t, f)
	require.Eventually(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return s.state == "running" }, time.Second, 5*time.Millisecond)
	cancel()
	o := <-done
	require.ErrorIs(t, o.err, context.Canceled)
	require.Equal(t, 1, f.stops)
	require.Equal(t, "killed", s.state)
}

func TestCancelDuringDeferral_StopsCreatedSession(t *testing.T) {
	ex, f, _ := setup(t, Config{})
	f.refuseLaunc = 1000
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := ex.Run(ctx, job(), nil); done <- err }()
	waitSession(t, f)
	time.Sleep(40 * time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Equal(t, 1, f.stops)
}

func TestTimeout_StopsSessionAndFails(t *testing.T) {
	ex, f, _ := setup(t, Config{DefaultTimeout: 80 * time.Millisecond})
	r, err := ex.Run(context.Background(), job(), nil)
	require.NoError(t, err)
	require.Equal(t, "failed", r.Status)
	require.Contains(t, r.Reason, "timed out")
	require.Equal(t, 1, f.stops)
}

func TestTimeoutAfterFinal_IsDone(t *testing.T) {
	ex, f, _ := setup(t, Config{DefaultTimeout: 150 * time.Millisecond, StopAfterFinalTurn: "10m", StopGrace: time.Hour})
	done := make(chan *executor.ExecutionResult, 1)
	go func() { r, _ := ex.Run(context.Background(), job(), nil); done <- r }()
	s := waitSession(t, f)
	finalEvent(f, s.id)
	r := <-done
	require.Equal(t, "done", r.Status)
	require.Equal(t, 1, f.stops)
}

func TestRefusedCreateIsPermanentBlock(t *testing.T) {
	ex, _, _ := setup(t, Config{})
	j := job()
	j.Metadata["tether_launch"] = "nope"
	r, err := ex.Run(context.Background(), j, nil)
	require.Error(t, err)
	require.True(t, executor.IsPermanent(err))
	require.Equal(t, "blocked", r.Status)
}

func TestValidate(t *testing.T) {
	ex, _, _ := setup(t, Config{})
	require.NoError(t, ex.Validate(job()))
	j := job()
	j.Metadata = nil
	require.True(t, executor.IsPermanent(ex.Validate(j)), "no launch")
	j.LaunchProfile = "from-profile"
	require.NoError(t, ex.Validate(j))
	require.True(t, executor.IsPermanent(New(nil, nil, Config{DefaultLaunch: "x"}).Validate(job())), "no tether url")
}

func TestBadReaperDurationIsPermanent(t *testing.T) {
	ex, _, _ := setup(t, Config{})
	j := job()
	j.Metadata["tether_stop_after_final_turn"] = "soon"
	_, err := ex.Run(context.Background(), j, nil)
	require.True(t, executor.IsPermanent(err))
}

func TestRoutedFinal_TextRecoveredFromWaitOnceSessionEnds(t *testing.T) {
	ex, f, _ := setup(t, Config{StopAfterFinalTurn: "30s", StopGrace: time.Minute})
	done := make(chan *executor.ExecutionResult, 1)
	go func() { r, _ := ex.Run(context.Background(), job(), nil); done <- r }()
	s := waitSession(t, f)
	// A routed final carries a message_id and no text.
	f.emit(s.id, "session.turn_output", map[string]any{"turn_id": "t1", "kind": "final", "message_id": "m1"})
	time.Sleep(40 * time.Millisecond)
	f.setState(s.id, "killed", 0)
	r := <-done
	require.Equal(t, "done", r.Status)
	require.Contains(t, r.Reason, "from wait")
}

func TestQuestionTurnIsNotCompletion(t *testing.T) {
	ex, f, _ := setup(t, Config{DefaultTimeout: 150 * time.Millisecond})
	done := make(chan *executor.ExecutionResult, 1)
	go func() { r, _ := ex.Run(context.Background(), job(), nil); done <- r }()
	s := waitSession(t, f)
	f.emit(s.id, "session.turn_output", map[string]any{"turn_id": "t1", "kind": "question", "text": "which db?"})
	r := <-done
	require.Equal(t, "failed", r.Status, "an unanswered question runs to the timeout, it is not success")
	require.Contains(t, r.Reason, "timed out")
}

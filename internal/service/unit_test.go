package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
)

// fakeTeamsAPI is a Tether team API with real idempotency semantics: a form
// with a key it has seen returns the same run; delegate and dissolve are keyed
// too. Unknown runs 404. A roster lists the owner (coordinator) seat, which
// stays active after the team stops. Faults are injected per test.
type fakeTeamsAPI struct {
	mu           sync.Mutex
	runsByKey    map[string]string
	formRequests []tetherclient.TeamForm
	formCalls    int
	delegations  []map[string]string
	dissolved    map[string]bool
	ended        bool

	refuseForm   int           // 4xx on form (nothing formed)
	replayStatus int           // status answered for a key that was already formed (e.g. 409)
	hideRoster   bool          // roster listing does not show formed teams
	loseResponse bool          // form forms the team, then drops the connection
	serverError  bool          // 500 before forming anything
	failDeleg    bool          // delegate answers 502
	failDissolve bool          // dissolve answers 500
	formDelay    time.Duration // slow formation
	onDissolve   func()
	onForm       func() // runs while a form is being handled
	forms        []tetherclient.TeamForm
	teamIDs      []string
	teamRun      []string
}

func newFake() *fakeTeamsAPI {
	return &fakeTeamsAPI{runsByKey: map[string]string{}, dissolved: map[string]bool{}}
}

func (f *fakeTeamsAPI) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runsByKey)
}
func (f *fakeTeamsAPI) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }
func (f *fakeTeamsAPI) isDissolved(run string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dissolved[run]
}

func (f *fakeTeamsAPI) apiError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"error":{"code":"x","message":%q}}`, msg)
}

func (f *fakeTeamsAPI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Header.Get("Idempotency-Key")
		switch r.URL.Path {
		case "/teams/form":
			f.mu.Lock()
			f.formCalls++
			var requested tetherclient.TeamForm
			_ = json.Unmarshal(body, &requested)
			f.formRequests = append(f.formRequests, requested)
			delay, hook, refuse, lose, srvErr := f.formDelay, f.onForm, f.refuseForm, f.loseResponse, f.serverError
			f.mu.Unlock()
			if delay > 0 {
				time.Sleep(delay)
			}
			if hook != nil {
				hook()
			}
			if refuse != 0 {
				f.apiError(w, refuse, "launch not allowlisted")
				return
			}
			if srvErr {
				f.apiError(w, 500, "boom")
				return
			}
			f.mu.Lock()
			run, seen := f.runsByKey[key]
			if seen && f.replayStatus != 0 {
				st := f.replayStatus
				f.mu.Unlock()
				f.apiError(w, st, "idempotency conflict")
				return
			}
			if !seen {
				run = fmt.Sprintf("run-%d", len(f.runsByKey)+1)
				f.runsByKey[key] = run
				var form tetherclient.TeamForm
				_ = json.Unmarshal(body, &form)
				f.forms = append(f.forms, form)
				f.teamIDs = append(f.teamIDs, fmt.Sprint(form.Team["id"]))
				f.teamRun = append(f.teamRun, run)
			}
			f.mu.Unlock()
			if lose {
				c, _, _ := w.(http.Hijacker).Hijack()
				_ = c.Close()
				return
			}
			_, _ = fmt.Fprintf(w, `{"run":{"id":%q,"status":"working"}}`, run)
		case "/teams/delegate":
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.failDeleg {
				f.apiError(w, 502, "orchestrator not ready")
				return
			}
			var m map[string]string
			_ = json.Unmarshal(body, &m)
			f.delegations = append(f.delegations, m)
			_, _ = w.Write([]byte(`{}`))
		case "/teams/dissolve":
			f.mu.Lock()
			hook := f.onDissolve
			f.mu.Unlock()
			if hook != nil {
				hook()
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			var m map[string]string
			_ = json.Unmarshal(body, &m)
			if !f.knows(m["run_id"]) {
				f.apiError(w, 404, "no such run")
				return
			}
			if f.failDissolve {
				f.apiError(w, 500, "stuck")
				return
			}
			f.dissolved[m["run_id"]] = true
			_, _ = w.Write([]byte(`{}`))
		case "/teams/roster":
			f.mu.Lock()
			defer f.mu.Unlock()
			run := r.URL.Query().Get("run_id")
			if run == "" { // listing
				items := []string{}
				for i, id := range f.teamIDs {
					if !f.hideRoster {
						items = append(items, fmt.Sprintf(`{"run":{"id":%q,"team_id":%q}}`, f.teamRun[i], id))
					}
				}
				_, _ = fmt.Fprintf(w, `{"runs":[%s]}`, strings.Join(items, ","))
				return
			}
			if !f.knows(run) {
				f.apiError(w, 404, "no such run")
				return
			}
			st := "active"
			if f.ended || f.dissolved[run] {
				st = "stopped"
			}
			_, _ = fmt.Fprintf(w, `{"runs":[{"run":{"id":%q,"status":"working"},"members":[`+
				`{"id":"own","slot":"coordinator","status":"active"},`+
				`{"id":"o","slot":"orchestrator","status":%q},{"id":"w","slot":"workers","status":%q}]}]}`, run, st, st)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// knows reports whether run was formed. Caller holds f.mu.
func (f *fakeTeamsAPI) knows(run string) bool {
	for _, id := range f.runsByKey {
		if id == run {
			return true
		}
	}
	return false
}

func fastCfg(cfg service.UnitConfig) service.UnitConfig {
	cfg.PollInterval, cfg.Grace, cfg.RetryBackoff = 10*time.Millisecond, time.Millisecond, time.Millisecond
	if cfg.ResultGrace == 0 {
		cfg.ResultGrace = time.Hour // off unless a test turns it on
	}
	return cfg
}

func unitSetup(t *testing.T, fake *fakeTeamsAPI, cfg service.UnitConfig) *service.Service {
	svc := setupService(t)
	svc.Unit.Configure(tetherclient.New(fake.server(t).URL, "op"), fastCfg(cfg))
	t.Cleanup(svc.Unit.Stop)
	return svc
}

func planMeta(extra map[string]any) map[string]any {
	p := map[string]any{"workstream_id": "ws-7", "goal": "add the thing", "done_when": []any{"thing works"}}
	for k, v := range extra {
		p[k] = v
	}
	return map[string]any{"unit_plan": p}
}

func newUnitTask(t *testing.T, svc *service.Service, extra map[string]any) string {
	return newUnitTaskOnDone(t, svc, extra, "")
}

func newUnitTaskOnDone(t *testing.T, svc *service.Service, extra map[string]any, onDone string) string {
	rec, err := svc.Task.Create(service.TaskCreateInput{Title: "Unit task", Description: "x", Manual: true, Metadata: planMeta(extra), OnDone: onDone})
	require.NoError(t, err)
	return rec.ID
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitStatus(t *testing.T, svc *service.Service, id, status string) {
	t.Helper()
	waitFor(t, "task "+status, func() bool { r, _ := svc.Task.Get(id); return r.Status == status })
}

func unitRun(t *testing.T, svc *service.Service, id string) service.UnitRun {
	rec, err := svc.Task.Get(id)
	require.NoError(t, err)
	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(rec.Metadata.String), &meta))
	b, _ := json.Marshal(meta["unit_run"])
	var r service.UnitRun
	require.NoError(t, json.Unmarshal(b, &r))
	return r
}

func dispatch(svc *service.Service, id string) (*service.UnitDispatchResult, error) {
	return svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
}

func TestUnitDispatchHappyPathAndIdempotent(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, map[string]any{"worker_runtime": "agy"})

	res, err := dispatch(svc, id)
	require.NoError(t, err)
	assert.Equal(t, "run-1", res.RunID)
	assert.False(t, res.Reused)

	rec, _ := svc.Task.Get(id)
	assert.Equal(t, "doing", rec.Status)
	run := unitRun(t, svc, id)
	assert.Equal(t, "run-1", run.RunID)
	assert.Equal(t, service.UnitActive, run.State)
	assert.True(t, strings.Contains(rec.Metadata.String, "unit_plan"), "the plan survives the run record")

	require.Len(t, fake.forms, 1)
	assert.Equal(t, "torque-unit-"+id+"-a1", run.Key)
	var workerPin string
	for _, s := range fake.forms[0].Team["slots"].([]any) {
		if m := s.(map[string]any); m["name"] == "workers" {
			workerPin = m["definition_ref"].(map[string]any)["id"].(string)
		}
	}
	assert.Equal(t, "team-worker-agy", workerPin)
	assert.Equal(t, map[string]int{"coordinator": 0, "orchestrator": 1, "workers": 1, "reviewer": 1}, fake.forms[0].Launch.Counts)

	require.Len(t, fake.delegations, 1)
	assert.Equal(t, "@orchestrator", fake.delegations[0]["address"])
	for _, want := range []string{id, "ws-7", "add the thing", "thing works", "@chief"} {
		assert.Contains(t, fake.delegations[0]["body"], want)
	}
	comments, _ := svc.Comment.ListForTask(id)
	assert.Contains(t, comments[len(comments)-1].Content, "run-1")

	again, err := dispatch(svc, id)
	require.NoError(t, err)
	assert.True(t, again.Reused)
	assert.Equal(t, 1, fake.runCount())
}

func TestUnitConcurrentDispatchFormsOneTeam(t *testing.T) {
	fake := newFake()
	fake.formDelay = 40 * time.Millisecond
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)

	var wg sync.WaitGroup
	runs := make([]string, 8)
	for i := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := dispatch(svc, id)
			if assert.NoError(t, err) {
				runs[i] = res.RunID
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, fake.runCount(), "exactly one team")
	for _, r := range runs {
		assert.Equal(t, "run-1", r)
	}
}

func TestUnitFinishWithResultGoesToReview(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.NoError(t, err)

	_, err = svc.Comment.AddForTask(id, "orchestrator", "RESULT run-1: merged PR #9, CI green")
	require.NoError(t, err)
	fake.set(func() { fake.ended = true })

	waitStatus(t, svc, id, "review")
	assert.Equal(t, service.UnitDone, unitRun(t, svc, id).State)
	comments, _ := svc.Comment.ListForTask(id)
	last := comments[len(comments)-1].Content
	assert.Contains(t, last, "merged PR #9")
	assert.Contains(t, last, "on_done=review")
}

func TestUnitOnDoneCloseAndNotify(t *testing.T) {
	for _, onDone := range []string{"close", "notify"} {
		t.Run(onDone, func(t *testing.T) {
			fake := newFake()
			svc := unitSetup(t, fake, service.UnitConfig{})
			var notified atomic.Value
			svc.Unit.SetNotifier(func(taskID, _ string) { notified.Store(taskID) })
			id := newUnitTaskOnDone(t, svc, nil, onDone)
			_, err := dispatch(svc, id)
			require.NoError(t, err)
			_, _ = svc.Comment.AddForTask(id, "orchestrator", "RESULT run-1: done")
			fake.set(func() { fake.ended = true })

			waitStatus(t, svc, id, "done")
			if onDone == "notify" {
				assert.Equal(t, id, notified.Load())
			} else {
				assert.Nil(t, notified.Load())
			}
		})
	}
}

func TestUnitFinishWithoutResultBlocks(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.NoError(t, err)
	fake.set(func() { fake.ended = true })

	waitStatus(t, svc, id, "blocked")
	rec, _ := svc.Task.Get(id)
	assert.Contains(t, rec.BlockedReason, "dissolved without a result")
	assert.Equal(t, service.UnitBlocked, unitRun(t, svc, id).State)
}

func TestUnitResultWithoutDissolveFinishesAndDissolves(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{ResultGrace: 30 * time.Millisecond})
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.NoError(t, err)

	_, _ = svc.Comment.AddForTask(id, "orchestrator", "RESULT run-1: all merged")
	waitStatus(t, svc, id, "review") // the run never ended on its own
	assert.True(t, fake.isDissolved("run-1"), "Torque dissolves the run once the result has settled")
}

func TestUnitTimeoutWithoutResultDissolvesAndBlocks(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	var offset atomic.Int64
	svc.Unit.SetClock(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) })
	id := newUnitTask(t, svc, map[string]any{"timeout": "1h"})
	_, err := dispatch(svc, id)
	require.NoError(t, err)

	offset.Store(int64(3 * time.Hour))
	waitStatus(t, svc, id, "blocked")
	rec, _ := svc.Task.Get(id)
	assert.Contains(t, rec.BlockedReason, "timed out")
	assert.True(t, fake.isDissolved("run-1"))
}

func TestUnitTimeoutWithResultSucceeds(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	var offset atomic.Int64
	svc.Unit.SetClock(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) })
	id := newUnitTask(t, svc, map[string]any{"timeout": "1h"})
	_, err := dispatch(svc, id)
	require.NoError(t, err)

	_, _ = svc.Comment.AddForTask(id, "orchestrator", "RESULT run-1: finished just in time")
	offset.Store(int64(3 * time.Hour))
	waitStatus(t, svc, id, "review")
	assert.True(t, fake.isDissolved("run-1"))
}

func TestUnitFormRefusedCleanlyLeavesTodoWithReason(t *testing.T) {
	fake := newFake()
	fake.refuseForm = 403
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)

	_, err := dispatch(svc, id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "launch not allowlisted")
	rec, _ := svc.Task.Get(id)
	assert.Equal(t, "todo", rec.Status)
	assert.True(t, rec.Manual)
	assert.Contains(t, rec.BlockedReason, "refused")
	assert.Equal(t, service.UnitFailed, unitRun(t, svc, id).State)
	assert.Equal(t, 0, fake.runCount())
}

// The first formation succeeds in Tether but its answer is lost. Torque must
// not mark the attempt failed and start a second team under a new key.
func TestUnitFormResponseLostKeepsKeyAndRecovers(t *testing.T) {
	fake := newFake()
	fake.loseResponse = true
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)

	_, err := dispatch(svc, id)
	require.Error(t, err)
	assert.Equal(t, 1, fake.runCount(), "Tether formed a team Torque has not learned about")
	run := unitRun(t, svc, id)
	assert.Equal(t, service.UnitForming, run.State)
	assert.True(t, run.Ambiguous)
	rec, _ := svc.Task.Get(id)
	assert.Equal(t, "todo", rec.Status)
	assert.True(t, rec.Manual, "kept out of the scheduler's reach")

	fake.set(func() { fake.loseResponse = false })
	res, err := dispatch(svc, id)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Attempt, "same attempt, same key")
	assert.Equal(t, "run-1", res.RunID, "the team that was already formed")
	assert.Equal(t, 1, fake.runCount(), "no second team")
}

func TestUnitServerErrorOnFormIsAmbiguousNotFailed(t *testing.T) {
	fake := newFake()
	fake.serverError = true
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.Error(t, err)
	assert.Equal(t, service.UnitForming, unitRun(t, svc, id).State)
	assert.Greater(t, fake.formCalls, 1, "retried with the same key before giving up")
}

func TestUnitFormLostOnceRecoversInCall(t *testing.T) {
	fake := newFake()
	var n atomic.Int32
	fake.onForm = func() {
		if n.Add(1) == 1 {
			fake.set(func() { fake.loseResponse = true })
		} else {
			fake.set(func() { fake.loseResponse = false })
		}
	}
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	res, err := dispatch(svc, id)
	require.NoError(t, err)
	assert.Equal(t, "run-1", res.RunID)
	assert.Equal(t, 1, fake.runCount())
}

func TestUnitDelegateFailureDissolvesRun(t *testing.T) {
	fake := newFake()
	fake.failDeleg = true
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)

	_, err := dispatch(svc, id)
	require.Error(t, err)
	assert.True(t, fake.isDissolved("run-1"), "a formed run is never left running")
	rec, _ := svc.Task.Get(id)
	assert.Equal(t, "todo", rec.Status)
	assert.Contains(t, rec.BlockedReason, "briefing the orchestrator failed")

	fake.set(func() { fake.failDeleg = false })
	res, err := dispatch(svc, id)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempt)
	assert.Equal(t, "run-2", res.RunID)
}

func TestUnitRecordFailureAfterFormDissolvesRun(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	// The task disappears while the team is being formed, so recording the run fails.
	fake.onForm = func() { _ = svc.Task.Delete(id) }

	_, err := dispatch(svc, id)
	require.Error(t, err)
	assert.True(t, fake.isDissolved("run-1"))
}

func TestUnitCapacityLimit(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{MaxActive: 1})
	a, b := newUnitTask(t, svc, nil), newUnitTask(t, svc, nil)
	_, err := dispatch(svc, a)
	require.NoError(t, err)
	_, err = dispatch(svc, b)
	require.ErrorIs(t, err, service.ErrUnitCapacity)
	assert.Equal(t, 1, fake.runCount())
	rec, _ := svc.Task.Get(b)
	assert.Equal(t, "todo", rec.Status)
	assert.Empty(t, rec.BlockedReason, "a capacity refusal is transient and parks nothing")
	again, err := dispatch(svc, a)
	require.NoError(t, err, "re-dispatching the live unit is not a new unit")
	assert.True(t, again.Reused)
}

func TestUnitDispatchRefusals(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})

	t.Run("no plan", func(t *testing.T) {
		rec, err := svc.Task.Create(service.TaskCreateInput{Title: "plain", Manual: true})
		require.NoError(t, err)
		_, err = dispatch(svc, rec.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no execution plan")
	})
	t.Run("several workers need a pool", func(t *testing.T) {
		id := newUnitTask(t, svc, nil)
		_, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id, Workers: 2})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pool identities")
		assert.Zero(t, fake.formCalls)
	})
	t.Run("not configured", func(t *testing.T) {
		bare := setupService(t)
		id := newUnitTask(t, bare, nil)
		_, err := dispatch(bare, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TORQUE_TETHER_OPERATOR_TOKEN")
	})
}

// The scheduler hook parks a refused task so the next tick does not pick it again.
func TestUnitDispatchTaskParksRefusals(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, map[string]any{"workers": 3}) // no pool configured
	rec, _ := svc.Task.Get(id)
	handled, err := svc.Unit.DispatchTask(context.Background(), *rec)
	require.Error(t, err)
	assert.True(t, handled)
	rec, _ = svc.Task.Get(id)
	assert.True(t, rec.Manual)
	assert.Contains(t, rec.BlockedReason, "pool identities")
	assert.Equal(t, "todo", rec.Status)
}

func TestUnitPoolWorkers(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{WorkerPool: []string{"msg://agent/local/w-a", "msg://agent/local/w-b", "msg://agent/local/w-c"}})
	id := newUnitTask(t, svc, map[string]any{"workers": 2})
	_, err := dispatch(svc, id)
	require.NoError(t, err)
	assert.Equal(t, 2, fake.forms[0].Launch.Counts["workers"])
	assert.Equal(t, []string{"msg://agent/local/w-a", "msg://agent/local/w-b"}, fake.forms[0].Launch.PoolIdentities["workers"])
}

// A unit whose run could not be confirmed ended must not be dispatched again
// until it is.
func TestUnitUnconfirmedRunBlocksRedispatch(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	var offset atomic.Int64
	svc.Unit.SetClock(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) })
	id := newUnitTask(t, svc, map[string]any{"timeout": "1h"})
	_, err := dispatch(svc, id)
	require.NoError(t, err)

	fake.set(func() { fake.failDissolve = true })
	offset.Store(int64(3 * time.Hour))
	waitStatus(t, svc, id, "blocked")
	assert.True(t, unitRun(t, svc, id).Unconfirmed)

	_, err = dispatch(svc, id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not confirmed ended")
	assert.Equal(t, 1, fake.runCount())

	fake.set(func() { fake.failDissolve = false })
	offset.Store(0)
	res, err := dispatch(svc, id)
	require.NoError(t, err)
	assert.Equal(t, "run-2", res.RunID)
	assert.True(t, fake.isDissolved("run-1"))
}

func TestUnitTaskLeavingDoingDissolvesRun(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.NoError(t, err)

	require.NoError(t, svc.Task.Transition(context.Background(), id, "paused"))
	waitFor(t, "run dissolved", func() bool { return fake.isDissolved("run-1") })
	waitFor(t, "record blocked", func() bool { return unitRun(t, svc, id).State == service.UnitBlocked })
}

func TestUnitResumeReattaches(t *testing.T) {
	fake := newFake()
	svc, store := setupServiceWithStore(t)
	svc.Unit.Configure(tetherclient.New(fake.server(t).URL, "op"), fastCfg(service.UnitConfig{}))
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.NoError(t, err)
	svc.Unit.Stop() // the process goes away; the run lives on in Tether

	svc2 := service.New(store)
	svc2.Unit.Configure(tetherclient.New(fake.server(t).URL, "op"), fastCfg(service.UnitConfig{}))
	t.Cleanup(svc2.Unit.Stop)
	svc2.Unit.Resume()

	_, _ = svc2.Comment.AddForTask(id, "orchestrator", "RESULT run-1: ok")
	fake.set(func() { fake.ended = true })
	waitStatus(t, svc2, id, "review")
}

// The dispatcher's record survives caller metadata writes, and a caller can
// neither forge one nor change the stored one.
func TestUnitRunProtectedFromMetadataWrites(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.NoError(t, err)

	// Replacing the metadata without unit_run keeps the stored record.
	v := unitNullStr(`{"unit_plan":{"workstream_id":"ws-7","goal":"edited goal","done_when":["x"]},"note":"hi"}`)
	require.NoError(t, svc.Task.Update(id, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &v}}))
	assert.Equal(t, "run-1", unitRun(t, svc, id).RunID)
	rec, _ := svc.Task.Get(id)
	assert.Contains(t, rec.Metadata.String, "edited goal")

	// Writing back what is stored is tolerated; changing it is refused.
	forged := unitNullStr(`{"unit_plan":{"workstream_id":"ws-7","goal":"g","done_when":["x"]},"unit_run":{"state":"done","run_id":"forged"}}`)
	err = svc.Task.Update(id, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &forged}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unit_run")
	assert.Equal(t, "run-1", unitRun(t, svc, id).RunID)

	// No unit_run is stored on a fresh task: it cannot be planted on create or update.
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "x", Manual: true, Metadata: map[string]any{"unit_run": map[string]any{"state": "active", "run_id": "r"}}})
	require.Error(t, err)
	plain, err := svc.Task.Create(service.TaskCreateInput{Title: "plain", Manual: true})
	require.NoError(t, err)
	err = svc.Task.Update(plain.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &forged}})
	require.Error(t, err)

	// Even past the service check, the store strips a unit_run it does not hold.
	raw := unitNullStr(`{"unit_run":{"state":"active"},"a":1}`)
	require.NoError(t, svc.Store().UpdateTask(plain.ID, sqlstore.TaskUpdate{Metadata: &raw}))
	got, _ := svc.Task.Get(plain.ID)
	assert.NotContains(t, got.Metadata.String, "unit_run")
}

// A RESULT comment counts only if it carries the run id; anyone else's is ignored.
func TestUnitForeignResultCommentIgnored(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{ResultGrace: 20 * time.Millisecond})
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.NoError(t, err)

	for _, c := range []string{"RESULT: looks done to me", "RESULT run-99: other run", "result run-1: wrong case"} {
		_, _ = svc.Comment.AddForTask(id, "someone", c)
	}
	time.Sleep(300 * time.Millisecond)
	rec, _ := svc.Task.Get(id)
	assert.Equal(t, "doing", rec.Status, "foreign RESULT comments do not finish the unit")
	assert.False(t, fake.isDissolved("run-1"))

	_, _ = svc.Comment.AddForTask(id, "orchestrator", "RESULT run-1: real")
	waitStatus(t, svc, id, "review")
}

// A formation that keeps answering 409 on the same key (or keeps failing with
// 5xx) is not replayed forever. With the team visible in Tether's rosters it is
// found and dissolved.
func TestUnitPersistentFormFailureGivesUpAndDissolvesFoundTeam(t *testing.T) {
	for name, setup := range map[string]func(*fakeTeamsAPI){
		"409 on replay": func(f *fakeTeamsAPI) { f.replayStatus = 409 },
		"500":           func(f *fakeTeamsAPI) { f.serverError = true },
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFake()
			fake.loseResponse = true // the first call forms a team but loses the answer
			svc := unitSetup(t, fake, service.UnitConfig{MaxReplays: 3})
			id := newUnitTask(t, svc, nil)

			_, err := dispatch(svc, id)
			require.Error(t, err)
			fake.set(func() { fake.loseResponse = false; setup(fake) })

			for i := 0; i < 10 && unitRun(t, svc, id).State == service.UnitForming; i++ {
				_, err = dispatch(svc, id)
				require.Error(t, err)
			}
			assert.Equal(t, service.UnitFailed, unitRun(t, svc, id).State, "gave up instead of replaying forever")
			assert.True(t, fake.isDissolved("run-1"), "the team that was found is dissolved")
			rec, _ := svc.Task.Get(id)
			assert.True(t, rec.Manual)
			assert.Equal(t, "todo", rec.Status)
		})
	}
}

func TestUnitGiveUpNotFoundParksUnconfirmedAndCountsAgainstLimit(t *testing.T) {
	fake := newFake()
	fake.serverError = true
	fake.hideRoster = true
	svc := unitSetup(t, fake, service.UnitConfig{MaxReplays: 2, MaxActive: 1})
	id := newUnitTask(t, svc, nil)
	other := newUnitTask(t, svc, nil)

	for i := 0; i < 6 && unitRun(t, svc, id).State != service.UnitUnconfirmed; i++ {
		_, err := dispatch(svc, id)
		require.Error(t, err)
	}
	run := unitRun(t, svc, id)
	require.Equal(t, service.UnitUnconfirmed, run.State)
	assert.True(t, run.Unconfirmed)
	rec, _ := svc.Task.Get(id)
	assert.True(t, rec.Manual)
	assert.Contains(t, rec.BlockedReason, "torque-unit-"+id+"-a1", "names the idempotency key")
	n := fake.formCalls
	_, err := dispatch(svc, id)
	require.Error(t, err)
	assert.Equal(t, n, fake.formCalls, "no more replays")

	// It counts against the unit limit.
	fake.set(func() { fake.serverError = false })
	_, err = dispatch(svc, other)
	require.ErrorIs(t, err, service.ErrUnitCapacity)

	// Cleared by the operator, the task starts a new attempt.
	res, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id, ClearUnconfirmed: true})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempt)
}

func TestUnitGiveUpByAge(t *testing.T) {
	fake := newFake()
	fake.serverError = true
	fake.hideRoster = true
	svc := unitSetup(t, fake, service.UnitConfig{MaxReplays: 100, FormingGiveUp: time.Minute})
	var offset atomic.Int64
	svc.Unit.SetClock(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) })
	id := newUnitTask(t, svc, nil)
	_, err := dispatch(svc, id)
	require.Error(t, err)
	require.Equal(t, service.UnitForming, unitRun(t, svc, id).State)

	offset.Store(int64(time.Hour))
	_, err = dispatch(svc, id)
	require.Error(t, err)
	assert.Equal(t, service.UnitUnconfirmed, unitRun(t, svc, id).State)
}

func TestUnitPlanValidatedOnTaskWrites(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Task.Create(service.TaskCreateInput{Title: "bad", Manual: true, Metadata: planMeta(map[string]any{"workers": 9})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unit_plan.workers")

	_, err = svc.Task.Create(service.TaskCreateInput{Title: "bad2", Manual: true, Metadata: map[string]any{"unit_plan": map[string]any{"goal": "g"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workstream_id")
}

func TestUnitTemplateCreatesValidTask(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.UnitOfWorkTemplate())
	require.NoError(t, err)
	task, err := svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "unit-of-work", Title: "Ship X",
		Vars: map[string]string{"workstream_id": "ws-3", "goal": "ship X", "done_when": "X merged\nX tested"},
	})
	require.NoError(t, err)
	assert.True(t, task.Manual, "approval is the single manual=false promotion")
	assert.True(t, strings.Contains(task.Metadata.String, "ws-3"))

	_, err = svc.Template.Instantiate(service.TemplateInstantiateInput{TemplateID: "unit-of-work", Title: "no vars"})
	require.Error(t, err)
}

func unitNullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// Brief addresses must resolve to the slots actually sent to Tether, including
// pooled workers. This exercises the public dispatch boundary, not a copied roster.
func TestUnitBriefAddressesMatchDefinition(t *testing.T) {
	for _, workers := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			fake := newFake()
			svc := unitSetup(t, fake, service.UnitConfig{WorkerPool: []string{"msg://agent/local/w-a", "msg://agent/local/w-b", "msg://agent/local/w-c"}})
			id := newUnitTask(t, svc, map[string]any{"workers": workers})
			_, err := dispatch(svc, id)
			require.NoError(t, err)
			require.Len(t, fake.forms, 1)
			require.Len(t, fake.delegations, 1)
			slots := map[string]bool{}
			for _, raw := range fake.forms[0].Team["slots"].([]any) {
				slots[raw.(map[string]any)["name"].(string)] = true
			}
			for _, slot := range []string{"workers", "reviewer"} {
				assert.True(t, slots[slot], "brief address must name a defined slot")
				assert.Contains(t, fake.delegations[0]["body"], "@"+slot)
			}
			assert.Equal(t, workers, fake.forms[0].Launch.Counts["workers"])
		})
	}
}

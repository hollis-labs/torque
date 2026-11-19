package service_test

import (
	"context"
	"encoding/json"
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

	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
)

// fakeTeamsAPI is a minimal Tether team API over HTTP.
type fakeTeamsAPI struct {
	mu          sync.Mutex
	failForm    bool
	failDeleg   bool
	ended       bool
	forms       []tetherclient.TeamForm
	formKeys    []string
	delegations []map[string]string
	dissolved   []string
}

func (f *fakeTeamsAPI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/teams/form":
			if f.failForm {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"denied","message":"launch not allowlisted"}}`))
				return
			}
			var form tetherclient.TeamForm
			_ = json.Unmarshal(body, &form)
			f.forms = append(f.forms, form)
			f.formKeys = append(f.formKeys, r.Header.Get("Idempotency-Key"))
			_, _ = w.Write([]byte(`{"run":{"id":"run-42","status":"working"}}`))
		case "/teams/delegate":
			if f.failDeleg {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"orchestrator not ready"}}`))
				return
			}
			var m map[string]string
			_ = json.Unmarshal(body, &m)
			f.delegations = append(f.delegations, m)
			_, _ = w.Write([]byte(`{}`))
		case "/teams/dissolve":
			var m map[string]string
			_ = json.Unmarshal(body, &m)
			f.dissolved = append(f.dissolved, m["run_id"])
			_, _ = w.Write([]byte(`{}`))
		case "/teams/roster":
			st := "working"
			if f.ended {
				st = "completed"
			}
			_, _ = w.Write([]byte(`{"runs":[{"run":{"id":"run-42","status":"` + st + `"},"members":[]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeTeamsAPI) setEnded() { f.mu.Lock(); f.ended = true; f.mu.Unlock() }
func (f *fakeTeamsAPI) dissolvedRuns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dissolved...)
}

func unitSetup(t *testing.T, fake *fakeTeamsAPI, cfg service.UnitConfig) *service.Service {
	svc := setupService(t)
	cfg.PollInterval = 10 * time.Millisecond
	cfg.Grace = time.Millisecond
	svc.Unit.Configure(tetherclient.New(fake.server(t).URL, "op"), cfg)
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
	rec, err := svc.Task.Create(service.TaskCreateInput{Title: "Unit task", Description: "x", Manual: true, Metadata: planMeta(extra)})
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

func TestUnitDispatchHappyPathAndIdempotent(t *testing.T) {
	fake := &fakeTeamsAPI{}
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, map[string]any{"worker_runtime": "agy"})

	res, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, err)
	assert.Equal(t, "run-42", res.RunID)
	assert.False(t, res.Reused)

	rec, _ := svc.Task.Get(id)
	assert.Equal(t, "doing", rec.Status)
	run := unitRun(t, svc, id)
	assert.Equal(t, "run-42", run.RunID)
	assert.Equal(t, service.UnitActive, run.State)

	// Idempotency key from task id + attempt; agy workers use the agy pin.
	require.Len(t, fake.forms, 1)
	assert.Equal(t, "torque-unit-"+id+"-a1", fake.formKeys[0])
	slots := fake.forms[0].Team["slots"].([]any)
	var workerPin string
	for _, s := range slots {
		m := s.(map[string]any)
		if m["name"] == "workers" {
			workerPin = m["definition_ref"].(map[string]any)["id"].(string)
		}
	}
	assert.Equal(t, "team-worker-agy", workerPin)
	assert.Equal(t, map[string]int{"coordinator": 0, "orchestrator": 1, "workers": 1, "reviewer": 1}, fake.forms[0].Launch.Counts)

	// The orchestrator is briefed with the contract.
	require.Len(t, fake.delegations, 1)
	assert.Equal(t, "@orchestrator", fake.delegations[0]["address"])
	for _, want := range []string{id, "ws-7", "add the thing", "thing works", "@chief"} {
		assert.Contains(t, fake.delegations[0]["body"], want)
	}

	// Comment records the run id.
	comments, _ := svc.Comment.ListForTask(id)
	require.NotEmpty(t, comments)
	assert.Contains(t, comments[len(comments)-1].Content, "run-42")

	// Re-dispatch returns the live run and forms nothing.
	again, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, err)
	assert.True(t, again.Reused)
	assert.Equal(t, "run-42", again.RunID)
	assert.Len(t, fake.forms, 1)
}

func TestUnitFinishWithResultGoesToReview(t *testing.T) {
	fake := &fakeTeamsAPI{}
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	_, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, err)

	_, err = svc.Comment.AddForTask(id, "orchestrator", "RESULT: merged PR #9, CI green")
	require.NoError(t, err)
	fake.setEnded()

	waitFor(t, "task in review", func() bool { r, _ := svc.Task.Get(id); return r.Status == "review" })
	assert.Equal(t, service.UnitDone, unitRun(t, svc, id).State)
	comments, _ := svc.Comment.ListForTask(id)
	last := comments[len(comments)-1].Content
	assert.Contains(t, last, "merged PR #9")
	assert.Contains(t, last, "dissolved with a result")
}

func TestUnitFinishWithoutResultBlocks(t *testing.T) {
	fake := &fakeTeamsAPI{}
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)
	_, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, err)
	fake.setEnded()

	waitFor(t, "task blocked", func() bool { r, _ := svc.Task.Get(id); return r.Status == "blocked" })
	rec, _ := svc.Task.Get(id)
	assert.Contains(t, rec.BlockedReason, "dissolved without a result")
	assert.Equal(t, service.UnitBlocked, unitRun(t, svc, id).State)
}

func TestUnitTimeoutDissolvesAndBlocks(t *testing.T) {
	fake := &fakeTeamsAPI{}
	svc := unitSetup(t, fake, service.UnitConfig{})
	var offset atomic.Int64
	svc.Unit.SetClock(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) })
	id := newUnitTask(t, svc, map[string]any{"timeout": "1h"})
	_, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, err)

	offset.Store(int64(3 * time.Hour)) // past the deadline, run still working
	waitFor(t, "task blocked", func() bool { r, _ := svc.Task.Get(id); return r.Status == "blocked" })
	rec, _ := svc.Task.Get(id)
	assert.Contains(t, rec.BlockedReason, "timed out")
	assert.Contains(t, rec.BlockedReason, "was dissolved")
	assert.Equal(t, []string{"run-42"}, fake.dissolvedRuns())
}

func TestUnitFormFailureLeavesTodoWithReason(t *testing.T) {
	fake := &fakeTeamsAPI{failForm: true}
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)

	_, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "launch not allowlisted")

	rec, _ := svc.Task.Get(id)
	assert.Equal(t, "todo", rec.Status)
	assert.Contains(t, rec.BlockedReason, "team formation failed")
	assert.True(t, rec.Manual, "set manual so the scheduler does not loop on it")
	assert.Equal(t, service.UnitFailed, unitRun(t, svc, id).State)
	assert.Empty(t, fake.dissolvedRuns(), "nothing was formed, nothing to dissolve")
	assert.Empty(t, fake.delegations)
}

func TestUnitDelegateFailureDissolvesRun(t *testing.T) {
	fake := &fakeTeamsAPI{failDeleg: true}
	svc := unitSetup(t, fake, service.UnitConfig{})
	id := newUnitTask(t, svc, nil)

	_, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.Error(t, err)
	assert.Equal(t, []string{"run-42"}, fake.dissolvedRuns(), "a formed run is never left running")

	rec, _ := svc.Task.Get(id)
	assert.Equal(t, "todo", rec.Status)
	assert.Contains(t, rec.BlockedReason, "briefing the orchestrator failed")
	assert.Contains(t, rec.BlockedReason, "run-42 dissolved")

	// Re-dispatch after the cause is fixed uses a new attempt key.
	fake.mu.Lock()
	fake.failDeleg = false
	fake.mu.Unlock()
	res, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempt)
	assert.Equal(t, "torque-unit-"+id+"-a2", fake.formKeys[len(fake.formKeys)-1])
}

func TestUnitDispatchRefusals(t *testing.T) {
	fake := &fakeTeamsAPI{}
	svc := unitSetup(t, fake, service.UnitConfig{})

	t.Run("no plan", func(t *testing.T) {
		rec, err := svc.Task.Create(service.TaskCreateInput{Title: "plain", Manual: true})
		require.NoError(t, err)
		_, err = svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: rec.ID})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no execution plan")
	})
	t.Run("several workers need a pool", func(t *testing.T) {
		id := newUnitTask(t, svc, nil)
		_, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id, Workers: 2})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pool identities")
		assert.Empty(t, fake.forms)
	})
	t.Run("not configured", func(t *testing.T) {
		bare := setupService(t)
		id := newUnitTask(t, bare, nil)
		_, err := bare.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TORQUE_TETHER_OPERATOR_TOKEN")
	})
}

func TestUnitPoolWorkers(t *testing.T) {
	fake := &fakeTeamsAPI{}
	svc := unitSetup(t, fake, service.UnitConfig{WorkerPool: []string{"msg://agent/local/w-a", "msg://agent/local/w-b", "msg://agent/local/w-c"}})
	id := newUnitTask(t, svc, map[string]any{"workers": 2})
	_, err := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, err)
	assert.Equal(t, 2, fake.forms[0].Launch.Counts["workers"])
	assert.Equal(t, []string{"msg://agent/local/w-a", "msg://agent/local/w-b"}, fake.forms[0].Launch.PoolIdentities["workers"])
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

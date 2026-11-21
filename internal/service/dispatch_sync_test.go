package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
	"github.com/stretchr/testify/require"
)

const registryID = "workstream:01M7VQE9DMDXCS0Z1MVA7045D6"
const registryID2 = "workstream:01M7VQE9DMDXCS0Z1MVA7045D7"

type identityFixture struct {
	projectDomains                               []any
	mu                                           sync.Mutex
	registryDown, closed, statusDown, loseStatus bool
	registryCalls                                int
	registryKeys, logicalKeys, tokens            []string
	bodies                                       []map[string]any
	accepted                                     map[string]map[string]any
	srv                                          *httptest.Server
	file                                         string
}

func newIdentityFixture(t *testing.T) *identityFixture {
	f := &identityFixture{accepted: map[string]map[string]any{}, file: filepath.Join(t.TempDir(), "credential")}
	require.NoError(t, os.WriteFile(f.file, []byte("test-credential\n"), 0600))
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "Bearer test-credential", r.Header.Get("Authorization"))
		require.Equal(t, "POST", r.Method)
		switch r.URL.Path {
		case "/v1/workstreams/ensure":
			f.registryCalls++
			f.projectDomains = append(f.projectDomains, body["project_urns"])
			f.registryKeys = append(f.registryKeys, r.Header.Get("Idempotency-Key"))
			f.logicalKeys = append(f.logicalKeys, body["key"].(string))
			require.NotEmpty(t, r.Header.Get("Idempotency-Key"))
			require.NotContains(t, body, "id")
			require.NotContains(t, body, "name")
			if f.closed {
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`{"error":{"code":"workstream_closed","message":"test-credential must not escape"}}`))
				return
			}
			if f.registryDown {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"test-credential"}}`))
				return
			}
			id := registryID
			if body["key"] == "new-key" {
				id = registryID2
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "created": f.registryCalls == 1, "version_token": "v1"})
		case "/v1/event/write":
			cs := body["consumer_state"].(map[string]any)
			require.Contains(t, []string{"started", "progress", "blocked", "review", "done", "failed", "cancelled", "note"}, cs["state"])
			require.Contains(t, cs, "task_id")
			require.Contains(t, cs, "run_id")
			require.NotEmpty(t, cs["workstream_key"])
			require.IsType(t, "", body["body"])

			f.bodies = append(f.bodies, body)
			key := body["idempotency_key"].(string)
			require.Equal(t, key, r.Header.Get("Idempotency-Key"))
			if f.statusDown {
				w.WriteHeader(503)
				return
			}
			if old, ok := f.accepted[key]; ok {
				require.Equal(t, old, body)
			} else {
				f.accepted[key] = body
			}
			if f.loseStatus {
				f.loseStatus = false
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"id":"event"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}
func (f *identityFixture) config() service.DispatchSyncConfig {
	return service.DispatchSyncConfig{RegistryURL: f.srv.URL, RegistryTokenFile: f.file, TesseractURL: f.srv.URL, TesseractTokenFile: f.file, PollInterval: time.Hour, Timeout: time.Second}
}
func (f *identityFixture) set(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }
func (f *identityFixture) events() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, v := range f.accepted {
		out = append(out, v)
	}
	return out
}
func syncFixture(t *testing.T, svc *service.Service, f *identityFixture) *service.DispatchSync {
	d, e := service.NewDispatchSync(svc.Store(), f.config())
	require.NoError(t, e)
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	return d
}
func taskMeta(t *testing.T, svc *service.Service, id string) map[string]any {
	rec, e := svc.Task.Get(id)
	require.NoError(t, e)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(rec.Metadata.String), &m))
	return m
}
func TestDispatchIdentityRegistryOwnedIdempotentAndProtected(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	id := newUnitTask(t, svc, nil)
	rec, e := svc.Task.Get(id)
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), rec))
	require.NoError(t, d.Prepare(context.Background(), rec))
	m := taskMeta(t, svc, id)
	require.Equal(t, registryID, m["workstream_id"])
	require.Equal(t, "ws-7", m["unit_plan"].(map[string]any)["workstream_id"])
	f.mu.Lock()
	require.Equal(t, []string{"ws-7", "ws-7"}, f.logicalKeys)
	require.Equal(t, f.registryKeys[0], f.registryKeys[1])
	f.mu.Unlock()
	raw := sql.NullString{String: `{"note":"concurrent edit","workstream_id":"caller-value"}`, Valid: true}
	require.NoError(t, svc.Task.Update(id, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &raw}}))
	require.Equal(t, registryID, taskMeta(t, svc, id)["workstream_id"])
	f.set(func() { f.closed = true })
	require.ErrorIs(t, d.Prepare(context.Background(), rec), service.ErrWorkstreamClosed)
	require.Equal(t, "closed", taskMeta(t, svc, id)["dispatch_workstream"].(map[string]any)["state"])
	require.NotContains(t, fmt.Sprint(taskMeta(t, svc, id)), "test-credential")
}
func TestDispatchIdentityOutageRetriesWithoutHoldingDispatch(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	f := newIdentityFixture(t)
	f.registryDown = true
	d := syncFixture(t, svc, f)
	svc.Unit.SetDispatchSync(d)
	id := newUnitTask(t, svc, nil)
	run, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, e)
	require.Equal(t, service.UnitActive, run.State)
	m := taskMeta(t, svc, id)
	require.Empty(t, m["workstream_id"])
	require.Equal(t, "pending", m["dispatch_workstream"].(map[string]any)["state"])
	require.Error(t, d.Tick(context.Background()))
	require.Empty(t, f.events())
	f.set(func() { f.registryDown = false })
	require.NoError(t, d.Tick(context.Background()))
	m = taskMeta(t, svc, id)
	require.Equal(t, registryID, m["workstream_id"])
	require.Equal(t, registryID, m["unit_run"].(map[string]any)["workstream_id"])
	require.NotEmpty(t, f.events())
	for _, body := range f.events() {
		require.Equal(t, registryID, body["workstream_id"])
		require.Equal(t, id, body["data"].(map[string]any)["task_id"])
		require.Equal(t, run.RunID, body["data"].(map[string]any)["run_id"])
	}
}
func TestDispatchIdentityClosedRefusesFormation(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	f := newIdentityFixture(t)
	f.closed = true
	d := syncFixture(t, svc, f)
	svc.Unit.SetDispatchSync(d)
	id := newUnitTask(t, svc, nil)
	_, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.ErrorContains(t, e, "workstream_closed")
	require.Zero(t, fake.runCount())
	require.Equal(t, "closed", taskMeta(t, svc, id)["dispatch_workstream"].(map[string]any)["state"])
}
func TestDispatchStatusUnitLifecycleAndAcceptedLostResponse(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	svc.Unit.SetDispatchSync(d)
	id := newUnitTask(t, svc, nil)
	run, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, e)
	require.Equal(t, registryID, taskMeta(t, svc, id)["unit_run"].(map[string]any)["workstream_id"])
	_, e = svc.Comment.AddForTask(id, "orchestrator", "RESULT "+run.RunID+": shipped")
	require.NoError(t, e)
	fake.set(func() { fake.ended = true })
	waitFor(t, "unit finishes", func() bool { rec, _ := svc.Task.Get(id); return rec.Status == "review" })
	f.set(func() { f.loseStatus = true })
	require.Error(t, d.Tick(context.Background()))
	// Reconstructing the consumer simulates restart after upstream accepted but
	// before Torque persisted the cursor. Payload and key must be byte-stable.
	d2, e := service.NewDispatchSync(svc.Store(), f.config())
	require.NoError(t, e)
	require.NoError(t, d2.Tick(context.Background()))
	states := map[string]int{}
	for _, body := range f.events() {
		require.Equal(t, "project/agent-os/event/status", body["namespace"])
		require.Equal(t, registryID, body["workstream_id"])
		data := body["data"].(map[string]any)
		require.Equal(t, id, data["task_id"])
		require.Equal(t, run.RunID, data["run_id"])
		require.Equal(t, registryID, data["workstream_id"])
		require.Equal(t, "torque-dispatcher", body["author"].(map[string]any)["agent_id"])
		states[body["consumer_state"].(map[string]any)["state"].(string)]++
	}
	require.Equal(t, map[string]int{"started": 2, "progress": 1, "done": 1, "note": 1, "review": 1}, states)
}
func TestDispatchStatusExecutorTransitionsAndRetry(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	task, e := svc.Task.Create(service.TaskCreateInput{Title: "executor task", Metadata: map[string]any{"ops_id": "ops-work"}})
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), task))
	require.NoError(t, svc.Task.Transition(context.Background(), task.ID, "doing"))
	run, e := svc.Store().CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "cli", Status: "running"})
	require.NoError(t, e)
	require.NoError(t, svc.Store().CompleteRun(run, sqlstore.RunCompletion{Status: "failed"}))
	for _, state := range []string{"blocked", "review", "done"} {
		require.NoError(t, svc.Task.Transition(context.Background(), task.ID, state))
	}
	f.set(func() { f.statusDown = true })
	require.Error(t, d.Tick(context.Background()))
	require.Empty(t, f.events())
	f.set(func() { f.statusDown = false })
	require.NoError(t, d.Tick(context.Background()))
	states := map[string]int{}
	for _, body := range f.events() {
		state := body["consumer_state"].(map[string]any)["state"].(string)
		states[state]++
		require.Equal(t, registryID, body["workstream_id"])
	}
	require.Equal(t, map[string]int{"started": 1, "progress": 1, "failed": 1, "blocked": 1, "review": 1, "done": 1}, states)
}
func TestDispatchStatusOutageEventsKeepOriginalIdentityAfterPlanChanges(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	f.registryDown = true
	d := syncFixture(t, svc, f)
	task, e := svc.Task.Create(service.TaskCreateInput{Title: "changing task", Metadata: map[string]any{"workstream_key": "old-key"}})
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), task))
	require.NoError(t, svc.Task.Transition(context.Background(), task.ID, "blocked"))
	raw := sql.NullString{String: `{"workstream_key":"new-key"}`, Valid: true}
	require.NoError(t, svc.Task.Update(task.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &raw}}))
	task, e = svc.Task.Get(task.ID)
	require.NoError(t, e)
	f.set(func() { f.registryDown = false })
	require.NoError(t, d.Prepare(context.Background(), task))
	require.NoError(t, svc.Task.Transition(context.Background(), task.ID, "review"))
	require.NoError(t, d.Tick(context.Background()))
	require.Equal(t, registryID2, taskMeta(t, svc, task.ID)["workstream_id"])
	for _, body := range f.events() {
		state := body["consumer_state"].(map[string]any)["state"]
		if state == "blocked" {
			require.Equal(t, registryID, body["workstream_id"])
		} else {
			require.Equal(t, registryID2, body["workstream_id"])
		}
	}
}
func TestDispatchSyncConfigFilesRequired(t *testing.T) {
	svc := setupService(t)
	for _, cfg := range []service.DispatchSyncConfig{{RegistryURL: "https://registry"}, {RegistryURL: "https://user:password@registry", RegistryTokenFile: "/token"}, {TesseractURL: "https://tesseract", TesseractTokenFile: "/token"}} {
		_, e := service.NewDispatchSync(svc.Store(), cfg)
		require.Error(t, e)
	}
}

func TestDispatchLegacyOpsKeySurvivesCanonicalStamp(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	rec, e := svc.Task.Create(service.TaskCreateInput{Title: "legacy Ops task", Metadata: map[string]any{"workstream_id": "ops-123"}})
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), rec))
	require.NoError(t, d.Prepare(context.Background(), rec))
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []string{"ops-123", "ops-123"}, f.logicalKeys)
	require.Equal(t, f.registryKeys[0], f.registryKeys[1])
}
func TestDispatchStatusRetryPinsPayloadAndRetention(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	task, e := svc.Task.Create(service.TaskCreateInput{Title: "outbox task"})
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), task))
	require.NoError(t, svc.Task.Transition(context.Background(), task.ID, "doing"))
	f.set(func() { f.loseStatus = true })
	require.Error(t, d.Tick(context.Background()))
	require.Len(t, f.events(), 1)
	// Run creation between attempts changes the inferred run, but must not
	// change a payload that Tesseract may already have accepted.
	_, e = svc.Store().CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "cli", Status: "running"})
	require.NoError(t, e)
	_, e = svc.Store().OutboxTrimBefore(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, e)
	require.NoError(t, d.Tick(context.Background()))
	require.Len(t, f.events(), 2)
}
func TestDispatchUnitLifecycleAtomicAndIdempotent(t *testing.T) {
	svc := setupService(t)
	id := newUnitTask(t, svc, nil)
	run := `{"state":"forming","key":"dispatch-key","attempt":1}`
	w, e := svc.Store().BeginWriteTx(context.Background())
	require.NoError(t, e)
	require.NoError(t, w.UpdateTask(id, sqlstore.TaskUpdate{UnitRun: &run}))
	require.NoError(t, w.Rollback())
	count := func() int {
		var n int
		require.NoError(t, svc.Store().ReadDB().QueryRow(`SELECT COUNT(*) FROM event_outbox WHERE kind='unit.lifecycle'`).Scan(&n))
		return n
	}
	require.Zero(t, count())
	require.NoError(t, svc.Store().UpdateTask(id, sqlstore.TaskUpdate{UnitRun: &run}))
	require.Equal(t, 1, count())
	require.NoError(t, svc.Store().UpdateTask(id, sqlstore.TaskUpdate{UnitRun: &run}))
	require.Equal(t, 1, count())
}
func TestDispatchRegistryTimeoutDoesNotHoldWriterOrOtherTask(t *testing.T) {
	svc := setupService(t)
	file := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(file, []byte("test"), 0600))
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["key"] == "slow" {
			close(entered)
			<-release
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": registryID})
	}))
	defer srv.Close()
	defer close(release)
	d, e := service.NewDispatchSync(svc.Store(), service.DispatchSyncConfig{RegistryURL: srv.URL, RegistryTokenFile: file, Timeout: 100 * time.Millisecond})
	require.NoError(t, e)
	a, e := svc.Task.Create(service.TaskCreateInput{Title: "slow", Metadata: map[string]any{"workstream_key": "slow"}})
	require.NoError(t, e)
	b, e := svc.Task.Create(service.TaskCreateInput{Title: "fast"})
	require.NoError(t, e)
	done := make(chan error, 1)
	go func() { done <- d.Prepare(context.Background(), a) }()
	<-entered
	// The slow network request owns neither SQLite's writer nor a global lock.
	require.NoError(t, svc.Task.Transition(context.Background(), b.ID, "blocked"))
	require.NoError(t, d.Prepare(context.Background(), b))
	require.Equal(t, registryID, taskMeta(t, svc, b.ID)["workstream_id"])
	require.NoError(t, <-done)
	require.Equal(t, "pending", taskMeta(t, svc, a.ID)["dispatch_workstream"].(map[string]any)["state"])
}

func TestDispatchCredentialFileRotationAndFailureMark(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	rec, e := svc.Task.Create(service.TaskCreateInput{Title: "rotating credential"})
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(f.file, nil, 0600))
	require.NoError(t, d.Prepare(context.Background(), rec))
	mark := taskMeta(t, svc, rec.ID)["dispatch_workstream"].(map[string]any)
	require.Equal(t, "pending", mark["state"])
	require.Equal(t, "credential_unavailable", mark["last_error"])
	require.NoError(t, os.WriteFile(f.file, []byte("test-credential\n"), 0600))
	require.NoError(t, d.Tick(context.Background()))
	require.Equal(t, registryID, taskMeta(t, svc, rec.ID)["workstream_id"])
}
func TestDispatchStatusFirstEnableStartsAfterPrunedLog(t *testing.T) {
	svc := setupService(t)
	rec, e := svc.Task.Create(service.TaskCreateInput{Title: "old task"})
	require.NoError(t, e)
	require.NoError(t, svc.Task.Transition(context.Background(), rec.ID, "blocked"))
	_, e = svc.Store().OutboxTrimBefore(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, e)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	require.NoError(t, d.Tick(context.Background()))
	require.Empty(t, f.events())
}

func TestDispatchUnitReattemptPreservesEachRunIdentity(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	svc.Unit.SetDispatchSync(d)
	id := newUnitTask(t, svc, nil)
	one, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, e)
	fake.set(func() { fake.ended = true })
	waitFor(t, "first unit blocks", func() bool { rec, _ := svc.Task.Get(id); return rec.Status == "blocked" })
	fake.set(func() { fake.ended = false })
	two, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, e)
	require.NotEqual(t, one.RunID, two.RunID)
	require.NoError(t, d.Tick(context.Background()))
	dispatched := map[string]bool{}
	for _, body := range f.events() {
		if body["data"].(map[string]any)["transition"] == "dispatched" {
			dispatched[body["data"].(map[string]any)["run_id"].(string)] = true
		}
	}
	require.Equal(t, map[string]bool{one.RunID: true, two.RunID: true}, dispatched)
}

func TestDispatchAmbiguousReplayKeepsWorkstreamKey(t *testing.T) {
	fake := newFake()
	fake.serverError = true
	svc := unitSetup(t, fake, service.UnitConfig{})
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	svc.Unit.SetDispatchSync(d)
	id := newUnitTask(t, svc, nil)
	_, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.Error(t, e)
	raw, _ := json.Marshal(planMeta(map[string]any{"workstream_id": "new-key"}))
	metadata := sql.NullString{String: string(raw), Valid: true}
	require.NoError(t, svc.Task.Update(id, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &metadata}}))
	fake.set(func() { fake.serverError = false })
	run, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
	require.NoError(t, e)
	require.Equal(t, 1, run.Attempt)
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []string{"ws-7", "ws-7"}, f.logicalKeys)
	require.Equal(t, registryID, taskMeta(t, svc, id)["workstream_id"])
}

func TestDispatchClosedBindingStickyAcrossOutageAndRestart(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	rec, e := svc.Task.Create(service.TaskCreateInput{Title: "closed"})
	require.NoError(t, e)
	f.set(func() { f.closed = true })
	require.ErrorIs(t, d.Prepare(context.Background(), rec), service.ErrWorkstreamClosed)
	f.set(func() { f.closed = false; f.registryDown = true })
	require.ErrorIs(t, d.Prepare(context.Background(), rec), service.ErrWorkstreamClosed)
	require.NoError(t, os.WriteFile(f.file, nil, 0600))
	restarted, e := service.NewDispatchSync(svc.Store(), f.config())
	require.NoError(t, e)
	require.ErrorIs(t, restarted.Prepare(context.Background(), rec), service.ErrWorkstreamClosed)
	f.mu.Lock()
	require.Equal(t, 1, f.registryCalls)
	f.mu.Unlock()
	require.Equal(t, "closed", taskMeta(t, svc, rec.ID)["dispatch_workstream"].(map[string]any)["state"])
	require.NoError(t, os.WriteFile(f.file, []byte("test-credential"), 0600))
	f.set(func() { f.registryDown = false })
	raw := sql.NullString{String: `{"workstream_key":"new-key"}`, Valid: true}
	require.NoError(t, svc.Task.Update(rec.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &raw}}))
	rec, e = svc.Task.Get(rec.ID)
	require.NoError(t, e)
	require.NoError(t, restarted.Prepare(context.Background(), rec))
	require.Equal(t, registryID2, taskMeta(t, svc, rec.ID)["workstream_id"])
}
func TestDispatchCanonicalProjectDomainAndSlug(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	p, _, e := svc.Store().CreateProjectByReference(&sqlstore.ProjectRecord{Name: "Tether Display Name", ExternalAuthority: "registry", ExternalID: "project:tether"})
	require.NoError(t, e)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	rec, e := svc.Task.Create(service.TaskCreateInput{Title: "mapped project", ProjectID: p.ID})
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), rec))
	require.NoError(t, svc.Task.Transition(context.Background(), rec.ID, "blocked"))
	require.NoError(t, d.Tick(context.Background()))
	f.mu.Lock()
	require.Equal(t, []any{"project:tether"}, f.projectDomains[0])
	require.NotEqual(t, p.ID, "project:tether")
	f.mu.Unlock()
	require.Equal(t, "project/tether/event/status", f.events()[0]["namespace"])
	// A local project with no Registry mapping must not mint in a PRJ-* domain.
	local := &sqlstore.ProjectRecord{ID: "PRJ-UNMAPPED", Name: "local-only"}
	require.NoError(t, svc.Store().CreateProject(local))
	other, e := svc.Task.Create(service.TaskCreateInput{Title: "unmapped", ProjectID: local.ID})
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), other))
	require.Equal(t, "project_unmapped", taskMeta(t, svc, other.ID)["dispatch_workstream"].(map[string]any)["last_error"])
	f.mu.Lock()
	require.Equal(t, 1, f.registryCalls)
	f.mu.Unlock()
}
func TestDispatchUnitDefaultProjectAndGatedLaunchContext(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			fake := newFake()
			svc := unitSetup(t, fake, service.UnitConfig{DefaultProject: "PRJ-20261120-0001", RegistryLaunchContext: enabled})
			require.NoError(t, svc.Store().CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20261120-0001", Name: "Agent OS", ExternalAuthority: "registry", ExternalID: "project:agent-os"}))
			f := newIdentityFixture(t)
			d := syncFixture(t, svc, f)
			svc.Unit.SetDispatchSync(d)
			id := newUnitTask(t, svc, nil)
			_, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
			require.NoError(t, e)
			rec, e := svc.Task.Get(id)
			require.NoError(t, e)
			require.Equal(t, "PRJ-20261120-0001", rec.ProjectID.String)
			fake.mu.Lock()
			formation := fake.formRequests[0]
			fake.mu.Unlock()
			raw, e := json.Marshal(formation)
			require.NoError(t, e)
			var body map[string]any
			require.NoError(t, json.Unmarshal(raw, &body))
			launch := body["launch"].(map[string]any)
			if enabled {
				require.Equal(t, id, launch["task_id"])
				require.Equal(t, registryID, launch["workstream_id"])
			} else {
				require.NotContains(t, launch, "task_id")
				require.NotContains(t, launch, "workstream_id")
			}
			f.mu.Lock()
			require.Equal(t, []any{"project:agent-os"}, f.projectDomains[0])
			f.mu.Unlock()
		})
	}
}
func TestDispatchUnitResultUsesClosedVocabulary(t *testing.T) {
	for _, tc := range []struct{ text, state string }{{"DONE: shipped", "done"}, {"FAILED: build failed", "failed"}, {"BLOCKED: missing dependency", "blocked"}, {`{"status":"failed"}`, "failed"}} {
		t.Run(tc.text, func(t *testing.T) {
			fake := newFake()
			svc := unitSetup(t, fake, service.UnitConfig{})
			f := newIdentityFixture(t)
			d := syncFixture(t, svc, f)
			svc.Unit.SetDispatchSync(d)
			id := newUnitTask(t, svc, nil)
			run, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
			require.NoError(t, e)
			_, e = svc.Comment.AddForTask(id, "orchestrator", "RESULT "+run.RunID+": "+tc.text)
			require.NoError(t, e)
			fake.set(func() { fake.ended = true })
			waitFor(t, "result", func() bool { r, _ := svc.Task.Get(id); return r.Status == "review" })
			require.NoError(t, d.Tick(context.Background()))
			found := false
			for _, body := range f.events() {
				if body["data"].(map[string]any)["transition"] == "RESULT" {
					cs := body["consumer_state"].(map[string]any)
					require.Equal(t, tc.state, cs["state"])
					require.Equal(t, id, cs["task_id"])
					require.Equal(t, run.RunID, cs["run_id"])
					require.Equal(t, run.RunID, cs["team_id"])
					require.Equal(t, "ws-7", cs["workstream_key"])
					found = true
				}
			}
			require.True(t, found)
		})
	}
}
func TestDispatchStatusWaitsForFormedRunBeforeSealing(t *testing.T) {
	fake := newFake()
	svc := unitSetup(t, fake, service.UnitConfig{})
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	svc.Unit.SetDispatchSync(d)
	id := newUnitTask(t, svc, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	fake.onForm = func() { close(entered); <-release }
	finished := make(chan *service.UnitDispatchResult, 1)
	fail := make(chan error, 1)
	go func() {
		run, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
		finished <- run
		fail <- e
	}()
	<-entered
	require.ErrorContains(t, d.Tick(context.Background()), "awaiting team run id")
	require.Empty(t, f.events())
	var sealed int
	require.NoError(t, svc.Store().ReadDB().QueryRow(`SELECT COUNT(*) FROM dispatch_status_payloads`).Scan(&sealed))
	require.Zero(t, sealed)
	close(release)
	run := <-finished
	require.NoError(t, <-fail)
	require.NoError(t, d.Tick(context.Background()))
	for _, body := range f.events() {
		require.Equal(t, run.RunID, body["consumer_state"].(map[string]any)["run_id"])
	}
}

func TestDispatchRepeatedStatesHaveDistinctTransitionKeys(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	rec, e := svc.Task.Create(service.TaskCreateInput{Title: "repeated transitions"})
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), rec))
	for _, state := range []string{"blocked", "doing", "blocked", "doing", "cancelled"} {
		require.NoError(t, svc.Task.Transition(context.Background(), rec.ID, state))
	}
	require.NoError(t, d.Tick(context.Background()))
	require.Len(t, f.events(), 5)
	counts := map[string]int{}
	for _, body := range f.events() {
		counts[body["consumer_state"].(map[string]any)["state"].(string)]++
	}
	require.Equal(t, map[string]int{"blocked": 2, "started": 2, "cancelled": 1}, counts)
}
func TestDispatchUnitConfigReadsDefaultAndWireGate(t *testing.T) {
	t.Setenv("TORQUE_UNIT_DEFAULT_PROJECT", "PRJ-20261120-0001")
	t.Setenv("TORQUE_UNIT_REGISTRY_LAUNCH_CONTEXT", "true")
	cfg := service.UnitConfigFromEnv()
	require.Equal(t, "PRJ-20261120-0001", cfg.DefaultProject)
	require.True(t, cfg.RegistryLaunchContext)
}

func TestDispatchClosedLedgerRejectsStalePendingSave(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	d := syncFixture(t, svc, f)
	rec, e := svc.Task.Create(service.TaskCreateInput{Title: "stale binding"})
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), rec))
	old, e := svc.Store().DispatchWorkstream(context.Background(), rec.ID)
	require.NoError(t, e)
	closed := old
	closed.State = "closed"
	closed.LastError = "workstream_closed"
	require.NoError(t, svc.Store().SaveDispatchWorkstream(context.Background(), closed))
	old.State = "pending"
	old.LastError = "unavailable"
	require.ErrorIs(t, svc.Store().SaveDispatchWorkstream(context.Background(), old), sqlstore.ErrDispatchWorkstreamClosed)
	require.Equal(t, "closed", taskMeta(t, svc, rec.ID)["dispatch_workstream"].(map[string]any)["state"])
}

func TestDispatchStaleOutageSavePreservesLearnedCanonicalID(t *testing.T) {
	svc := setupService(t)
	f := newIdentityFixture(t)
	f.registryDown = true
	d := syncFixture(t, svc, f)
	rec, e := svc.Task.Create(service.TaskCreateInput{Title: "learned ID"})
	require.NoError(t, e)
	require.NoError(t, d.Prepare(context.Background(), rec))
	stale, e := svc.Store().DispatchWorkstream(context.Background(), rec.ID)
	require.NoError(t, e)
	require.Empty(t, stale.ID)
	f.set(func() { f.registryDown = false })
	require.NoError(t, d.Prepare(context.Background(), rec))
	stale.State = "pending"
	stale.LastError = "unavailable"
	require.NoError(t, svc.Store().SaveDispatchWorkstream(context.Background(), stale))
	require.Equal(t, registryID, taskMeta(t, svc, rec.ID)["workstream_id"])
}

func TestDispatchOutageAmbiguousRecoveryPreservesFormationBytes(t *testing.T) {
	for _, initialGate := range []bool{false, true} {
		t.Run(fmt.Sprint(initialGate), func(t *testing.T) {
			fake := newFake()
			fake.loseResponse = true
			fake.replayStatus = http.StatusServiceUnavailable
			svc := unitSetup(t, fake, service.UnitConfig{RegistryLaunchContext: initialGate})
			f := newIdentityFixture(t)
			f.registryDown = true
			d := syncFixture(t, svc, f)
			svc.Unit.SetDispatchSync(d)
			id := newUnitTask(t, svc, nil)
			_, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
			require.Error(t, e)
			require.Equal(t, 1, fake.runCount())
			meta := taskMeta(t, svc, id)
			before := meta["unit_run"].(map[string]any)
			require.Equal(t, service.UnitForming, before["state"])
			require.Equal(t, true, before["ambiguous"])
			frozen, e := json.Marshal(before["formation"])
			require.NoError(t, e)
			key := before["key"]
			replacement := planMeta(nil)
			replacement["concurrent_note"] = "retain during recovery"
			payload, e := json.Marshal(replacement)
			require.NoError(t, e)
			raw := sql.NullString{String: string(payload), Valid: true}
			require.NoError(t, svc.Task.Update(id, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &raw}}))
			f.set(func() { f.registryDown = false })
			require.ErrorContains(t, d.Tick(context.Background()), "awaiting team run id")
			require.Equal(t, registryID, taskMeta(t, svc, id)["workstream_id"])
			fake.set(func() { fake.loseResponse = false; fake.replayStatus = 0 })
			// Toggle deployment configuration after an attempted formation. Recovery
			// must use the saved request rather than rebuilding it with the new gate.
			svc.Unit.Configure(tetherclient.New(fake.server(t).URL, "op"), fastCfg(service.UnitConfig{RegistryLaunchContext: !initialGate}))
			run, e := svc.Unit.Dispatch(context.Background(), service.UnitDispatchInput{TaskID: id})
			require.NoError(t, e)
			require.Equal(t, 1, run.Attempt)
			require.Equal(t, "run-1", run.RunID)
			require.Equal(t, 1, fake.runCount())
			meta = taskMeta(t, svc, id)
			after := meta["unit_run"].(map[string]any)
			actual, e := json.Marshal(after["formation"])
			require.NoError(t, e)
			require.Equal(t, string(frozen), string(actual))
			require.Equal(t, key, after["key"])
			require.Equal(t, "retain during recovery", meta["concurrent_note"])
			require.Equal(t, registryID, after["workstream_id"])
			fake.mu.Lock()
			defer fake.mu.Unlock()
			require.GreaterOrEqual(t, len(fake.formRequests), 4)
			first, e := json.Marshal(fake.formRequests[0])
			require.NoError(t, e)
			for _, request := range fake.formRequests[1:] {
				next, e := json.Marshal(request)
				require.NoError(t, e)
				require.Equal(t, string(first), string(next))
			}
			require.Empty(t, fake.formRequests[0].Launch.WorkstreamID)
			if initialGate {
				require.Equal(t, id, fake.formRequests[0].Launch.TaskID)
			} else {
				require.Empty(t, fake.formRequests[0].Launch.TaskID)
			}
		})
	}
}

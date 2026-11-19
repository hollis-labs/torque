package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/tetherclient"
	"github.com/hollis-labs/torque/internal/unitplan"
)

// UnitRunKey is the task metadata key that records the team run for a unit of
// work. It is written by the dispatcher only.
const UnitRunKey = "unit_run"

// Unit run states.
const (
	UnitForming = "forming" // key chosen, form may or may not have happened
	UnitActive  = "active"  // team formed and briefed; being tracked
	UnitDone    = "done"    // dissolved with a result
	UnitBlocked = "blocked" // dissolved without a result, timed out, or tracking gave up
	UnitFailed  = "failed"  // formation or briefing failed; no run is live
)

// unitAuthor is the comment author for dispatcher-written comments.
const unitAuthor = "torque-dispatch"

// resultPrefix marks the orchestrator's result comment on the task.
const resultPrefix = "RESULT:"

// UnitTeams is the slice of Tether's team API the dispatcher uses.
type UnitTeams interface {
	FormTeam(ctx context.Context, key string, f tetherclient.TeamForm) (*tetherclient.TeamResult, error)
	DelegateTeam(ctx context.Context, key, runID, address, body string) (*tetherclient.TeamResult, error)
	DissolveTeam(ctx context.Context, key, runID string) error
	TeamRun(ctx context.Context, runID string) (*tetherclient.TeamRunInfo, error)
}

// UnitPins are the definition pins for each role. They must match
// teams-policy.yaml in the Tether catalog (the policy binds one launch, and so
// one model, to each pin).
type UnitPins struct {
	Orchestrator tetherclient.DefinitionRef `json:"orchestrator"`
	WorkerSol    tetherclient.DefinitionRef `json:"worker_sol"`
	WorkerAgy    tetherclient.DefinitionRef `json:"worker_agy"`
	Reviewer     tetherclient.DefinitionRef `json:"reviewer"`
}

const liveDigest = "sha256:c8692289df79259473f3907beb7951e6a2b6fcbb0fd4cca18bd4797493ff043c"

// DefaultUnitPins are the pins in the live teams-policy.yaml as of 2026-11-19
// (team-orchestrator-astra, team-worker-sol, team-worker-agy-w1,
// team-reviewer-merger-astra).
func DefaultUnitPins() UnitPins {
	return UnitPins{
		Orchestrator: tetherclient.DefinitionRef{ID: "team-orchestrator", Revision: "r1", Digest: liveDigest},
		WorkerSol:    tetherclient.DefinitionRef{ID: "team-worker-sol", Revision: "r1", Digest: liveDigest},
		WorkerAgy:    tetherclient.DefinitionRef{ID: "team-worker-agy", Revision: "r1", Digest: liveDigest},
		Reviewer:     tetherclient.DefinitionRef{ID: "team-reviewer-merger", Revision: "r1", Digest: liveDigest},
	}
}

// UnitConfig tunes the dispatcher.
type UnitConfig struct {
	PollInterval   time.Duration // run-state poll; default 15s
	DefaultTimeout time.Duration // run lifetime when the plan sets none; default 4h
	Grace          time.Duration // wait past the deadline for Tether's own reaper before dissolving; default 2m
	// WorkerPool lists pre-registered worker identities (msg://agent/...). Needed
	// only for plans with more than one worker.
	WorkerPool []string
	Pins       UnitPins
}

// UnitConfigFromEnv reads TORQUE_UNIT_POLL, TORQUE_UNIT_TIMEOUT,
// TORQUE_UNIT_WORKER_POOL (comma-separated URNs) and TORQUE_UNIT_PINS (JSON
// overriding DefaultUnitPins).
func UnitConfigFromEnv() UnitConfig {
	cfg := UnitConfig{Pins: DefaultUnitPins()}
	if d, err := time.ParseDuration(os.Getenv("TORQUE_UNIT_POLL")); err == nil && d > 0 {
		cfg.PollInterval = d
	}
	if d, err := time.ParseDuration(os.Getenv("TORQUE_UNIT_TIMEOUT")); err == nil && d > 0 {
		cfg.DefaultTimeout = d
	}
	for _, p := range strings.Split(os.Getenv("TORQUE_UNIT_WORKER_POOL"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.WorkerPool = append(cfg.WorkerPool, p)
		}
	}
	if raw := os.Getenv("TORQUE_UNIT_PINS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Pins); err != nil {
			log.Printf("[unit] ignoring TORQUE_UNIT_PINS: %v", err)
			cfg.Pins = DefaultUnitPins()
		}
	}
	return cfg
}

// UnitRun is the dispatcher's record on the task (metadata.unit_run).
type UnitRun struct {
	RunID         string `json:"run_id,omitempty"`
	Attempt       int    `json:"attempt"`
	State         string `json:"state"`
	Key           string `json:"key"`
	Workers       int    `json:"workers"`
	WorkerRuntime string `json:"worker_runtime"`
	StartedAt     string `json:"started_at,omitempty"`
	Deadline      string `json:"deadline,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// UnitDispatchInput names the task and optional plan overrides.
type UnitDispatchInput struct {
	TaskID        string
	Workers       int    // 0 = plan value
	WorkerRuntime string // "" = plan value
}

// UnitDispatchResult is the outcome of a dispatch.
type UnitDispatchResult struct {
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id"`
	Attempt int    `json:"attempt"`
	State   string `json:"state"`
	// Reused is true when the task already had a live run and nothing was formed.
	Reused bool `json:"reused"`
}

// UnitService dispatches a unit of work (a task with an execution plan) as a
// Tether team and tracks the run to its end. No agent does the launching.
type UnitService struct {
	store   *sqlstore.Store
	task    *TaskService
	comment *CommentService

	teams UnitTeams
	cfg   UnitConfig

	mu       sync.Mutex
	locks    map[string]*sync.Mutex // per-task dispatch lock
	watching map[string]bool
	baseCtx  context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	now      func() time.Time
}

func newUnitService(store *sqlstore.Store, task *TaskService, comment *CommentService) *UnitService {
	ctx, cancel := context.WithCancel(context.Background())
	return &UnitService{
		store: store, task: task, comment: comment,
		cfg:   UnitConfig{Pins: DefaultUnitPins()},
		locks: map[string]*sync.Mutex{}, watching: map[string]bool{},
		baseCtx: ctx, cancel: cancel, now: time.Now,
	}
}

// Configure installs the Tether team API and settings. A nil teams leaves
// dispatch disabled with a clear error. Zero config durations take defaults.
func (u *UnitService) Configure(teams UnitTeams, cfg UnitConfig) {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 15 * time.Second
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 4 * time.Hour
	}
	if cfg.Grace <= 0 {
		cfg.Grace = 2 * time.Minute
	}
	if cfg.Pins == (UnitPins{}) {
		cfg.Pins = DefaultUnitPins()
	}
	u.teams, u.cfg = teams, cfg
}

// SetClock replaces the time source (tests).
func (u *UnitService) SetClock(now func() time.Time) { u.now = now }

// Enabled reports whether a Tether team API is configured.
func (u *UnitService) Enabled() bool { return u != nil && u.teams != nil }

// Stop cancels every watcher and waits for them. Runs stay live in Tether and
// are picked up again by Resume on the next start.
func (u *UnitService) Stop() {
	u.cancel()
	u.wg.Wait()
}

func (u *UnitService) taskLock(id string) *sync.Mutex {
	u.mu.Lock()
	defer u.mu.Unlock()
	l := u.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		u.locks[id] = l
	}
	return l
}

func taskMetadata(t *sqlstore.TaskRecord) map[string]any {
	m := map[string]any{}
	if t.Metadata.Valid && t.Metadata.String != "" {
		_ = json.Unmarshal([]byte(t.Metadata.String), &m)
	}
	return m
}

func unitRunFrom(meta map[string]any) (UnitRun, bool) {
	raw, ok := meta[UnitRunKey]
	if !ok {
		return UnitRun{}, false
	}
	b, _ := json.Marshal(raw)
	var r UnitRun
	if json.Unmarshal(b, &r) != nil {
		return UnitRun{}, false
	}
	return r, true
}

func (u *UnitService) saveRun(taskID string, r UnitRun, upd sqlstore.TaskUpdate) error {
	// Metadata writes replace the column, so merge unit_run into the current
	// metadata rather than writing it alone (that would drop unit_plan).
	cur, err := u.task.Get(taskID)
	if err != nil {
		return err
	}
	meta := taskMetadata(cur)
	meta[UnitRunKey] = r
	b, _ := json.Marshal(meta)
	md := sql.NullString{String: string(b), Valid: true}
	upd.Metadata = &md
	return u.task.Update(taskID, TaskUpdateInput{TaskUpdate: upd})
}

// HasUnitPlan reports whether a task carries an execution plan.
func HasUnitPlan(t sqlstore.TaskRecord) bool { return unitplan.Present(taskMetadata(&t)) }

// DispatchTask is the scheduler hook: it dispatches a picked task that has an
// execution plan and reports whether the task was handled. A failure leaves the
// task todo, manual and annotated, so the next tick does not pick it again.
func (u *UnitService) DispatchTask(ctx context.Context, t sqlstore.TaskRecord) (bool, error) {
	if !HasUnitPlan(t) {
		return false, nil
	}
	_, err := u.Dispatch(ctx, UnitDispatchInput{TaskID: t.ID})
	return true, err
}

// Dispatch validates the task's plan, forms a team for it, briefs the
// orchestrator, records the run on the task and starts tracking it. It is
// idempotent: a task with a live run returns that run.
func (u *UnitService) Dispatch(ctx context.Context, in UnitDispatchInput) (*UnitDispatchResult, error) {
	if !u.Enabled() {
		return nil, &ValidationError{Field: "tether", Message: "unit dispatch is not configured: set TORQUE_TETHER_URL and TORQUE_TETHER_OPERATOR_TOKEN on the serve process"}
	}
	if in.TaskID == "" {
		return nil, &ValidationError{Field: "task_id", Message: "task_id required"}
	}
	lock := u.taskLock(in.TaskID)
	lock.Lock()
	defer lock.Unlock()

	t, err := u.task.Get(in.TaskID)
	if err != nil {
		return nil, err
	}
	meta := taskMetadata(t)
	prev, hadRun := unitRunFrom(meta)
	if hadRun && prev.State == UnitActive && prev.RunID != "" {
		u.watch(t.ID)
		return &UnitDispatchResult{TaskID: t.ID, RunID: prev.RunID, Attempt: prev.Attempt, State: prev.State, Reused: true}, nil
	}
	switch t.Status {
	case "done", "archived", "cancelled", "abandoned":
		return nil, &ValidationError{Field: "status", Message: fmt.Sprintf("task is %s; a unit is dispatched from todo, backlog, blocked or paused", t.Status)}
	case "doing":
		return nil, &ValidationError{Field: "status", Message: "task is doing but has no live team run; move it to todo (or blocked) to dispatch"}
	}

	plan, err := unitplan.FromMetadata(meta)
	if err != nil {
		return nil, &ValidationError{Field: "metadata.unit_plan", Message: err.Error()}
	}
	if in.Workers != 0 {
		plan.Workers = unitplan.Int(in.Workers)
	}
	if in.WorkerRuntime != "" {
		plan.WorkerRuntime = strings.ToLower(in.WorkerRuntime)
	}
	if err := plan.Validate(); err != nil {
		return nil, &ValidationError{Field: "metadata.unit_plan", Message: err.Error()}
	}
	if int(plan.Workers) > 1 && len(u.cfg.WorkerPool) < int(plan.Workers) {
		return nil, &ValidationError{Field: "workers", Message: fmt.Sprintf("%d workers need %d pre-registered pool identities; TORQUE_UNIT_WORKER_POOL lists %d (use workers=1 or register identities)", plan.Workers, plan.Workers, len(u.cfg.WorkerPool))}
	}

	// Attempt number: a crashed dispatch (state forming) reuses its key, so
	// Tether's idempotency returns the run it already made.
	attempt := prev.Attempt + 1
	if hadRun && prev.State == UnitForming {
		attempt = prev.Attempt
	}
	if attempt < 1 {
		attempt = 1
	}
	key := fmt.Sprintf("torque-unit-%s-a%d", t.ID, attempt)
	timeout := plan.TimeoutDuration(u.cfg.DefaultTimeout)
	started := u.now().UTC()
	run := UnitRun{
		Attempt: attempt, State: UnitForming, Key: key,
		Workers: int(plan.Workers), WorkerRuntime: plan.WorkerRuntime,
		StartedAt: started.Format(time.RFC3339), Deadline: started.Add(timeout).Format(time.RFC3339),
	}
	if err := u.saveRun(t.ID, run, sqlstore.TaskUpdate{}); err != nil {
		return nil, fmt.Errorf("unit dispatch: record attempt: %w", err)
	}

	res, err := u.teams.FormTeam(ctx, key, u.buildForm(t.ID, attempt, plan, timeout))
	if err != nil {
		return nil, u.failDispatch(t.ID, run, "team formation failed", err, "")
	}
	run.RunID = res.RunID()
	if run.RunID == "" {
		return nil, u.failDispatch(t.ID, run, "team formation failed", errors.New("tether returned no run id"), "")
	}
	run.State = UnitActive
	// Record the run id before anything else can fail, so no live run is ever
	// unknown to Torque.
	if err := u.saveRun(t.ID, run, sqlstore.TaskUpdate{}); err != nil {
		return nil, u.failDispatch(t.ID, run, "could not record the team run", err, run.RunID)
	}

	if _, err := u.teams.DelegateTeam(ctx, key+"-delegate", run.RunID, "@orchestrator", plan.Brief(t.ID, t.Title)); err != nil {
		return nil, u.failDispatch(t.ID, run, "briefing the orchestrator failed", err, run.RunID)
	}

	empty := ""
	upd := sqlstore.TaskUpdate{BlockedReason: &empty}
	if err := u.saveRun(t.ID, run, upd); err != nil {
		return nil, u.failDispatch(t.ID, run, "could not update the task", err, run.RunID)
	}
	if err := u.task.TransitionWithComment(ctx, t.ID, "doing",
		fmt.Sprintf("Team run %s formed (attempt %d): 1 orchestrator, %d worker(s) on %s, 1 reviewer. Deadline %s. Tracked by Torque; the team dissolves when the orchestrator reports.", run.RunID, attempt, plan.Workers, plan.WorkerRuntime, run.Deadline),
		unitAuthor, false); err != nil {
		return nil, u.failDispatch(t.ID, run, "could not move the task to doing", err, run.RunID)
	}
	u.watch(t.ID)
	return &UnitDispatchResult{TaskID: t.ID, RunID: run.RunID, Attempt: attempt, State: UnitActive}, nil
}

// failDispatch leaves no session running and no loop behind: it dissolves a
// formed run, records the failure, leaves the task todo with a blocked_reason
// and takes it out of the scheduler's reach (manual) until someone re-dispatches.
func (u *UnitService) failDispatch(taskID string, run UnitRun, what string, cause error, runID string) error {
	reason := fmt.Sprintf("unit dispatch attempt %d: %s: %v", run.Attempt, what, cause)
	if runID != "" {
		if derr := u.dissolve(runID, run.Key); derr != nil {
			reason += fmt.Sprintf(" (and dissolving run %s failed: %v; dissolve it by hand)", runID, derr)
		} else {
			reason += fmt.Sprintf(" (run %s dissolved)", runID)
		}
	}
	run.State, run.Reason = UnitFailed, reason
	manual := true
	if err := u.saveRun(taskID, run, sqlstore.TaskUpdate{BlockedReason: &reason, Manual: &manual}); err != nil {
		log.Printf("[unit] %s: record failure: %v", taskID, err)
	}
	if _, err := u.comment.AddForTask(taskID, unitAuthor, reason+". Task left todo and set manual; fix the cause and dispatch again."); err != nil {
		log.Printf("[unit] %s: failure comment: %v", taskID, err)
	}
	return errors.New(reason)
}

// dissolve ends a run, retrying once; the key makes a retry safe.
func (u *UnitService) dissolve(runID, key string) error {
	var err error
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		err = u.teams.DissolveTeam(ctx, key+"-dissolve", runID)
		cancel()
		if err == nil {
			return nil
		}
		var te *tetherclient.TeamError
		if errors.As(err, &te) && te.Status == 404 {
			return nil
		}
	}
	return err
}

func (u *UnitService) buildForm(taskID string, attempt int, plan unitplan.Plan, timeout time.Duration) tetherclient.TeamForm {
	pins := u.cfg.Pins
	workerPin := pins.WorkerSol
	if plan.WorkerRuntime == unitplan.RuntimeAgy {
		workerPin = pins.WorkerAgy
	}
	slot := func(name string, pin tetherclient.DefinitionRef, min, max int) tetherclient.TeamSlot {
		return tetherclient.TeamSlot{Name: name, DefinitionRef: pin, Resolution: "fresh", Activation: "singleton", Min: min, Max: max}
	}
	workers := slot("workers", workerPin, 1, 1)
	launch := tetherclient.TeamLaunch{Counts: map[string]int{"coordinator": 0, "orchestrator": 1, "workers": int(plan.Workers), "reviewer": 1}}
	if plan.Workers > 1 {
		ids := u.cfg.WorkerPool[:plan.Workers]
		workers.Resolution, workers.Pool, workers.Identities, workers.Activation = "pool", "workers", ids, "concurrent"
		workers.Min, workers.Max = 1, 3
		launch.PoolIdentities = map[string][]string{"workers": ids}
	}
	g := func(from, verb, to string) map[string]string {
		return map[string]string{"from_slot": from, "verb": verb, "to_slot": to}
	}
	grants := []map[string]string{}
	for _, v := range []string{"may_message", "may_assign", "may_delegate"} {
		grants = append(grants, g("coordinator", v, "orchestrator"), g("orchestrator", v, "workers"), g("orchestrator", v, "reviewer"))
	}
	grants = append(grants, g("workers", "may_message", "orchestrator"), g("reviewer", "may_message", "orchestrator"))
	team := map[string]any{
		"id":      "unit-" + strings.ToLower(taskID) + fmt.Sprintf("-a%d", attempt),
		"name":    "Unit " + taskID,
		"version": 1,
		"slots": []tetherclient.TeamSlot{
			slot("coordinator", pins.Orchestrator, 0, 1),
			slot("orchestrator", pins.Orchestrator, 1, 1),
			workers,
			slot("reviewer", pins.Reviewer, 1, 1),
		},
		"phases": []map[string]any{{
			"id": "work", "kind": "flex",
			"active_slots": []string{"coordinator", "orchestrator", "workers", "reviewer"},
			"owner_slot":   "coordinator",
			"exit_trigger": map[string]any{"kind": "manual", "spec": map[string]any{"signal": "done"}},
		}},
		"authority": map[string]any{"mode": "strict", "grants": grants},
		"routing":   map[string]any{"rules": nil, "coordinator_slot": "coordinator"},
		"policy": map[string]any{
			"spawn":   map[string]any{"max_depth": 2, "max_children": 6, "fan_out": 6, "max_rounds": 200, "max_stalls": 20, "budget": 2000, "timeout": int64(timeout)},
			"history": "none",
		},
	}
	return tetherclient.TeamForm{Team: team, Launch: launch}
}

// watch starts tracking a task's run if it is not already tracked.
func (u *UnitService) watch(taskID string) {
	u.mu.Lock()
	if u.watching[taskID] || u.baseCtx.Err() != nil {
		u.mu.Unlock()
		return
	}
	u.watching[taskID] = true
	u.wg.Add(1)
	u.mu.Unlock()
	go func() {
		defer u.wg.Done()
		defer func() {
			u.mu.Lock()
			delete(u.watching, taskID)
			u.mu.Unlock()
		}()
		u.track(taskID)
	}()
}

// Resume re-attaches a watcher to every doing task with an active unit run. Call it once at serve start.
func (u *UnitService) Resume() {
	if !u.Enabled() {
		return
	}
	tasks, err := u.task.List(sqlstore.TaskFilter{Status: "doing", Limit: 1000})
	if err != nil {
		log.Printf("[unit] resume: %v", err)
		return
	}
	n := 0
	for i := range tasks {
		if r, ok := unitRunFrom(taskMetadata(&tasks[i])); ok && r.State == UnitActive && r.RunID != "" {
			u.watch(tasks[i].ID)
			n++
		}
	}
	if n > 0 {
		log.Printf("[unit] resumed tracking of %d team run(s)", n)
	}
}

// track polls a run until it ends, times out, or the service stops.
func (u *UnitService) track(taskID string) {
	for {
		t, err := u.task.Get(taskID)
		if err != nil {
			log.Printf("[unit] %s: read task: %v", taskID, err)
			return
		}
		run, ok := unitRunFrom(taskMetadata(t))
		if !ok || run.State != UnitActive || run.RunID == "" || t.Status != "doing" {
			return // finished, moved by someone else, or never ours
		}
		deadline, _ := time.Parse(time.RFC3339, run.Deadline)
		info, perr := u.teams.TeamRun(u.baseCtx, run.RunID)
		switch {
		case perr == nil && info.Ended():
			u.finish(t, run)
			return
		case perr != nil && errors.Is(perr, tetherclient.ErrRunNotFound):
			// Never conclude death from a missing run alone: Tether's read for a
			// run id is only trusted when it answers; keep polling to the deadline.
			log.Printf("[unit] %s: run %s not found in roster", taskID, run.RunID)
		case perr != nil && u.baseCtx.Err() == nil:
			log.Printf("[unit] %s: poll run %s: %v", taskID, run.RunID, perr)
		}
		if !deadline.IsZero() && u.now().After(deadline.Add(u.cfg.Grace)) {
			u.timeout(t, run)
			return
		}
		select {
		case <-u.baseCtx.Done():
			return
		case <-time.After(u.cfg.PollInterval):
		}
	}
}

// finish handles a run that ended: with a RESULT comment since dispatch the
// task goes to its on_done status; without one it is blocked.
func (u *UnitService) finish(t *sqlstore.TaskRecord, run UnitRun) {
	lock := u.taskLock(t.ID)
	lock.Lock()
	defer lock.Unlock()
	result := u.findResult(t.ID, run)
	if result == "" {
		u.block(t, run, fmt.Sprintf("team run %s dissolved without a result (no %q comment from the orchestrator since dispatch)", run.RunID, resultPrefix))
		return
	}
	to := "review"
	if t.OnDone == "done" {
		to = "done"
	}
	run.State, run.Reason = UnitDone, ""
	empty := ""
	if err := u.saveRun(t.ID, run, sqlstore.TaskUpdate{BlockedReason: &empty}); err != nil {
		log.Printf("[unit] %s: record done: %v", t.ID, err)
	}
	if len(result) > 4000 {
		result = result[:4000] + "..."
	}
	msg := fmt.Sprintf("Team run %s dissolved with a result (attempt %d). Task moved to %s per on_done.\n\nResult: %s", run.RunID, run.Attempt, to, result)
	if err := u.task.TransitionWithComment(u.baseCtx, t.ID, to, msg, unitAuthor, false); err != nil {
		log.Printf("[unit] %s: move to %s: %v", t.ID, to, err)
		u.block(t, run, fmt.Sprintf("team run %s finished with a result but the task could not move to %s: %v", run.RunID, to, err))
	}
}

func (u *UnitService) timeout(t *sqlstore.TaskRecord, run UnitRun) {
	lock := u.taskLock(t.ID)
	lock.Lock()
	defer lock.Unlock()
	reason := fmt.Sprintf("team run %s timed out at %s", run.RunID, run.Deadline)
	if err := u.dissolve(run.RunID, run.Key); err != nil {
		reason += fmt.Sprintf(" and could not be dissolved (%v); dissolve it by hand", err)
	} else {
		reason += " and was dissolved"
	}
	u.block(t, run, reason)
}

func (u *UnitService) block(t *sqlstore.TaskRecord, run UnitRun, reason string) {
	run.State, run.Reason = UnitBlocked, reason
	if err := u.saveRun(t.ID, run, sqlstore.TaskUpdate{BlockedReason: &reason}); err != nil {
		log.Printf("[unit] %s: record block: %v", t.ID, err)
	}
	if err := u.task.TransitionWithComment(u.baseCtx, t.ID, "blocked", "Unit blocked: "+reason, unitAuthor, false); err != nil {
		log.Printf("[unit] %s: move to blocked: %v", t.ID, err)
	}
}

// findResult returns the newest result comment posted since the run started.
func (u *UnitService) findResult(taskID string, run UnitRun) string {
	since, _ := time.Parse(time.RFC3339, run.StartedAt)
	comments, err := u.comment.ListForTask(taskID)
	if err != nil {
		log.Printf("[unit] %s: list comments: %v", taskID, err)
		return ""
	}
	best := ""
	for _, c := range comments {
		if c.Author == unitAuthor || !strings.HasPrefix(strings.TrimSpace(c.Content), resultPrefix) {
			continue
		}
		if c.CreatedAt.Before(since.Add(-2 * time.Second)) {
			continue
		}
		best = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.Content), resultPrefix))
	}
	return best
}

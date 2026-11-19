package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
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
	UnitForming     = "forming"     // key chosen, form may or may not have happened
	UnitActive      = "active"      // team formed and briefed; being tracked
	UnitDone        = "done"        // dissolved with a result
	UnitUnconfirmed = "unconfirmed" // formation outcome unknown and not resolved; an operator must check Tether
	UnitClosing     = "closing"     // result or deadline seen; the run is being dissolved
	UnitBlocked     = "blocked"     // dissolved without a result, timed out, or tracking gave up
	UnitFailed      = "failed"      // formation or briefing failed; no run is live
)

// unitAuthor is the comment author for dispatcher-written comments.
const unitAuthor = "torque-dispatch"

// UnitTeams is the slice of Tether's team API the dispatcher uses.
type UnitTeams interface {
	FormTeam(ctx context.Context, key string, f tetherclient.TeamForm) (*tetherclient.TeamResult, error)
	DelegateTeam(ctx context.Context, key, runID, address, body string) (*tetherclient.TeamResult, error)
	DissolveTeam(ctx context.Context, key, runID string) error
	TeamRun(ctx context.Context, runID string) (*tetherclient.TeamRunInfo, error)
	FindTeamRun(ctx context.Context, teamID string) (string, error)
}

// UnitPins are the definition pins for each role. They must match
// teams-policy.yaml in the Tether catalog (the policy binds one launch, and so
// one model, to each pin).
type UnitPins struct {
	Orchestrator        tetherclient.DefinitionRef            `json:"orchestrator"`
	WorkerSol           tetherclient.DefinitionRef            `json:"worker_sol"`
	WorkerAgy           tetherclient.DefinitionRef            `json:"worker_agy"`
	Reviewer            tetherclient.DefinitionRef            `json:"reviewer"`
	WorkerSolPlacements map[string]tetherclient.DefinitionRef `json:"worker_sol_placements,omitempty"`
}

const liveDigest = "sha256:c8692289df79259473f3907beb7951e6a2b6fcbb0fd4cca18bd4797493ff043c"

// DefaultUnitPins names the approved role and per-host Sol definitions.
// Deployment must provision these exact pins in teams-policy.yaml first.
func DefaultUnitPins() UnitPins {
	pins := UnitPins{
		Orchestrator: tetherclient.DefinitionRef{ID: "team-orchestrator", Revision: "r1", Digest: liveDigest},
		WorkerSol:    tetherclient.DefinitionRef{ID: "team-worker-sol", Revision: "r1", Digest: liveDigest},
		WorkerAgy:    tetherclient.DefinitionRef{ID: "team-worker-agy", Revision: "r1", Digest: liveDigest},
		Reviewer:     tetherclient.DefinitionRef{ID: "team-reviewer-merger", Revision: "r1", Digest: liveDigest},
	}
	pins.WorkerSolPlacements = map[string]tetherclient.DefinitionRef{}
	for i := 1; i <= 6; i++ {
		pins.WorkerSolPlacements[fmt.Sprintf("worker-%d", i)] = tetherclient.DefinitionRef{ID: fmt.Sprintf("team-worker-sol-w%d", i), Revision: "r1", Digest: liveDigest}
	}
	pins.WorkerSolPlacements["build-1"] = tetherclient.DefinitionRef{ID: "team-worker-sol-build1", Revision: "r1", Digest: liveDigest}
	return pins
}

// UnitConfig tunes the dispatcher.
type UnitConfig struct {
	PollInterval   time.Duration // run-state poll; default 15s
	DefaultTimeout time.Duration // run lifetime when the plan sets none; default 4h
	Grace          time.Duration // wait past the deadline for Tether's own reaper before dissolving; default 2m
	// ResultGrace is how long a RESULT: comment may sit with the run still
	// live before Torque dissolves the run itself and finishes the task; default 2m.
	ResultGrace time.Duration
	// MaxActive caps in-flight units (forming plus running) across all
	// projects, so repeated dispatches cannot start unbounded teams; default 4.
	MaxActive int
	// RetryBackoff spaces the same-key retries after an ambiguous formation
	// failure; default 2s.
	RetryBackoff time.Duration
	// MaxReplays and FormingGiveUp bound the same-key replays of an ambiguous
	// formation: after this many failed rounds, or this long, Torque stops
	// replaying and looks for the run another way. Defaults 5 and 10m.
	MaxReplays    int
	FormingGiveUp time.Duration
	// WorkerPool lists pre-registered worker identities (msg://agent/...). Needed
	// only for plans with more than one worker.
	WorkerPool []string
	Pins       UnitPins
}

// UnitConfigFromEnv reads TORQUE_UNIT_POLL, TORQUE_UNIT_TIMEOUT,
// TORQUE_UNIT_MAX_ACTIVE, TORQUE_UNIT_WORKER_POOL (comma-separated URNs) and
// TORQUE_UNIT_PINS (JSON overriding DefaultUnitPins).
func UnitConfigFromEnv() UnitConfig {
	cfg := UnitConfig{Pins: DefaultUnitPins()}
	if d, err := time.ParseDuration(os.Getenv("TORQUE_UNIT_POLL")); err == nil && d > 0 {
		cfg.PollInterval = d
	}
	if d, err := time.ParseDuration(os.Getenv("TORQUE_UNIT_TIMEOUT")); err == nil && d > 0 {
		cfg.DefaultTimeout = d
	}
	if n, err := strconv.Atoi(os.Getenv("TORQUE_UNIT_MAX_ACTIVE")); err == nil && n > 0 {
		cfg.MaxActive = n
	}
	for _, p := range strings.Split(os.Getenv("TORQUE_UNIT_WORKER_POOL"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.WorkerPool = append(cfg.WorkerPool, p)
		}
	}
	if raw := os.Getenv("TORQUE_UNIT_PINS"); raw != "" {
		// An explicit custom pin set does not inherit fleet placements.
		cfg.Pins.WorkerSolPlacements = nil
		if err := json.Unmarshal([]byte(raw), &cfg.Pins); err != nil {
			log.Printf("[unit] ignoring TORQUE_UNIT_PINS: %v", err)
			cfg.Pins = DefaultUnitPins()
		}
	}
	return cfg
}

// UnitRun is the dispatcher's record on the task (metadata.unit_run). Only the
// dispatcher writes it; a caller's metadata update keeps the stored value.
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
	// Ambiguous: a formation call failed in a way that does not prove nothing
	// formed. The next dispatch replays the same key to learn the run id.
	Ambiguous bool `json:"ambiguous,omitempty"`
	// Replays counts rounds of failed same-key formation; AmbiguousSince is when
	// the first one failed.
	Replays        int    `json:"replays,omitempty"`
	AmbiguousSince string `json:"ambiguous_since,omitempty"`
	// Unconfirmed: the run could not be confirmed ended when the unit was
	// closed. Dispatch refuses until it is dissolved or seen ended.
	Unconfirmed bool `json:"unconfirmed,omitempty"`
	// Placement and Formation are selected once before the first network call.
	// Formation seals pins, pool identities and limits across ambiguous replays.
	Placement string                 `json:"placement,omitempty"`
	Formation *tetherclient.TeamForm `json:"formation,omitempty"`
}

// UnitDispatchInput names the task and optional plan overrides.
type UnitDispatchInput struct {
	TaskID        string
	Workers       int    // 0 = plan value
	WorkerRuntime string // "" = plan value
	// ClearUnconfirmed: the operator has checked Tether and the stuck formation
	// (state unconfirmed) left no team; dispatch may proceed with a new attempt.
	ClearUnconfirmed bool
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

// ErrUnitCapacity is returned when the global in-flight unit limit is reached.
// It is transient: nothing is parked, the caller may try again later.
var ErrUnitCapacity = errors.New("unit capacity reached")

// UnitService dispatches a unit of work (a task with an execution plan) as a
// Tether team and tracks the run to its end. No agent does the launching.
//
// Locking: u.mu guards short decisions (read task, decide, write state) and is
// never held across a network call. A task being formed is guarded by an
// in-flight marker instead, so a second dispatch of it waits for the first.
type UnitService struct {
	store   *sqlstore.Store
	task    *TaskService
	comment *CommentService

	teams  UnitTeams
	cfg    UnitConfig
	notify func(taskID, reason string)

	mu       sync.Mutex
	inflight map[string]chan struct{} // task id -> closed when its dispatch ends
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
		cfg:      UnitConfig{Pins: DefaultUnitPins()},
		inflight: map[string]chan struct{}{}, watching: map[string]bool{},
		baseCtx: ctx, cancel: cancel, now: time.Now,
	}
}

// Configure installs the Tether team API and settings. A nil teams leaves
// dispatch disabled with a clear error. Zero config values take defaults.
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
	if cfg.ResultGrace <= 0 {
		cfg.ResultGrace = 2 * time.Minute
	}
	if cfg.MaxActive <= 0 {
		cfg.MaxActive = 4
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 2 * time.Second
	}
	if cfg.MaxReplays <= 0 {
		cfg.MaxReplays = 5
	}
	if cfg.FormingGiveUp <= 0 {
		cfg.FormingGiveUp = 10 * time.Minute
	}
	if cfg.Pins.Orchestrator == (tetherclient.DefinitionRef{}) && cfg.Pins.WorkerSol == (tetherclient.DefinitionRef{}) && cfg.Pins.WorkerAgy == (tetherclient.DefinitionRef{}) && cfg.Pins.Reviewer == (tetherclient.DefinitionRef{}) && cfg.Pins.WorkerSolPlacements == nil {
		cfg.Pins = DefaultUnitPins()
	}
	u.teams, u.cfg = teams, cfg
}

// SetNotifier installs the hook called when a notify-on-done unit completes
// (the same "task.notify" event the executor path publishes).
func (u *UnitService) SetNotifier(fn func(taskID, reason string)) { u.notify = fn }

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

// saveRun writes the unit_run record. The merge happens inside the UPDATE
// (sqlstore TaskUpdate.UnitRun), so it neither loses nor clobbers a concurrent
// metadata edit.
func (u *UnitService) saveRun(taskID string, r UnitRun, upd sqlstore.TaskUpdate) error {
	b, _ := json.Marshal(r)
	js := string(b)
	upd.UnitRun = &js
	return u.task.Update(taskID, TaskUpdateInput{TaskUpdate: upd})
}

// HasUnitPlan reports whether a task carries an execution plan.
func HasUnitPlan(t sqlstore.TaskRecord) bool { return unitplan.Present(taskMetadata(&t)) }

// DispatchTask is the scheduler hook: it dispatches a picked task that has an
// execution plan and reports whether the task was handled. A refusal (invalid
// plan, missing pool) parks the task - manual, with a blocked_reason - so the
// next tick does not pick it again. A capacity refusal is transient and leaves
// the task as it is.
func (u *UnitService) DispatchTask(ctx context.Context, t sqlstore.TaskRecord) (bool, error) {
	if !HasUnitPlan(t) {
		return false, nil
	}
	_, err := u.Dispatch(ctx, UnitDispatchInput{TaskID: t.ID})
	var verr *ValidationError
	if err != nil && errors.As(err, &verr) {
		u.park(t.ID, err.Error())
	}
	return true, err
}

// park takes a task out of the scheduler's reach with the reason recorded.
func (u *UnitService) park(taskID, reason string) {
	manual := true
	reason = "unit dispatch refused: " + reason
	if err := u.task.Update(taskID, TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{BlockedReason: &reason, Manual: &manual}}); err != nil {
		log.Printf("[unit] %s: park: %v", taskID, err)
		return
	}
	if _, err := u.comment.AddForTask(taskID, unitAuthor, reason+". Task left todo and set manual; fix the plan and dispatch again."); err != nil {
		log.Printf("[unit] %s: park comment: %v", taskID, err)
	}
}

// netCtx is a context for one Tether call. It hangs off the service, not the
// caller, so a short-lived request or scheduler tick cannot cut a formation
// short and leave its outcome unknown.
func (u *UnitService) netCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(u.baseCtx, d)
}

type unitAttempt struct {
	task      *sqlstore.TaskRecord
	plan      unitplan.Plan
	run       UnitRun
	confirm   *UnitRun // a previous unconfirmed run to settle first
	settle    *UnitRun // a stuck formation to resolve before a new attempt
	clear     bool
	reused    *UnitDispatchResult
	waitFor   chan struct{}
	ambiguous bool
}

// begin decides what a dispatch does, under u.mu, and claims the task.
func (u *UnitService) begin(in UnitDispatchInput) (*unitAttempt, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if ch, busy := u.inflight[in.TaskID]; busy {
		return &unitAttempt{waitFor: ch}, nil
	}
	t, err := u.task.Get(in.TaskID)
	if err != nil {
		return nil, err
	}
	meta := taskMetadata(t)
	prev, hadRun := unitRunFrom(meta)
	if hadRun && prev.State == UnitActive && prev.RunID != "" {
		u.watchLocked(t.ID, prev.RunID)
		return &unitAttempt{reused: &UnitDispatchResult{TaskID: t.ID, RunID: prev.RunID, Attempt: prev.Attempt, State: prev.State, Reused: true}}, nil
	}
	if hadRun && prev.State == UnitClosing {
		return nil, &ValidationError{Field: "state", Message: "the unit's team run is being closed; try again shortly"}
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

	n, cerr := u.store.CountUnitsInFlight()
	if cerr != nil {
		return nil, fmt.Errorf("unit dispatch: count units in flight: %w", cerr)
	}
	if hadRun && (prev.State == UnitForming || prev.State == UnitUnconfirmed || prev.Unconfirmed) {
		n-- // this task's own record is already counted
	}
	if n >= u.cfg.MaxActive {
		return nil, fmt.Errorf("%w: %d units in flight, limit %d (TORQUE_UNIT_MAX_ACTIVE)", ErrUnitCapacity, n, u.cfg.MaxActive)
	}

	at := &unitAttempt{task: t, plan: plan}
	if hadRun && prev.RunID != "" && prev.Unconfirmed && prev.State != UnitUnconfirmed {
		p := prev
		at.confirm = &p
	}
	// A crashed or ambiguous formation reuses its key, so Tether's idempotency
	// returns the run it already made instead of a second team. The replays are
	// bounded: past the limit Torque stops replaying and looks for the run.
	attempt := prev.Attempt + 1
	carry := UnitRun{}
	switch {
	case hadRun && prev.State == UnitUnconfirmed:
		p := prev
		at.settle, at.clear = &p, in.ClearUnconfirmed
	case hadRun && prev.State == UnitForming:
		if prev.Replays >= u.cfg.MaxReplays || u.agedOut(prev) {
			p := prev
			at.settle, at.clear = &p, in.ClearUnconfirmed
		} else {
			attempt, at.ambiguous = prev.Attempt, true
			carry.Replays, carry.AmbiguousSince = prev.Replays, prev.AmbiguousSince
		}
	}
	if attempt < 1 {
		attempt = 1
	}
	timeout := plan.TimeoutDuration(u.cfg.DefaultTimeout)
	started := u.now().UTC()
	at.run = UnitRun{
		Attempt: attempt, State: UnitForming, Key: fmt.Sprintf("torque-unit-%s-a%d", t.ID, attempt),
		Workers: int(plan.Workers), WorkerRuntime: plan.WorkerRuntime, Ambiguous: at.ambiguous,
		Replays: carry.Replays, AmbiguousSince: carry.AmbiguousSince,
		StartedAt: started.Format(time.RFC3339), Deadline: started.Add(timeout).Format(time.RFC3339),
	}
	if at.ambiguous && prev.Formation == nil && int(plan.Workers) > 1 && len(u.cfg.WorkerPool) < int(plan.Workers) {
		return nil, &ValidationError{Field: "workers", Message: "legacy forming replay requires its original worker pool identities"}
	}
	if at.ambiguous {
		// Legacy forming records have no snapshot: retain their original catalog
		// selection rather than introducing a new host under an existing key.
		at.run.Placement, at.run.Formation = prev.Placement, prev.Formation
		if prev.Formation != nil {
			at.run.Workers, at.run.WorkerRuntime = prev.Workers, prev.WorkerRuntime
			at.run.StartedAt, at.run.Deadline = prev.StartedAt, prev.Deadline
			at.plan.Workers, at.plan.WorkerRuntime = unitplan.Int(prev.Workers), prev.WorkerRuntime
		}
	} else {
		if int(plan.Workers) > 1 && len(u.cfg.WorkerPool) < int(plan.Workers) {
			return nil, &ValidationError{Field: "workers", Message: fmt.Sprintf("%d workers need %d pre-registered pool identities; TORQUE_UNIT_WORKER_POOL lists %d (use workers=1 or register identities)", plan.Workers, plan.Workers, len(u.cfg.WorkerPool))}
		}
		placement, pin, err := u.selectPlacement(t.ID, plan)
		if err != nil {
			return nil, err
		}
		at.run.Placement = placement
		form := u.buildForm(t.ID, attempt, plan, timeout)
		if pin != nil {
			slots := form.Team["slots"].([]tetherclient.TeamSlot)
			for i := range slots {
				if slots[i].Name == "workers" {
					slots[i].DefinitionRef = *pin
				}
			}
		}
		at.run.Formation = &form
	}
	if at.confirm == nil && at.settle == nil {
		if err := u.saveRun(t.ID, at.run, sqlstore.TaskUpdate{}); err != nil {
			return nil, fmt.Errorf("unit dispatch: record attempt: %w", err)
		}
	}
	u.inflight[t.ID] = make(chan struct{})
	return at, nil
}

// agedOut reports whether an ambiguous formation has been unresolved for
// longer than FormingGiveUp.
func (u *UnitService) agedOut(r UnitRun) bool {
	since, err := time.Parse(time.RFC3339, r.AmbiguousSince)
	return err == nil && u.now().Sub(since) > u.cfg.FormingGiveUp
}

func (u *UnitService) endInflight(taskID string) {
	u.mu.Lock()
	if ch, ok := u.inflight[taskID]; ok {
		close(ch)
		delete(u.inflight, taskID)
	}
	u.mu.Unlock()
}

// Dispatch validates the task's plan, forms a team for it, briefs the
// orchestrator, records the run on the task and starts tracking it. It is
// idempotent: a task with a live run returns that run, and concurrent
// dispatches of one task form exactly one team.
func (u *UnitService) Dispatch(ctx context.Context, in UnitDispatchInput) (*UnitDispatchResult, error) {
	if !u.Enabled() {
		return nil, &ValidationError{Field: "tether", Message: "unit dispatch is not configured: set TORQUE_TETHER_URL and TORQUE_TETHER_OPERATOR_TOKEN on the serve process"}
	}
	if in.TaskID == "" {
		return nil, &ValidationError{Field: "task_id", Message: "task_id required"}
	}
	var at *unitAttempt
	for {
		var err error
		if at, err = u.begin(in); err != nil {
			return nil, err
		}
		if at.reused != nil {
			return at.reused, nil
		}
		if at.waitFor == nil {
			break
		}
		select {
		case <-at.waitFor:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	defer u.endInflight(in.TaskID)
	return u.run(at)
}

// run does the network half of a dispatch without holding any lock.
func (u *UnitService) run(at *unitAttempt) (*UnitDispatchResult, error) {
	t, plan, run := at.task, at.plan, at.run
	if at.confirm != nil {
		if err := u.confirmEnded(*at.confirm); err != nil {
			return nil, &ValidationError{Field: "run", Message: fmt.Sprintf("previous team run %s is not confirmed ended: %v; dissolve it first", at.confirm.RunID, err)}
		}
		if err := u.saveRun(t.ID, run, sqlstore.TaskUpdate{}); err != nil {
			return nil, fmt.Errorf("unit dispatch: record attempt: %w", err)
		}
	}

	if at.settle != nil {
		if err := u.settleStuck(t.ID, *at.settle, at.clear); err != nil {
			return nil, err
		}
		if err := u.saveRun(t.ID, run, sqlstore.TaskUpdate{}); err != nil {
			return nil, fmt.Errorf("unit dispatch: record attempt: %w", err)
		}
	}

	formation := run.Formation
	if formation == nil {
		legacy := u.buildForm(t.ID, run.Attempt, plan, plan.TimeoutDuration(u.cfg.DefaultTimeout))
		formation = &legacy
	}
	res, err := u.form(run, *formation)
	if err != nil {
		var te *tetherclient.TeamError
		if errors.As(err, &te) && cleanRefusal(te) && !run.Ambiguous {
			return nil, u.failDispatch(t.ID, run, "team formation refused", err, "")
		}
		// Ambiguous: the call may have formed a team. Keep the key, keep state
		// "forming", and take the task out of the scheduler's reach; the next
		// dispatch replays the same key to learn the run id.
		run.Ambiguous = true
		run.Replays++
		if run.AmbiguousSince == "" {
			run.AmbiguousSince = u.now().UTC().Format(time.RFC3339)
		}
		reason := fmt.Sprintf("unit dispatch attempt %d: team formation outcome unknown (%v); the next dispatch retries the same idempotency key", run.Attempt, err)
		run.Reason = reason
		manual := true
		if serr := u.saveRun(t.ID, run, sqlstore.TaskUpdate{BlockedReason: &reason, Manual: &manual}); serr != nil {
			log.Printf("[unit] %s: record ambiguous formation: %v", t.ID, serr)
		}
		if _, cerr := u.comment.AddForTask(t.ID, unitAuthor, reason); cerr != nil {
			log.Printf("[unit] %s: comment: %v", t.ID, cerr)
		}
		if run.Replays >= u.cfg.MaxReplays {
			// Enough: stop replaying and look for the run another way.
			if serr := u.settleStuck(t.ID, run, false); serr != nil {
				return nil, serr
			}
			return nil, u.failDispatch(t.ID, run, "formation never confirmed; the team that was found was dissolved", err, "")
		}
		return nil, errors.New(reason)
	}
	run.RunID = res.RunID()
	if run.RunID == "" {
		return nil, u.failDispatch(t.ID, run, "team formation failed", errors.New("tether returned no run id"), "")
	}
	run.State, run.Ambiguous, run.Reason = UnitActive, false, ""
	// Record the run id before anything else can fail, so no live run is ever
	// unknown to Torque.
	if err := u.saveRun(t.ID, run, sqlstore.TaskUpdate{}); err != nil {
		return nil, u.failDispatch(t.ID, run, "could not record the team run", err, run.RunID)
	}

	ctx, cancel := u.netCtx(2 * time.Minute)
	_, err = u.teams.DelegateTeam(ctx, run.Key+"-delegate", run.RunID, "@orchestrator", plan.Brief(t.ID, t.Title, run.RunID))
	cancel()
	if err != nil {
		return nil, u.failDispatch(t.ID, run, "briefing the orchestrator failed", err, run.RunID)
	}

	empty := ""
	if err := u.saveRun(t.ID, run, sqlstore.TaskUpdate{BlockedReason: &empty}); err != nil {
		return nil, u.failDispatch(t.ID, run, "could not update the task", err, run.RunID)
	}
	if err := u.task.TransitionWithComment(u.baseCtx, t.ID, "doing",
		fmt.Sprintf("Team run %s formed (attempt %d): 1 orchestrator, %d worker(s) on %s, 1 reviewer. Deadline %s. Tracked by Torque; the team dissolves when the orchestrator reports.", run.RunID, run.Attempt, plan.Workers, plan.WorkerRuntime, run.Deadline),
		unitAuthor, false); err != nil {
		return nil, u.failDispatch(t.ID, run, "could not move the task to doing", err, run.RunID)
	}
	u.mu.Lock()
	u.watchLocked(t.ID, run.RunID)
	u.mu.Unlock()
	return &UnitDispatchResult{TaskID: t.ID, RunID: run.RunID, Attempt: run.Attempt, State: UnitActive}, nil
}

// cleanRefusal reports whether a Tether answer proves nothing was formed: a
// definite 4xx. Timeouts, throttling and server errors do not.
func cleanRefusal(te *tetherclient.TeamError) bool {
	return te.Status >= 400 && te.Status < 500 && te.Status != http.StatusRequestTimeout && te.Status != http.StatusTooManyRequests
}

// form calls /teams/form on a detached context. A failure that does not prove
// nothing formed is retried with the SAME idempotency key, which returns the
// run if the first call formed one and lost its answer.
func (u *UnitService) form(run UnitRun, f tetherclient.TeamForm) (*tetherclient.TeamResult, error) {
	var err error
	for i := 0; i < 3; i++ {
		if i > 0 {
			select {
			case <-u.baseCtx.Done():
				return nil, err
			case <-time.After(u.cfg.RetryBackoff):
			}
		}
		ctx, cancel := u.netCtx(100 * time.Second)
		var res *tetherclient.TeamResult
		res, err = u.teams.FormTeam(ctx, run.Key, f)
		cancel()
		if err == nil {
			return res, nil
		}
		var te *tetherclient.TeamError
		if errors.As(err, &te) && cleanRefusal(te) && !run.Ambiguous && i == 0 {
			return nil, err
		}
	}
	return nil, err
}

// failDispatch leaves no session running and no loop behind: it dissolves a
// formed run, records the failure, leaves the task todo with a blocked_reason
// and takes it out of the scheduler's reach (manual) until someone re-dispatches.
func (u *UnitService) failDispatch(taskID string, run UnitRun, what string, cause error, runID string) error {
	reason := fmt.Sprintf("unit dispatch attempt %d: %s: %v", run.Attempt, what, cause)
	if runID != "" {
		if derr := u.dissolve(runID, run.Key); derr != nil {
			reason += fmt.Sprintf(" (and dissolving run %s failed: %v; dissolve it by hand)", runID, derr)
			run.Unconfirmed = true
		} else {
			reason += fmt.Sprintf(" (run %s dissolved)", runID)
		}
	}
	run.State, run.Reason, run.Ambiguous = UnitFailed, reason, false
	manual := true
	if err := u.saveRun(taskID, run, sqlstore.TaskUpdate{BlockedReason: &reason, Manual: &manual}); err != nil {
		log.Printf("[unit] %s: record failure: %v", taskID, err)
	}
	if _, err := u.comment.AddForTask(taskID, unitAuthor, reason+". Task left todo and set manual; fix the cause and dispatch again."); err != nil {
		log.Printf("[unit] %s: failure comment: %v", taskID, err)
	}
	return errors.New(reason)
}

// dissolve ends a run, retrying once. Each call uses a fresh key so a replayed
// failure cannot mask a retry; a 404 means the run is already gone.
func (u *UnitService) dissolve(runID, key string) error {
	var err error
	for i := 0; i < 2; i++ {
		ctx, cancel := u.netCtx(60 * time.Second)
		err = u.teams.DissolveTeam(ctx, fmt.Sprintf("%s-dissolve-%d", key, u.now().UnixNano()), runID)
		cancel()
		if err == nil {
			return nil
		}
		var te *tetherclient.TeamError
		if errors.As(err, &te) && te.Status == http.StatusNotFound {
			return nil
		}
	}
	return err
}

// teamIDFor is the team definition id formed for a task attempt (see buildForm).
func teamIDFor(taskID string, attempt int) string {
	return "unit-" + strings.ToLower(taskID) + fmt.Sprintf("-a%d", attempt)
}

// settleStuck resolves a formation whose outcome is unknown and can no longer
// be replayed. It looks the team up by its definition id in Tether's rosters
// and dissolves it if found. If it cannot establish that nothing is running it
// parks the task as "unconfirmed" (blocked_reason names the idempotency key and
// team id, manual, counted against the unit limit) and returns an error. clear
// is the operator's assertion that Tether holds nothing for this attempt.
func (u *UnitService) settleStuck(taskID string, prev UnitRun, clear bool) error {
	if clear {
		return nil
	}
	teamID := teamIDFor(taskID, prev.Attempt)
	ctx, cancel := u.netCtx(60 * time.Second)
	runID, ferr := u.teams.FindTeamRun(ctx, teamID)
	cancel()
	if ferr == nil {
		prev.RunID = runID
		if derr := u.dissolve(runID, prev.Key); derr == nil {
			return nil
		} else {
			return u.markUnconfirmed(taskID, prev, fmt.Sprintf("found run %s for team %s but could not dissolve it (%v)", runID, teamID, derr))
		}
	}
	why := fmt.Sprintf("could not look for team %s in Tether (%v)", teamID, ferr)
	if errors.Is(ferr, tetherclient.ErrRunNotFound) {
		why = fmt.Sprintf("team %s is not in Tether's rosters, but its formation was never acknowledged", teamID)
	}
	return u.markUnconfirmed(taskID, prev, why)
}

func (u *UnitService) markUnconfirmed(taskID string, prev UnitRun, why string) error {
	first := prev.State != UnitUnconfirmed
	prev.State, prev.Unconfirmed, prev.Ambiguous = UnitUnconfirmed, true, false
	reason := fmt.Sprintf("unit dispatch attempt %d unconfirmed: %s. Idempotency key %q (team %s). Check Tether, dissolve the team if it exists, then dispatch with clear_unconfirmed to start a new attempt", prev.Attempt, why, prev.Key, teamIDFor(taskID, prev.Attempt))
	prev.Reason = reason
	manual := true
	if err := u.saveRun(taskID, prev, sqlstore.TaskUpdate{BlockedReason: &reason, Manual: &manual}); err != nil {
		log.Printf("[unit] %s: record unconfirmed: %v", taskID, err)
	}
	if first {
		if _, err := u.comment.AddForTask(taskID, unitAuthor, reason); err != nil {
			log.Printf("[unit] %s: comment: %v", taskID, err)
		}
	}
	return &ValidationError{Field: "run", Message: reason}
}

// confirmEnded settles a run that could not be confirmed ended earlier: it
// must be dissolved now, or be seen ended.
func (u *UnitService) confirmEnded(prev UnitRun) error {
	derr := u.dissolve(prev.RunID, prev.Key)
	if derr == nil {
		return nil
	}
	ctx, cancel := u.netCtx(30 * time.Second)
	defer cancel()
	if info, err := u.teams.TeamRun(ctx, prev.RunID); err == nil && info.Ended() {
		return nil
	}
	return derr
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

// watchLocked starts tracking a task's run if it is not already tracked.
// Caller holds u.mu.
func (u *UnitService) watchLocked(taskID, runID string) {
	if u.watching[taskID] || u.baseCtx.Err() != nil {
		return
	}
	u.watching[taskID] = true
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		defer func() {
			u.mu.Lock()
			delete(u.watching, taskID)
			u.mu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[unit] %s: tracker panic: %v", taskID, r)
				u.abandon(taskID, runID, fmt.Sprintf("the run tracker panicked: %v", r), true)
			}
		}()
		u.track(taskID, runID)
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
	u.mu.Lock()
	for i := range tasks {
		if r, ok := unitRunFrom(taskMetadata(&tasks[i])); ok && (r.State == UnitActive || r.State == UnitClosing) && r.RunID != "" {
			u.watchLocked(tasks[i].ID, r.RunID)
			n++
		}
	}
	u.mu.Unlock()
	if n > 0 {
		log.Printf("[unit] resumed tracking of %d team run(s)", n)
	}
}

// track polls a run until the unit is closed, or the service stops.
func (u *UnitService) track(taskID, runID string) {
	for {
		t, err := u.task.Get(taskID)
		if err != nil {
			log.Printf("[unit] %s: read task: %v", taskID, err)
			return
		}
		run, ok := unitRunFrom(taskMetadata(t))
		switch {
		case !ok:
			// The record is the only link to the live run; without it the run
			// would be untracked. End it rather than leave it running.
			u.abandon(taskID, runID, "the unit_run record disappeared from the task", true)
			return
		case run.RunID != runID || (run.State != UnitActive && run.State != UnitClosing):
			return // closed by this watcher or replaced by a newer attempt
		case t.Status != "doing":
			u.abandon(taskID, runID, fmt.Sprintf("the task left doing (now %s) while its team run was live", t.Status), false)
			return
		}

		info, perr := u.teams.TeamRun(u.baseCtx, runID)
		ended := perr == nil && info.Ended()
		switch {
		case perr != nil && errors.Is(perr, tetherclient.ErrRunNotFound):
			// Never conclude death from a missing run alone; poll to the deadline.
			log.Printf("[unit] %s: run %s not found in roster", taskID, runID)
		case perr != nil && u.baseCtx.Err() == nil:
			log.Printf("[unit] %s: poll run %s: %v", taskID, runID, perr)
		}
		deadline, _ := time.Parse(time.RFC3339, run.Deadline)
		timedOut := !deadline.IsZero() && u.now().After(deadline.Add(u.cfg.Grace))
		_, resultAt := u.findResult(taskID, run)
		resultSettled := !resultAt.IsZero() && u.now().Sub(resultAt) >= u.cfg.ResultGrace
		if ended || timedOut || resultSettled {
			u.closeRun(taskID, runID, ended, timedOut)
			return
		}
		select {
		case <-u.baseCtx.Done():
			return
		case <-time.After(u.cfg.PollInterval):
		}
	}
}

// closeRun ends a unit. Under u.mu it re-checks that the task is still doing
// with this run active and claims it ("closing"); the dissolve, which is
// network, runs unlocked; then it records the outcome.
//
//	RESULT comment present -> review/done per on_done (even on timeout)
//	no result              -> blocked, with the reason
//	dissolve failed        -> blocked, run flagged unconfirmed
func (u *UnitService) closeRun(taskID, runID string, ended, timedOut bool) {
	u.mu.Lock()
	t, err := u.task.Get(taskID)
	if err != nil {
		u.mu.Unlock()
		return
	}
	run, ok := unitRunFrom(taskMetadata(t))
	if !ok || run.RunID != runID || t.Status != "doing" || (run.State != UnitActive && run.State != UnitClosing) {
		u.mu.Unlock()
		return
	}
	result, _ := u.findResult(taskID, run)
	run.State = UnitClosing
	if err := u.saveRun(taskID, run, sqlstore.TaskUpdate{}); err != nil {
		u.mu.Unlock()
		log.Printf("[unit] %s: claim closing: %v", taskID, err)
		return
	}
	u.mu.Unlock()

	var derr error
	if !ended {
		derr = u.dissolve(runID, run.Key)
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	t, err = u.task.Get(taskID)
	if err != nil {
		return
	}
	if cur, ok := unitRunFrom(taskMetadata(t)); !ok || cur.RunID != runID || cur.State != UnitClosing || t.Status != "doing" {
		return
	}
	run.Unconfirmed = derr != nil
	switch {
	case derr != nil:
		u.blockLocked(t, run, fmt.Sprintf("team run %s could not be dissolved (%v); dissolve it by hand", runID, derr))
	case result != "":
		u.succeedLocked(t, run, result)
	case timedOut:
		u.blockLocked(t, run, fmt.Sprintf("team run %s timed out at %s and was dissolved", runID, run.Deadline))
	default:
		u.blockLocked(t, run, fmt.Sprintf("team run %s dissolved without a result (no \"RESULT %s:\" comment since dispatch)", runID, runID))
	}
}

// succeedLocked moves a finished unit to its on_done status. on_done follows
// the executor path: close -> done, review -> review, notify -> done plus a
// task.notify event. on_review is not consulted: it governs how a task already
// in review is handled, and the unit is parked there for the owner.
func (u *UnitService) succeedLocked(t *sqlstore.TaskRecord, run UnitRun, result string) {
	to := "review"
	switch t.OnDone {
	case "close", "notify":
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
	msg := fmt.Sprintf("Team run %s dissolved with a result (attempt %d). Task moved to %s per on_done=%s.\n\nResult: %s", run.RunID, run.Attempt, to, t.OnDone, result)
	if err := u.task.TransitionWithComment(u.baseCtx, t.ID, to, msg, unitAuthor, false); err != nil {
		log.Printf("[unit] %s: move to %s: %v", t.ID, to, err)
		u.blockLocked(t, run, fmt.Sprintf("team run %s finished with a result but the task could not move to %s: %v", run.RunID, to, err))
		return
	}
	if t.OnDone == "notify" && u.notify != nil {
		u.notify(t.ID, "task completed")
	}
}

func (u *UnitService) blockLocked(t *sqlstore.TaskRecord, run UnitRun, reason string) {
	run.State, run.Reason = UnitBlocked, reason
	if err := u.saveRun(t.ID, run, sqlstore.TaskUpdate{BlockedReason: &reason}); err != nil {
		log.Printf("[unit] %s: record block: %v", t.ID, err)
	}
	if err := u.task.TransitionWithComment(u.baseCtx, t.ID, "blocked", "Unit blocked: "+reason, unitAuthor, false); err != nil {
		log.Printf("[unit] %s: move to blocked: %v", t.ID, err)
	}
}

// abandon ends a run whose tracking can no longer be trusted (record lost,
// task moved away, tracker panic): dissolve it and record why. When toBlocked
// is set the task is also moved to blocked.
func (u *UnitService) abandon(taskID, runID, why string, toBlocked bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[unit] %s: abandon panic: %v", taskID, r)
		}
	}()
	derr := u.dissolve(runID, fmt.Sprintf("torque-unit-%s-abandon", taskID))
	reason := fmt.Sprintf("team run %s ended by Torque: %s", runID, why)
	if derr != nil {
		reason = fmt.Sprintf("team run %s could not be dissolved (%v) and %s; dissolve it by hand", runID, derr, why)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	t, err := u.task.Get(taskID)
	if err != nil {
		log.Printf("[unit] %s: abandon: %v", taskID, err)
		return
	}
	run, _ := unitRunFrom(taskMetadata(t))
	run.RunID, run.State, run.Reason, run.Unconfirmed = runID, UnitBlocked, reason, derr != nil
	if toBlocked && t.Status == "doing" {
		u.blockLocked(t, run, reason)
		return
	}
	if err := u.saveRun(taskID, run, sqlstore.TaskUpdate{BlockedReason: &reason}); err != nil {
		log.Printf("[unit] %s: abandon record: %v", taskID, err)
	}
	if _, err := u.comment.AddForTask(taskID, unitAuthor, "Unit run ended: "+reason); err != nil {
		log.Printf("[unit] %s: abandon comment: %v", taskID, err)
	}
}

// findResult returns the newest result comment posted since the run started
// and when it was posted.
func (u *UnitService) findResult(taskID string, run UnitRun) (string, time.Time) {
	since, _ := time.Parse(time.RFC3339, run.StartedAt)
	comments, err := u.comment.ListForTask(taskID)
	if err != nil {
		log.Printf("[unit] %s: list comments: %v", taskID, err)
		return "", time.Time{}
	}
	best, at := "", time.Time{}
	for _, c := range comments {
		// Comment authorship is not verified in Torque, so a result counts only if
		// it carries this run's id: "RESULT <run id>: ...". The brief tells the
		// orchestrator to write it that way.
		marker := fmt.Sprintf("%s %s:", "RESULT", run.RunID)
		if c.Author == unitAuthor || run.RunID == "" || !strings.HasPrefix(strings.TrimSpace(c.Content), marker) {
			continue
		}
		if c.CreatedAt.Before(since.Add(-2 * time.Second)) {
			continue
		}
		best, at = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.Content), marker)), c.CreatedAt
	}
	return best, at
}

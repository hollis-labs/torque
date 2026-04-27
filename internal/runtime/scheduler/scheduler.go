package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/modelcatalog"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/waitpoll"
	"github.com/hollis-labs/clockwork-manifold/internal/worktree"
)

// envRepoRoot returns CLOCKWORK_REPO so the startup sweep knows which
// repo's worktree admin to prune against. Kept as a tiny helper so tests
// can override behavior by setting the env var.
func envRepoRoot() string { return os.Getenv("CLOCKWORK_REPO") }

// SchedulerStatus reports the current state of the scheduler.
type SchedulerStatus struct {
	Enabled       bool    `json:"enabled"`
	MaxWorkers    int     `json:"max_workers"`
	ActiveWorkers int     `json:"active_workers"`
	QueueDepth    int     `json:"queue_depth"`
	TotalCost     float64 `json:"total_cost"`
	Subscribers   int     `json:"subscribers"`
	// StaleHeartbeatThresholdSeconds is the number of seconds since a
	// worker's last heartbeat after which it is considered stale and its
	// row is pruned by the next scheduler tick. Surfaced here so operators
	// can verify the effective threshold without re-reading the env
	// (CW-20260418-0018). Controlled by CLOCKWORK_SCHED_STALE, default 300.
	StaleHeartbeatThresholdSeconds int `json:"stale_heartbeat_threshold_seconds"`
}

// Scheduler is the core orchestration loop. It picks eligible tasks,
// dispatches them to executors via a worker pool, and handles results
// through the lifecycle manager.
type Scheduler struct {
	store      *sqlstore.Store
	queue      *queue.Queue
	registry   *executor.Registry
	predicates *waitpoll.Registry
	cfg        *config.SchedulerConfig

	pool              *WorkerPool
	picker            *Picker
	lifecycle         *LifecycleManager
	cost              *CostTracker
	heartbeat         *HeartbeatMonitor
	bus               *EventBus
	progressThrottler *progressThrottler
	progressHeartbeat *progressHeartbeat
	cancels           *cancelRegistry

	// cancelGrace is how long the worker gives a child process to exit
	// after SIGTERM before escalating to SIGKILL. Sourced from
	// CLOCKWORK_SCHED_CANCEL_GRACE at New() time; defaults to 5s.
	// (CW-20260418-0005)
	cancelGrace time.Duration

	mu      sync.RWMutex
	enabled bool
	results chan WorkerResult
	stopCh  chan struct{}

	// tickCounter monotonically increases each time Tick runs to completion
	// of the pick phase. Used only for observability — the per-tick
	// [picker] log line includes it so operators can correlate a quiet
	// stretch to a specific tick. Access is serialized because Tick runs
	// from a single ticker loop, but we increment under s.mu.Lock for
	// safety against future concurrent callers of Tick (tests, drains).
	tickCounter uint64

	// pickerDebug controls whether per-task [picker] decisions are logged
	// at debug level. Sampled once at startup from CLOCKWORK_SCHEDULER_DEBUG
	// so test runs don't flip mid-suite based on env changes.
	pickerDebug bool

	// Models + Profiles support cost backfill (Phase 2 of the modelcatalog
	// integration). Optional — set by serve.go after New(); when either is
	// nil the backfill code paths bail and cost is recorded as-reported.
	// Tests that don't exercise cost paths can leave them unset.
	Models   *modelcatalog.Catalog
	Profiles config.ProfileMap
	// CostBackfillEnabled gates the catalog-based estimate even when Models
	// + Profiles are wired. False during initial rollout so we can A/B against
	// executor-reported cost without lighting up the new code path.
	CostBackfillEnabled bool

	// Precheck holds Phase 4 dispatch-time guardrail config. Zero value =
	// no checks (safe default). Set by serve.go from settings.
	Precheck PrecheckOptions
}

// progressTokensWindow caps how often a run may emit a tokens-class
// run.progress event. Chatty LLM stream parsers can produce several token
// updates per second; 2s per run keeps the SSE stream readable without
// starving the low-frequency note/artifact signals.
const progressTokensWindow = 2 * time.Second

// New creates a new scheduler with all sub-components. predicates may be nil
// when no kind=wait tasks are expected; in that case, a wait task reaching
// the tick will be marked blocked with an "unknown predicate" reason.
func New(
	store *sqlstore.Store,
	q *queue.Queue,
	registry *executor.Registry,
	predicates *waitpoll.Registry,
	cfg *config.SchedulerConfig,
) *Scheduler {
	bus := NewEventBus()
	results := make(chan WorkerResult, cfg.Workers*2)

	pool := NewWorkerPool(cfg.Workers)
	pool.OnResult(func(r WorkerResult) {
		results <- r
	})

	if predicates == nil {
		predicates = waitpoll.NewRegistry()
	}

	picker := NewPicker(store)
	// DEPRECATED: remove when CW-20260417-0129 (workspace support) ships.
	// Stopgap project-scope filter is config-driven; read once here and
	// never mutated. An empty list leaves the picker in default
	// all-projects mode.
	picker.SetProjectAllowlist(cfg.ProjectAllowlist)

	s := &Scheduler{
		store:             store,
		queue:             q,
		registry:          registry,
		predicates:        predicates,
		cfg:               cfg,
		pool:              pool,
		picker:            picker,
		lifecycle:         NewLifecycleManager(store, bus),
		cost:              NewCostTracker(store),
		heartbeat:         NewHeartbeatMonitor(store),
		bus:               bus,
		progressThrottler: newProgressThrottler(progressTokensWindow),
		progressHeartbeat: newProgressHeartbeat(bus, time.Duration(cfg.HeartbeatProgressSeconds)*time.Second),
		cancels:           newCancelRegistry(),
		cancelGrace:       cancelGraceFromEnv(),
		enabled:           cfg.Enabled,
		results:           results,
		stopCh:            make(chan struct{}),
		pickerDebug:       isPickerDebugEnabled(),
	}

	// Subscribe to DB-driven task transitions so that a manual / external
	// task_transition out of "doing" cancels the in-flight worker's
	// context within one tick. Registered once at construction; the hook
	// lives for the life of the store (which outlives the scheduler, so
	// never-unregister is the correct choice here — see task_hooks.go).
	store.RegisterTaskTransitionHook(func(ev sqlstore.TaskTransitionEvent) {
		if ev.OldStatus != "doing" || ev.NewStatus == "doing" {
			return
		}
		if s.cancels.cancel(ev.TaskID) {
			log.Printf("[scheduler] canceling in-flight worker for %s (transitioned %s -> %s)",
				ev.TaskID, ev.OldStatus, ev.NewStatus)
		}
	})

	return s
}

// cancelGraceFromEnv parses CLOCKWORK_SCHED_CANCEL_GRACE (duration string
// accepted by time.ParseDuration, e.g. "5s", "500ms") and returns the
// configured grace period. Falls back to 5s when unset or unparseable —
// matches the ticket default and keeps shutdown latency predictable.
func cancelGraceFromEnv() time.Duration {
	const def = 5 * time.Second
	v := os.Getenv("CLOCKWORK_SCHED_CANCEL_GRACE")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("[scheduler] invalid CLOCKWORK_SCHED_CANCEL_GRACE=%q, using default %s", v, def)
		return def
	}
	return d
}

// isPickerDebugEnabled reads the debug-level toggle for the scheduler
// picker. Set CLOCKWORK_SCHEDULER_DEBUG=1 (or "true") to emit per-task
// [picker] decision logs. The per-tick counter line is ALWAYS emitted at
// info level regardless of this toggle.
func isPickerDebugEnabled() bool {
	switch os.Getenv("CLOCKWORK_SCHEDULER_DEBUG") {
	case "1", "true", "TRUE", "yes", "on":
		return true
	}
	return false
}

// EventBus returns the scheduler's event bus for subscribing to events.
func (s *Scheduler) EventBus() *EventBus {
	return s.bus
}

// Status returns the current scheduler status.
func (s *Scheduler) Status() SchedulerStatus {
	s.mu.RLock()
	enabled := s.enabled
	s.mu.RUnlock()

	depth, _ := s.queue.Depth(context.Background())
	total, _ := s.cost.GlobalTotal()

	return SchedulerStatus{
		Enabled:                        enabled,
		MaxWorkers:                     s.cfg.Workers,
		ActiveWorkers:                  s.pool.ActiveCount(),
		QueueDepth:                     depth,
		TotalCost:                      total,
		Subscribers:                    s.bus.SubscriberCount(),
		StaleHeartbeatThresholdSeconds: s.cfg.StaleSeconds,
	}
}

// SetEnabled enables or disables the scheduler.
func (s *Scheduler) SetEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = enabled
}

// Tick runs one scheduler cycle: pick eligible tasks, dispatch to workers.
func (s *Scheduler) Tick(ctx context.Context) error {
	s.mu.RLock()
	enabled := s.enabled
	s.mu.RUnlock()

	if !enabled {
		return nil
	}

	// Check cost budget
	if s.cfg.CostCeiling > 0 {
		ok, err := s.cost.WithinGlobalBudget(s.cfg.CostCeiling)
		if err != nil {
			return fmt.Errorf("scheduler: check cost budget: %w", err)
		}
		if !ok {
			log.Printf("[scheduler] global cost ceiling reached (%.2f), pausing", s.cfg.CostCeiling)
			return nil
		}
	}

	// Sweep timed-out checkpoints before picking — any pending row past
	// its timeout_at is flipped to "timed_out" and, if its task is parked
	// in review on that correlation_id, the task transitions review →
	// blocked. Running ahead of the pick keeps stale-parked tasks from
	// being considered for dispatch this tick.
	if _, err := SweepCheckpointTimeouts(s.store, s.bus, time.Now().UTC()); err != nil {
		log.Printf("[scheduler] checkpoint timeout sweep error: %v", err)
	}

	// Roll up parent-kind task statuses from their children. Runs before
	// the pick so a parent transitioned by the rollup isn't picked up
	// again this tick. Picker also filters kind=parent as belt-and-braces.
	if err := ParentRollupTick(s.store, s.bus); err != nil {
		log.Printf("[scheduler] parent rollup error: %v", err)
	}

	// Pick eligible tasks
	available := s.pool.AvailableSlots()
	if available <= 0 {
		return nil
	}

	tasks, decisions, err := s.picker.Pick(available)
	if err != nil {
		return fmt.Errorf("scheduler: pick tasks: %w", err)
	}

	s.mu.Lock()
	s.tickCounter++
	tickN := s.tickCounter
	s.mu.Unlock()

	// Per-tick info log: one line per tick with full counter breakdown.
	// Keep this line stable — it's the primary "why is the scheduler
	// silent?" diagnostic. Format is grep-friendly:
	//   [picker] tick=N candidates=C picked=P skipped_by_reason=map[...]
	log.Printf("[picker] tick=%d candidates=%d picked=%d skipped_by_reason=%s",
		tickN, decisions.Candidates, len(tasks), formatSkipCounts(decisions.Counts))

	// Per-task debug log: one line per skipped candidate. Gated behind
	// CLOCKWORK_SCHEDULER_DEBUG so steady-state operators don't drown in
	// per-tick noise.
	if s.pickerDebug {
		for _, d := range decisions.Skipped {
			log.Printf("[picker] tick=%d skip task=%s reason=%s", tickN, d.TaskID, d.Reason)
		}
	}

	for _, task := range tasks {
		if task.Kind == "wait" {
			// Wait tasks bypass the executor pool entirely — predicate poll
			// is cheap and synchronous. Errors stay in the scheduler log;
			// DispatchWait internally transitions the task to blocked on
			// fatal config errors so the state machine doesn't stall.
			t := task
			if err := DispatchWait(ctx, s.store, s.predicates, s.bus, &t); err != nil {
				log.Printf("[scheduler] wait dispatch %s: %v", task.ID, err)
			}
			continue
		}
		if err := s.dispatchTask(ctx, task); err != nil {
			log.Printf("[scheduler] failed to dispatch %s: %v", task.ID, err)
			continue
		}
	}

	// Check for stale workers. Zombie heartbeat rows (from a crashed or
	// force-killed serve where Deregister never ran) are logged, published on
	// the bus, and then deleted in the same pass. Without the delete, each
	// tick re-logs the same zombies indefinitely, and across sessions the
	// table accumulates noise that obscures real staleness signals
	// (CW-20260418-0003 secondary fix).
	staleThreshold := time.Duration(s.cfg.StaleSeconds) * time.Second
	stale, err := s.heartbeat.FindStale(staleThreshold)
	if err != nil {
		log.Printf("[scheduler] stale check error: %v", err)
	}
	for _, w := range stale {
		log.Printf("[scheduler] stale worker detected: %s (task %s)", w.WorkerID, w.TaskID)
		s.bus.Publish(SchedulerEvent{
			Type:   "worker.stale",
			TaskID: w.TaskID,
			Data:   map[string]interface{}{"worker_id": w.WorkerID},
		})
	}
	if len(stale) > 0 {
		deleted, derr := s.heartbeat.DeleteStale(staleThreshold)
		if derr != nil {
			log.Printf("[scheduler] stale cleanup error: %v", derr)
		} else if deleted > 0 {
			log.Printf("[scheduler] cleaned up %d stale heartbeat row(s)", deleted)
		}
	}

	// Per-tick heartbeat gauge (CW-20260418-0018). One info-level line per
	// tick so operators can see zombie accumulation *before* it bites. Stays
	// at info (not debug) because a silently-growing stale= counter is the
	// earliest signal that a Deregister path has regressed. Counts are
	// computed AFTER the cleanup above so stale= normally reads zero in
	// steady-state; a persistent non-zero stale= means DeleteStale didn't
	// keep up (e.g. threshold set too high, or an insert-after-sweep race).
	if counts, cerr := s.heartbeat.CountHeartbeats(staleThreshold); cerr != nil {
		log.Printf("[scheduler] heartbeat gauge error: %v", cerr)
	} else {
		log.Printf("[scheduler] heartbeats live=%d stale=%d threshold=%ds",
			counts.Live, counts.Stale, s.cfg.StaleSeconds)
	}

	s.bus.Publish(SchedulerEvent{Type: "scheduler.tick"})
	return nil
}

func (s *Scheduler) dispatchTask(ctx context.Context, task sqlstore.TaskRecord) error {
	// Look up executor
	exec, err := s.registry.Get(task.Executor)
	if err != nil {
		return fmt.Errorf("executor %q: %w", task.Executor, err)
	}

	// Pre-dispatch validation (CW-20260418-0010). Building the job here
	// rather than after the doing-transition lets Validate see the same
	// shape Run will; the runID is zero because no run has been created
	// yet — executors must only inspect job fields during Validate, not
	// side-effect on RunID. A PermanentError (missing agent profile,
	// unresolvable command, etc.) blocks the task directly without
	// consuming retry budget or spawning a worker. Non-permanent
	// validate errors fall through to the normal dispatch path; the
	// existing retry/lifecycle path handles them.
	preJob := buildJob(task, 0)
	if verr := exec.Validate(preJob); verr != nil {
		if executor.IsPermanent(verr) {
			return s.handlePermanentValidationError(task, verr)
		}
		log.Printf("[scheduler] non-permanent validate error for %s: %v", task.ID, verr)
	}

	// Phase 4 model precheck (CW-20260426-0036). Both context-window and
	// capability gates are opt-in via PrecheckOptions; the helper bails
	// silently when Models or the dispatched profile aren't wired, so
	// disabled configs see no overhead. A non-empty BlockReason routes
	// to the same blocked-with-reason path as a permanent Validate error.
	if pre := s.precheckDispatch(task, s.Precheck); pre.BlockReason != "" {
		return s.handlePermanentValidationError(task, fmt.Errorf("%s", pre.BlockReason))
	} else if len(pre.Warnings) > 0 {
		for _, w := range pre.Warnings {
			log.Printf("[scheduler] precheck warning for %s: %s", task.ID, w)
		}
	}

	// Transition to doing
	if err := s.store.TransitionTask(task.ID, "doing"); err != nil {
		return fmt.Errorf("transition to doing: %w", err)
	}

	// Create run record
	runID, err := s.store.CreateRun(&sqlstore.RunRecord{
		TaskID:   task.ID,
		Executor: task.Executor,
		Status:   "running",
	})
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	// Build execution job. RunID must be the DB-issued runs.id so the
	// executor can key stderr sidecars, the CLOCKWORK_RUN_ID env var, and
	// log messages on the same id observers see in runs table.
	job := buildJob(task, runID)

	// If per-run worktrees are enabled and the task has a working dir, try
	// to create an ephemeral worktree branched from origin/main and route
	// the executor into it. Failure is non-fatal — we log and fall back to
	// running in task.working_dir so a stale remote or git issue can't block
	// dispatch.
	wtPath := ""
	if s.cfg.WorktreePerRun && task.WorkingDir != "" {
		path, err := worktree.SetupPerRun(worktree.PerRunOptions{
			Enabled:  true,
			Root:     s.cfg.WorktreeRoot,
			KeepDays: s.cfg.WorktreeKeepDays,
		}, task.WorkingDir, runID)
		if err != nil {
			log.Printf("[scheduler] per-run worktree setup failed for %s run %d: %v (falling back to %s)", task.ID, runID, err, task.WorkingDir)
		} else {
			wtPath = path
			job.WorkingDir = path
			log.Printf("[scheduler] per-run worktree ready for %s run %d at %s", task.ID, runID, path)
		}
	}

	// Register heartbeat
	workerID := fmt.Sprintf("worker-%s-%d", task.ID, runID)
	s.heartbeat.Register(workerID, task.ID, runID, task.Executor)

	writeRunEvent(s.store, runID, task.ID, "task_transitioned", map[string]string{
		"from": "todo",
		"to":   "doing",
	})
	writeRunEvent(s.store, runID, task.ID, "run_started", map[string]string{
		"executor": task.Executor,
	})

	s.bus.Publish(SchedulerEvent{
		Type:   "task.transitioned",
		TaskID: task.ID,
		RunID:  runID,
		Data:   map[string]interface{}{"from": "todo", "to": "doing"},
	})

	startedAt := time.Now().UTC()
	s.bus.Publish(SchedulerEvent{
		Type:   "run.started",
		TaskID: task.ID,
		RunID:  runID,
		Data: map[string]interface{}{
			"executor":   task.Executor,
			"started_at": startedAt.Format(time.RFC3339),
		},
	})

	// Launch the synthetic heartbeat broadcaster so consumers see liveness
	// even when the executor isn't producing mid-stream signals. Stopped in
	// the worker closure defer alongside the HeartbeatMonitor deregistration.
	s.progressHeartbeat.start(task.ID, runID, workerID, startedAt)

	// Per-dispatch cancel context. Registered BEFORE pool.Submit so that a
	// cancel arriving while the worker is still queued on the semaphore
	// (or between submit and the goroutine's first line) is honored —
	// addresses the "cancellation between dispatch and first tick"
	// acceptance case in CW-20260418-0005. Parent is context.Background
	// rather than the tick ctx because the tick ctx goes away after Tick
	// returns and we don't want its cancellation to kill running workers;
	// scheduler Stop explicitly calls cancelAll to drain.
	dispatchCtx, dispatchCancel := context.WithCancel(context.Background())
	s.cancels.register(task.ID, dispatchCancel)

	// Submit to worker pool
	capturedRunID := runID
	capturedWorkerID := workerID
	capturedTaskID := task.ID
	capturedWorktree := wtPath
	capturedRepoHint := task.WorkingDir
	capturedDispatchCtx := dispatchCtx
	capturedDispatchCancel := dispatchCancel
	s.pool.Submit(task.ID, runID, func(wctx context.Context) (*executor.ExecutionResult, error) {
		// runCtx merges pool-shutdown cancellation (wctx) and per-task
		// cancellation (capturedDispatchCtx). Either source cancelling
		// will propagate into exec.Run so the child process can be torn
		// down via the executor's normal context path.
		runCtx, runCancel := mergeContexts(wctx, capturedDispatchCtx)
		defer runCancel()
		defer s.cancels.deregister(capturedTaskID)
		defer capturedDispatchCancel() // release context.Background goroutine
		defer s.heartbeat.Deregister(capturedWorkerID)
		defer s.progressHeartbeat.stop(capturedRunID)
		defer s.progressThrottler.release(capturedRunID)
		defer func() {
			if capturedWorktree == "" {
				return
			}
			removed, err := worktree.CleanupPerRun(capturedRepoHint, capturedWorktree)
			switch {
			case err != nil:
				log.Printf("[scheduler] per-run worktree cleanup failed for %s run %d at %s: %v", capturedTaskID, capturedRunID, capturedWorktree, err)
			case removed:
				log.Printf("[scheduler] per-run worktree removed for %s run %d at %s", capturedTaskID, capturedRunID, capturedWorktree)
			default:
				log.Printf("[scheduler] per-run worktree preserved for %s run %d at %s (commits or uncommitted work present)", capturedTaskID, capturedRunID, capturedWorktree)
			}
		}()

		// Create event callback that updates heartbeat and run
		cb := func(event executor.ExecutionEvent) {
			s.heartbeat.Beat(capturedWorkerID)

			if event.Type == executor.EventArtifact && event.Artifact != nil {
				var metadataJSON sql.NullString
				if event.Artifact.Metadata != nil {
					if b, err := json.Marshal(event.Artifact.Metadata); err == nil {
						metadataJSON = sql.NullString{String: string(b), Valid: true}
					}
				}
				s.store.CreateArtifact(&sqlstore.ArtifactRecord{
					TaskID:   capturedTaskID,
					RunID:    sql.NullInt64{Int64: capturedRunID, Valid: true},
					Type:     event.Artifact.Type,
					Content:  event.Artifact.Content,
					URL:      event.Artifact.URL,
					FilePath: event.Artifact.FilePath,
					Metadata: metadataJSON,
				})
			}

			// CLOCKWORK_SUBTODO_DONE: mark the named subtodo as done and
			// record the agent's evidence token. Parse failures and unknown
			// item-ids are logged but non-fatal — the gate at lifecycle
			// time will block the task if required items stay unchecked.
			if event.Type == executor.EventSignal && event.Signal == "CLOCKWORK_SUBTODO_DONE" {
				itemID, evidence, perr := executor.ParseSubtodoDonePayload(event.Content)
				if perr != nil {
					log.Printf("[scheduler] subtodo signal parse error for %s: %v", capturedTaskID, perr)
				} else if err := s.store.SetSubtodoDone(capturedTaskID, itemID, evidence); err != nil {
					log.Printf("[scheduler] subtodo done write error for %s/%s: %v", capturedTaskID, itemID, err)
				} else {
					s.bus.Publish(SchedulerEvent{
						Type:   "subtodo.done",
						TaskID: capturedTaskID,
						RunID:  capturedRunID,
						Data: map[string]interface{}{
							"item_id":  itemID,
							"evidence": evidence,
						},
					})
				}
			}

			// Inline-form CLOCKWORK_CHECKPOINT emits are routed to the
			// checkpoint handler which creates the row and parks the task
			// if its checkpoint_mode is "blocking". JSON-form checkpoints
			// (event.Signal == "CLOCKWORK_CHECKPOINT" with a JSON payload)
			// are left for later work — MVP emits via the inline form.
			if event.Type == executor.EventSignal && event.Signal == "CLOCKWORK_CHECKPOINT" {
				if out, err := HandleCheckpointSignal(s.store, capturedTaskID, capturedRunID, event.Content, time.Now().UTC()); err != nil {
					log.Printf("[scheduler] checkpoint emit error for %s: %v", capturedTaskID, err)
				} else {
					s.bus.Publish(SchedulerEvent{
						Type:   "checkpoint.emitted",
						TaskID: capturedTaskID,
						RunID:  capturedRunID,
						Data: map[string]interface{}{
							"correlation_id": out.CorrelationID,
							"type":           out.Type,
							"park":           out.ParkTask,
						},
					})
				}
			}

			writeRunEvent(s.store, capturedRunID, capturedTaskID, runEventType(event), runEventPayload(event))

			s.bus.Publish(SchedulerEvent{
				Type:   "run.event",
				TaskID: capturedTaskID,
				RunID:  capturedRunID,
				Data:   map[string]interface{}{"event_type": event.Type.String(), "content": event.Content},
			})

			s.publishProgress(capturedTaskID, capturedRunID, event)
		}

		result, err := exec.Run(runCtx, job, cb)

		// Cancellation branch. When the per-task dispatch context is
		// cancelled (DB transition out of doing) or the pool is shutting
		// down, the executor's Run returns either a context error or
		// exits successfully after honoring its grace window. We detect
		// this by checking the dispatch context FIRST — if it's been
		// cancelled we treat the run as cancelled regardless of what err
		// is (the executor may return a legitimate result if it finished
		// a stream between the cancel fire and the check). Marking
		// status=canceled with a structured reason keeps retry budgets
		// intact and gives operators a clear signal.
		if capturedDispatchCtx.Err() != nil {
			reason := "task_transition_out_of_doing"
			s.store.CompleteRun(capturedRunID, sqlstore.RunCompletion{
				Status:       "canceled",
				ErrorMessage: reason,
			})
			writeRunEvent(s.store, capturedRunID, capturedTaskID, "run_canceled", map[string]string{
				"reason": reason,
			})
			s.bus.Publish(SchedulerEvent{
				Type:   "run.canceled",
				TaskID: capturedTaskID,
				RunID:  capturedRunID,
				Data:   map[string]interface{}{"reason": reason},
			})
			// Return a result with Status=canceled so the lifecycle
			// manager can short-circuit: the task has already been
			// transitioned by the external actor, and we must not
			// retry, re-queue, or block.
			return &executor.ExecutionResult{
				Status: "canceled",
				Reason: reason,
			}, nil
		}

		if err != nil {
			// Complete run with error
			s.store.CompleteRun(capturedRunID, sqlstore.RunCompletion{
				Status:       "failed",
				ErrorMessage: err.Error(),
			})
			// TODO(path-b): write run_completed run_event here too so failed-run observability
			// doesn't require cross-referencing task_transitioned. Success path writes it at
			// line ~287; failure path transitions via lifecycle which writes task_transitioned.
			return nil, err
		}

		// Complete run with result
		s.store.CompleteRun(capturedRunID, sqlstore.RunCompletion{
			Status:           result.Status,
			PromptTokens:     result.Tokens.PromptTokens,
			CompletionTokens: result.Tokens.CompletionTokens,
			Cost:             result.Cost,
		})

		// Record cost. resolveCost decides whether the executor's reported
		// figure is authoritative or whether to backfill from the models.dev
		// catalog when result.Cost is 0 but tokens > 0 (Phase 2 of the
		// modelcatalog series).
		sprintID := ""
		if task.SprintID.Valid {
			sprintID = task.SprintID.String
		}
		cost, source := s.resolveCost(task.AgentProfile, result)
		s.cost.Record(CostEntry{
			TaskID:           capturedTaskID,
			RunID:            capturedRunID,
			SprintID:         sprintID,
			Cost:             cost,
			PromptTokens:     result.Tokens.PromptTokens,
			CompletionTokens: result.Tokens.CompletionTokens,
			Source:           source,
		})

		writeRunEvent(s.store, capturedRunID, capturedTaskID, "run_completed", map[string]interface{}{
			"status": result.Status,
			"cost":   result.Cost,
		})

		s.bus.Publish(SchedulerEvent{
			Type:   "run.completed",
			TaskID: capturedTaskID,
			RunID:  capturedRunID,
			Data: map[string]interface{}{
				"status": result.Status,
				"cost":   result.Cost,
			},
		})

		return result, nil
	})

	// Post-submit dispatch log. Emitted AFTER pool.Submit so a failure in
	// any earlier step (transition, run create, worktree setup) never
	// produces a misleading "dispatched" entry. Pairs with the
	// run.completed event emitted by the worker closure on finish.
	log.Printf("[scheduler] dispatched task=%s run=%d executor=%s profile=%s worker=%s",
		task.ID, runID, task.Executor, task.AgentProfile, workerID)

	return nil
}

// handlePermanentValidationError blocks a task whose pre-dispatch Validate()
// returned a PermanentError. Config-permanent failures (missing agent
// profile, unresolvable command, etc.) cannot be recovered by retrying —
// the task transitions straight to "blocked" with blocked_reason set to
// the validation error text (the profile name is included by executors
// that care). A single run row is created for audit (status=blocked,
// error_message populated) so the run table still carries a record of the
// attempted dispatch; retry_count is NEVER incremented. See CW-20260418-0010.
func (s *Scheduler) handlePermanentValidationError(task sqlstore.TaskRecord, verr error) error {
	reason := verr.Error()

	// Audit: single runs row with status=blocked + error_message. If the
	// CreateRun write fails we still proceed to block the task — the task
	// state is the operator-facing signal, the runs row is supporting audit.
	runID, rerr := s.store.CreateRun(&sqlstore.RunRecord{
		TaskID:       task.ID,
		Executor:     task.Executor,
		Status:       "blocked",
		ErrorMessage: reason,
	})
	if rerr != nil {
		log.Printf("[scheduler] audit run create failed for %s: %v", task.ID, rerr)
	}

	if err := s.store.TransitionTaskWithReason(task.ID, "blocked", reason); err != nil {
		return fmt.Errorf("transition to blocked: %w", err)
	}

	writeRunEvent(s.store, runID, task.ID, "task_transitioned", map[string]string{
		"from":   "todo",
		"to":     "blocked",
		"reason": reason,
	})
	writeRunEvent(s.store, runID, task.ID, "run_blocked_permanent", map[string]string{
		"reason": reason,
	})

	s.bus.Publish(SchedulerEvent{
		Type:   "task.transitioned",
		TaskID: task.ID,
		RunID:  runID,
		Data: map[string]interface{}{
			"from":   "todo",
			"to":     "blocked",
			"reason": reason,
		},
	})

	log.Printf("[scheduler] pre-dispatch validation blocked %s (permanent): %s", task.ID, reason)
	return nil
}

// DrainResults processes all pending worker results through the lifecycle manager.
func (s *Scheduler) DrainResults() {
	for {
		select {
		case r := <-s.results:
			if r.Err != nil {
				log.Printf("[scheduler] worker error for %s (run %d): %v", r.TaskID, r.RunID, r.Err)
				// Treat executor errors as failures
				s.lifecycle.HandleResult(r.TaskID, r.RunID, &executor.ExecutionResult{
					Status: "failed",
					Reason: r.Err.Error(),
				})
				continue
			}
			if r.Result != nil {
				if err := s.lifecycle.HandleResult(r.TaskID, r.RunID, r.Result); err != nil {
					log.Printf("[scheduler] lifecycle error for %s (run %d): %v", r.TaskID, r.RunID, err)
				}
			}
		default:
			return
		}
	}
}

// Run starts the scheduler loop. Blocks until context is cancelled.
func (s *Scheduler) Run(ctx context.Context) error {
	interval := time.Duration(s.cfg.IntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("[scheduler] started (workers=%d, interval=%s, stale_heartbeat_threshold=%ds)",
		s.cfg.Workers, interval, s.cfg.StaleSeconds)

	// Best-effort sweep of orphaned per-run worktrees on startup. Requires
	// CLOCKWORK_REPO so we know which repo's admin to prune against; if the
	// operator hasn't set it, the sweep is silently skipped.
	if s.cfg.WorktreePerRun && s.cfg.WorktreeKeepDays > 0 && envRepoRoot() != "" {
		removed, errs := worktree.SweepPerRun(envRepoRoot(), s.cfg.WorktreeRoot, s.cfg.WorktreeKeepDays, time.Now())
		for _, p := range removed {
			log.Printf("[scheduler] swept stale worktree %s", p)
		}
		for _, e := range errs {
			log.Printf("[scheduler] worktree sweep error: %v", e)
		}
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[scheduler] stopping...")
			return s.Stop(ctx)
		case <-ticker.C:
			s.DrainResults()
			if err := s.Tick(ctx); err != nil {
				log.Printf("[scheduler] tick error: %v", err)
			}
		}
	}
}

// Stop gracefully shuts down the scheduler.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.DrainResults()
	// Cancel any registered per-task dispatch contexts before telling the
	// pool to shut down. This keeps cancellation semantics consistent on
	// shutdown: workers see runCtx cancelled and write status=canceled
	// rather than leaving runs in "running" until the pool hard-cancels.
	// Pool shutdown still follows to release the semaphore and wait on
	// the WaitGroup.
	s.cancels.cancelAll()
	if err := s.pool.Shutdown(ctx); err != nil {
		return fmt.Errorf("scheduler: shutdown workers: %w", err)
	}
	s.bus.Close()
	return nil
}

// publishProgress emits a run.progress SSE event for user-visible signals
// (notes, artifacts, tokens). Log lines and internal signals (CLOCKWORK_DONE,
// CLOCKWORK_BLOCKED, CLOCKWORK_CHECKPOINT, etc.) are intentionally skipped —
// those drive lifecycle transitions and are not activity-feed content.
// Tokens-class emissions are rate-limited via progressThrottler to keep the
// SSE stream readable during chatty streaming runs.
func (s *Scheduler) publishProgress(taskID string, runID int64, event executor.ExecutionEvent) {
	switch {
	case event.Type == executor.EventSignal && event.Signal == "CLOCKWORK_NOTE":
		s.bus.Publish(SchedulerEvent{
			Type:   "run.progress",
			TaskID: taskID,
			RunID:  runID,
			Data: map[string]interface{}{
				"kind": "note",
				"text": event.Content,
			},
		})

	case event.Type == executor.EventArtifact && event.Artifact != nil:
		s.bus.Publish(SchedulerEvent{
			Type:   "run.progress",
			TaskID: taskID,
			RunID:  runID,
			Data: map[string]interface{}{
				"kind":          "artifact",
				"artifact_type": event.Artifact.Type,
				"content":       event.Artifact.Content,
				"url":           event.Artifact.URL,
				"file_path":     event.Artifact.FilePath,
			},
		})

	case event.Type == executor.EventToolUse && event.ToolUse != nil:
		s.bus.Publish(SchedulerEvent{
			Type:   "run.progress",
			TaskID: taskID,
			RunID:  runID,
			Data: map[string]interface{}{
				"kind":         "tool_use",
				"tool_name":    event.ToolUse.Name,
				"args_summary": event.ToolUse.ArgsSummary,
			},
		})

	case event.Type == executor.EventTokenUsage,
		event.Type == executor.EventSignal && event.Signal == "CLOCKWORK_TOKENS":
		if !s.progressThrottler.allow(runID, time.Now()) {
			return
		}
		data := map[string]interface{}{"kind": "tokens"}
		if event.Tokens != nil {
			data["prompt"] = event.Tokens.PromptTokens
			data["completion"] = event.Tokens.CompletionTokens
			data["cost"] = event.Tokens.Cost
		} else if event.Type == executor.EventSignal && event.Content != "" {
			p, c, cost := executor.ParseTokenPayload(event.Content)
			data["prompt"] = p
			data["completion"] = c
			data["cost"] = cost
		}
		s.bus.Publish(SchedulerEvent{
			Type:   "run.progress",
			TaskID: taskID,
			RunID:  runID,
			Data:   data,
		})
	}
}

func buildJob(task sqlstore.TaskRecord, runID int64) *executor.ExecutionJob {
	job := &executor.ExecutionJob{
		TaskID:       task.ID,
		RunID:        runID,
		Description:  task.Description,
		SystemPrompt: task.SystemPrompt,
		AgentFile:    task.AgentFile,
		WorkingDir:   task.WorkingDir,
		AgentProfile: task.AgentProfile,
		Limits: executor.ExecutionLimits{
			MaxRetries: task.MaxRetries,
		},
	}

	// Parse JSON fields
	if task.Tools.Valid && task.Tools.String != "" {
		json.Unmarshal([]byte(task.Tools.String), &job.Tools)
	}
	if task.Permissions.Valid && task.Permissions.String != "" {
		json.Unmarshal([]byte(task.Permissions.String), &job.Permissions)
	}
	if task.Environment.Valid && task.Environment.String != "" {
		json.Unmarshal([]byte(task.Environment.String), &job.Environment)
	}
	if task.Files.Valid && task.Files.String != "" {
		json.Unmarshal([]byte(task.Files.String), &job.Files)
	}
	if task.Deliverables.Valid && task.Deliverables.String != "" {
		json.Unmarshal([]byte(task.Deliverables.String), &job.Deliverables)
	}
	if task.Metadata.Valid && task.Metadata.String != "" {
		var md map[string]any
		if err := json.Unmarshal([]byte(task.Metadata.String), &md); err == nil {
			job.Metadata = md
		}
	}

	// Parse limits
	if task.CostBudget.Valid {
		v := task.CostBudget.Float64
		job.Limits.CostBudget = &v
	}
	if task.TokenBudget.Valid {
		v := int(task.TokenBudget.Int64)
		job.Limits.TokenBudget = &v
	}
	if task.MaxDurationMs.Valid {
		d := time.Duration(task.MaxDurationMs.Int64) * time.Millisecond
		job.Limits.MaxDuration = &d
	}

	return job
}

// formatSkipCounts renders a PickDecisions.Counts map as a stable
// "map[key1:N key2:M]" string matching Go's fmt default for maps but with
// a deterministic key order so operators grepping across ticks can diff
// cleanly. An empty map renders as "map[]" so the log line shape is
// always parseable.
func formatSkipCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "map[]"
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("map[")
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s:%d", k, counts[k])
	}
	b.WriteByte(']')
	return b.String()
}

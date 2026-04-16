package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/waitpoll"
)

// SchedulerStatus reports the current state of the scheduler.
type SchedulerStatus struct {
	Enabled       bool    `json:"enabled"`
	MaxWorkers    int     `json:"max_workers"`
	ActiveWorkers int     `json:"active_workers"`
	QueueDepth    int     `json:"queue_depth"`
	TotalCost     float64 `json:"total_cost"`
	Subscribers   int     `json:"subscribers"`
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

	pool      *WorkerPool
	picker    *Picker
	lifecycle *LifecycleManager
	cost      *CostTracker
	heartbeat *HeartbeatMonitor
	bus       *EventBus

	mu      sync.RWMutex
	enabled bool
	results chan WorkerResult
	stopCh  chan struct{}
}

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

	s := &Scheduler{
		store:      store,
		queue:      q,
		registry:   registry,
		predicates: predicates,
		cfg:        cfg,
		pool:       pool,
		picker:     NewPicker(store),
		lifecycle:  NewLifecycleManager(store, bus),
		cost:       NewCostTracker(store),
		heartbeat:  NewHeartbeatMonitor(store),
		bus:        bus,
		enabled:    cfg.Enabled,
		results:    results,
		stopCh:     make(chan struct{}),
	}

	return s
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
		Enabled:       enabled,
		MaxWorkers:    s.cfg.Workers,
		ActiveWorkers: s.pool.ActiveCount(),
		QueueDepth:    depth,
		TotalCost:     total,
		Subscribers:   s.bus.SubscriberCount(),
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

	// Pick eligible tasks
	available := s.pool.AvailableSlots()
	if available <= 0 {
		return nil
	}

	tasks, err := s.picker.Pick(available)
	if err != nil {
		return fmt.Errorf("scheduler: pick tasks: %w", err)
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

	// Sweep timed-out checkpoints — any pending row past its timeout_at is
	// flipped to "timed_out" and, if its task is parked in review on that
	// correlation_id, the task transitions review → blocked.
	if _, err := SweepCheckpointTimeouts(s.store, s.bus, time.Now().UTC()); err != nil {
		log.Printf("[scheduler] checkpoint timeout sweep error: %v", err)
	}

	// Check for stale workers
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

	s.bus.Publish(SchedulerEvent{Type: "scheduler.tick"})
	return nil
}

func (s *Scheduler) dispatchTask(ctx context.Context, task sqlstore.TaskRecord) error {
	// Look up executor
	exec, err := s.registry.Get(task.Executor)
	if err != nil {
		return fmt.Errorf("executor %q: %w", task.Executor, err)
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

	// Build execution job
	job := buildJob(task)

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

	s.bus.Publish(SchedulerEvent{
		Type:   "run.started",
		TaskID: task.ID,
		RunID:  runID,
	})

	// Submit to worker pool
	capturedRunID := runID
	capturedWorkerID := workerID
	capturedTaskID := task.ID
	s.pool.Submit(task.ID, runID, func(wctx context.Context) (*executor.ExecutionResult, error) {
		defer s.heartbeat.Deregister(capturedWorkerID)

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
		}

		result, err := exec.Run(wctx, job, cb)
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

		// Record cost
		sprintID := ""
		if task.SprintID.Valid {
			sprintID = task.SprintID.String
		}
		s.cost.Record(CostEntry{
			TaskID:           capturedTaskID,
			RunID:            capturedRunID,
			SprintID:         sprintID,
			Cost:             result.Cost,
			PromptTokens:     result.Tokens.PromptTokens,
			CompletionTokens: result.Tokens.CompletionTokens,
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

	log.Printf("[scheduler] started (workers=%d, interval=%s)", s.cfg.Workers, interval)

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
	if err := s.pool.Shutdown(ctx); err != nil {
		return fmt.Errorf("scheduler: shutdown workers: %w", err)
	}
	s.bus.Close()
	return nil
}

func buildJob(task sqlstore.TaskRecord) *executor.ExecutionJob {
	job := &executor.ExecutionJob{
		TaskID:       task.ID,
		Description:  task.Description,
		SystemPrompt: task.SystemPrompt,
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

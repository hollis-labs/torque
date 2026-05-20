// Package healthscan implements the system health agent's DETECT half
// (CW-20260519-0083): a deterministic scanner over the canonical state in
// SQLite (tasks, runs, worker_heartbeats) that surfaces orphaned and
// stuck worker rows the scheduler's tick-time stale check cannot see.
//
// The tick-time path in scheduler.go uses FindStale(threshold) — it only
// considers a worker an orphan after its heartbeat is older than
// StaleSeconds (default 900s, raised from 300s in CW-20260519-0079). That
// is the right shape for the steady-state case but leaves two gaps:
//
//  1. Daemon restart. The previous serve's heartbeat rows survive into
//     the new process but the new cancelRegistry is empty. Every
//     surviving row is by definition an orphan (its worker pid is gone)
//     yet the new serve can't act on them until each row independently
//     ages past the staleness threshold. Run 864 / CW-20260515-0133
//     observed this directly — the task zombied for ~1h47m in `doing`.
//
//  2. Heartbeat-row-missing-but-task-doing. If a heartbeat row is
//     deleted (deregister succeeded) but the lifecycle path failed to
//     transition the task out of `doing` (crash mid-completion, manual
//     DB poke, partial txn), the task is invisible to a heartbeat-only
//     sweep — there is nothing to FindStale against. Same story for a
//     `runs` row stuck at status='running' with no heartbeat.
//
// The scanner reports anomalies; it does NOT mutate state. The
// scheduler decides what to do with each Anomaly — boot-mode results
// are routed through the existing recoverOrphanedWorker primitive
// (CW-20260519-0079 owns RECOVER for the orphan-worker case); tick-mode
// `task_doing_no_worker` / `run_running_no_worker` anomalies are
// detect-only today and paired with the session-recovery work.
//
// Surface: New(store, liveness, cfg) → Scan(ctx, mode) → Result. The
// Liveness interface is satisfied by scheduler.cancelRegistry; tests
// pass an in-memory fake. NowFunc is injectable so test fixtures don't
// have to backdate rows with literal timestamps.
package healthscan

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Mode selects which gaps the scanner emphasizes. Both modes return the
// same Anomaly shape; the wire-up differs in (a) whether the staleness
// threshold is applied to heartbeat rows and (b) what the scheduler
// does with each kind.
type Mode string

const (
	// ModeBoot scans every heartbeat row regardless of staleness. The
	// new process's cancelRegistry is empty, so any surviving row is an
	// orphan-by-definition and must be reclaimed immediately to close
	// the run 864 gap. The scheduler routes these through
	// recoverOrphanedWorker.
	ModeBoot Mode = "boot"

	// ModeTick scans only heartbeat rows past the staleness threshold,
	// matching the steady-state shape of the existing tick-time sweep.
	// The scanner also surfaces tasks-in-doing-no-row and
	// runs-in-running-no-row, which the staleness-keyed sweep cannot
	// observe.
	ModeTick Mode = "tick"
)

// Liveness is the in-process "is this worker still alive in MY scheduler"
// signal. Satisfied by scheduler.cancelRegistry — a taskID in the
// registry means the worker closure is still on the worker-pool wait
// group, even if its DB heartbeat aged out. The scanner uses Has to
// distinguish a slow-but-live worker from a real orphan. Tests pass an
// in-memory implementation; on boot the scheduler passes an empty
// registry, which Has correctly reports as Has(_)=false for every key.
type Liveness interface {
	Has(taskID string) bool
}

// AnomalyKind is the typed category for an observation. Stable strings
// are part of the contract — the scheduler's log lines, the bus event
// Data["kind"] field, and any future dashboard wire-up all key on these
// values.
type AnomalyKind string

const (
	// AnomalyOrphanWorker — a worker_heartbeats row whose taskID is NOT
	// in the in-process Liveness registry. In boot mode this fires for
	// every surviving row (because cancelRegistry is empty by
	// construction); in tick mode it fires only for rows whose
	// last_heartbeat is older than StaleHeartbeat. The scheduler routes
	// these into recoverOrphanedWorker.
	AnomalyOrphanWorker AnomalyKind = "orphan_worker"

	// AnomalyTaskDoingNoWorker — a tasks row at status='doing' with NO
	// matching worker_heartbeats row. The heartbeat lifecycle has
	// already deregistered, but the task never transitioned out of
	// `doing`. Invisible to the staleness sweep (nothing to find
	// stale). Detect-only today; the RECOVER half is tracked
	// separately.
	AnomalyTaskDoingNoWorker AnomalyKind = "task_doing_no_worker"

	// AnomalyRunRunningNoWorker — a runs row at status='running' with
	// NO matching worker_heartbeats row keyed by run_id. The run never
	// reached CompleteRun. Same blind-spot class as
	// task_doing_no_worker but keyed off the runs table — a task may
	// have moved on while its prior run row was abandoned.
	AnomalyRunRunningNoWorker AnomalyKind = "run_running_no_worker"
)

// Anomaly describes one observation. Zero-valued fields are normal for
// kinds that don't carry them (e.g. AnomalyTaskDoingNoWorker has no
// WorkerID, no LastHeartbeat).
type Anomaly struct {
	Kind          AnomalyKind
	TaskID        string
	RunID         int64
	WorkerID      string
	Executor      string
	LastHeartbeat time.Time
	// Detail is a free-form human-readable note. Logged verbatim by the
	// scheduler; not part of the structured wire shape.
	Detail string
}

// Config holds the scanner's tunables. The Now hook is for tests — at
// production callers leave it nil and time.Now is used.
type Config struct {
	// StaleHeartbeat is the threshold applied in ModeTick to decide
	// whether a heartbeat row's age makes it eligible to be classified
	// as an orphan-with-no-cancel. Ignored in ModeBoot (every row is
	// scanned).
	StaleHeartbeat time.Duration

	// Now returns the current time. Nil → time.Now.UTC. Plumbed for
	// deterministic tests; in production all paths route through
	// time.Now.UTC directly.
	Now func() time.Time
}

// Scanner is the DETECT primitive. Construct with New, call Scan once
// per cycle.
type Scanner struct {
	store    *sqlstore.Store
	liveness Liveness
	cfg      Config
}

// New constructs a Scanner. liveness MUST NOT be nil — at boot pass an
// empty cancelRegistry (its Has correctly reports false for every key,
// which is what we want).
func New(store *sqlstore.Store, liveness Liveness, cfg Config) *Scanner {
	return &Scanner{store: store, liveness: liveness, cfg: cfg}
}

// Result is the per-call output. Mode echoes the caller's choice so a
// shared subscriber can route boot vs tick events without re-reading
// state. StartedAt is sampled at Scan entry, AT is sampled at exit —
// the delta is the scan's wall-clock cost.
type Result struct {
	Mode        Mode
	StartedAt   time.Time
	CompletedAt time.Time
	Anomalies   []Anomaly
}

// Scan executes one full sweep and returns the observations. It does
// NOT mutate task / run / heartbeat state — callers are expected to act
// on the result (log, publish, recover).
//
// The three SELECTs are deliberately small and independent: the
// scanner runs at boot (one-shot) and once per tick (alongside the
// existing FindStale call), so chattering against the read-side of the
// store is fine for the sizes the daemon sees. If the heartbeat table
// ever grows large enough to matter, the scan can be refactored into a
// single LEFT JOIN — but the explicit shape today is easier to test
// and to reason about, and matches the existing FindStale style.
func (s *Scanner) Scan(ctx context.Context, mode Mode) (Result, error) {
	now := time.Now().UTC()
	if s.cfg.Now != nil {
		now = s.cfg.Now()
	}
	res := Result{Mode: mode, StartedAt: now}

	hbs, err := s.scanHeartbeats(ctx, mode, now)
	if err != nil {
		return res, fmt.Errorf("healthscan: heartbeats: %w", err)
	}
	res.Anomalies = append(res.Anomalies, hbs...)

	dnw, err := s.scanTasksDoingNoWorker(ctx)
	if err != nil {
		return res, fmt.Errorf("healthscan: tasks_doing_no_worker: %w", err)
	}
	res.Anomalies = append(res.Anomalies, dnw...)

	rnw, err := s.scanRunsRunningNoWorker(ctx)
	if err != nil {
		return res, fmt.Errorf("healthscan: runs_running_no_worker: %w", err)
	}
	res.Anomalies = append(res.Anomalies, rnw...)

	if s.cfg.Now != nil {
		res.CompletedAt = s.cfg.Now()
	} else {
		res.CompletedAt = time.Now().UTC()
	}
	return res, nil
}

// scanHeartbeats walks worker_heartbeats and flags rows whose taskID
// is NOT in the liveness registry. In ModeBoot, every row is
// considered (because the registry is empty); in ModeTick, only rows
// past StaleHeartbeat are.
func (s *Scanner) scanHeartbeats(ctx context.Context, mode Mode, now time.Time) ([]Anomaly, error) {
	var (
		rows *sql.Rows
		err  error
	)
	switch mode {
	case ModeBoot:
		rows, err = s.store.DB().QueryContext(ctx,
			`SELECT worker_id, task_id, run_id, executor, last_heartbeat
			 FROM worker_heartbeats`)
	case ModeTick:
		thresholdSeconds := int(s.cfg.StaleHeartbeat.Seconds())
		if thresholdSeconds <= 0 {
			// A zero/negative threshold in tick mode would behave like
			// boot mode (every row past 0s of age). That isn't what
			// the caller asked for — skip the heartbeat sweep entirely
			// in this case rather than producing a flood. The
			// scheduler enforces StaleSeconds > 0 at config-load.
			return nil, nil
		}
		rows, err = s.store.DB().QueryContext(ctx,
			`SELECT worker_id, task_id, run_id, executor, last_heartbeat
			 FROM worker_heartbeats
			 WHERE last_heartbeat < datetime('now', '-' || ? || ' seconds')`,
			thresholdSeconds)
	default:
		return nil, fmt.Errorf("healthscan: unknown mode %q", mode)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Anomaly
	for rows.Next() {
		var (
			workerID, taskID, executor string
			runID                      int64
			lastHB                     time.Time
		)
		if err := rows.Scan(&workerID, &taskID, &runID, &executor, &lastHB); err != nil {
			return nil, err
		}
		if s.liveness.Has(taskID) {
			continue
		}
		detail := "no in-process cancelRegistry entry for taskID"
		if mode == ModeTick {
			detail = fmt.Sprintf("stale heartbeat (age %s) and no in-process cancelRegistry entry", now.Sub(lastHB).Round(time.Second))
		}
		out = append(out, Anomaly{
			Kind:          AnomalyOrphanWorker,
			TaskID:        taskID,
			RunID:         runID,
			WorkerID:      workerID,
			Executor:      executor,
			LastHeartbeat: lastHB,
			Detail:        detail,
		})
	}
	return out, rows.Err()
}

// scanTasksDoingNoWorker finds tasks parked at status='doing' with no
// matching worker_heartbeats row. The matching key is task_id (each
// dispatch registers exactly one heartbeat row keyed by worker_id but
// indexed on task_id by the scheduler's lookup path).
func (s *Scanner) scanTasksDoingNoWorker(ctx context.Context) ([]Anomaly, error) {
	rows, err := s.store.DB().QueryContext(ctx,
		`SELECT t.id, t.executor
		 FROM tasks t
		 LEFT JOIN worker_heartbeats h ON h.task_id = t.id
		 WHERE t.status = 'doing' AND h.task_id IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Anomaly
	for rows.Next() {
		var taskID, executor string
		if err := rows.Scan(&taskID, &executor); err != nil {
			return nil, err
		}
		out = append(out, Anomaly{
			Kind:     AnomalyTaskDoingNoWorker,
			TaskID:   taskID,
			Executor: executor,
			Detail:   "task is `doing` but no worker_heartbeats row exists",
		})
	}
	return out, rows.Err()
}

// scanRunsRunningNoWorker finds runs at status='running' with no
// matching worker_heartbeats row. The key is run_id — a worker
// dispatch always writes (task_id, run_id) together so absence on
// run_id is a true "no worker is keeping this run alive" signal.
func (s *Scanner) scanRunsRunningNoWorker(ctx context.Context) ([]Anomaly, error) {
	rows, err := s.store.DB().QueryContext(ctx,
		`SELECT r.id, r.task_id, r.executor
		 FROM runs r
		 LEFT JOIN worker_heartbeats h ON h.run_id = r.id
		 WHERE r.status = 'running' AND h.run_id IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Anomaly
	for rows.Next() {
		var (
			runID            int64
			taskID, executor string
		)
		if err := rows.Scan(&runID, &taskID, &executor); err != nil {
			return nil, err
		}
		out = append(out, Anomaly{
			Kind:     AnomalyRunRunningNoWorker,
			TaskID:   taskID,
			RunID:    runID,
			Executor: executor,
			Detail:   "run is `running` but no worker_heartbeats row keyed by run_id",
		})
	}
	return out, rows.Err()
}

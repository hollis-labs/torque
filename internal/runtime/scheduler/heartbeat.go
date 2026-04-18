package scheduler

import (
	"database/sql"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// StaleWorker represents a worker that has not sent a heartbeat within the threshold.
type StaleWorker struct {
	WorkerID      string
	TaskID        string
	RunID         int64
	Executor      string
	LastHeartbeat time.Time
}

// HeartbeatMonitor tracks worker liveness via periodic heartbeats.
type HeartbeatMonitor struct {
	store *sqlstore.Store
}

// NewHeartbeatMonitor creates a new heartbeat monitor.
func NewHeartbeatMonitor(store *sqlstore.Store) *HeartbeatMonitor {
	return &HeartbeatMonitor{store: store}
}

// Register records a new active worker.
func (h *HeartbeatMonitor) Register(workerID, taskID string, runID int64, executor string) error {
	_, err := h.store.DB().Exec(
		`INSERT OR REPLACE INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		workerID, taskID, runID, executor,
	)
	return err
}

// Beat updates the heartbeat timestamp for a worker.
func (h *HeartbeatMonitor) Beat(workerID string) error {
	_, err := h.store.DB().Exec(
		`UPDATE worker_heartbeats SET last_heartbeat = CURRENT_TIMESTAMP WHERE worker_id = ?`,
		workerID,
	)
	return err
}

// Deregister removes a worker's heartbeat record.
func (h *HeartbeatMonitor) Deregister(workerID string) error {
	_, err := h.store.DB().Exec(
		`DELETE FROM worker_heartbeats WHERE worker_id = ?`,
		workerID,
	)
	return err
}

// FindStale returns workers whose last heartbeat is older than the threshold.
func (h *HeartbeatMonitor) FindStale(threshold time.Duration) ([]StaleWorker, error) {
	thresholdSeconds := int(threshold.Seconds())
	rows, err := h.store.DB().Query(
		`SELECT worker_id, task_id, run_id, executor, last_heartbeat
		 FROM worker_heartbeats
		 WHERE last_heartbeat < datetime('now', '-' || ? || ' seconds')`,
		thresholdSeconds,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stale []StaleWorker
	for rows.Next() {
		var s StaleWorker
		if err := rows.Scan(&s.WorkerID, &s.TaskID, &s.RunID, &s.Executor, &s.LastHeartbeat); err != nil {
			return nil, err
		}
		stale = append(stale, s)
	}
	return stale, rows.Err()
}

// DeleteStale removes heartbeat rows whose last_heartbeat is older than the
// threshold and returns the number of rows deleted. The scheduler calls this
// after logging stale workers so zombie rows from crashed or force-killed
// serves don't persist across sessions (CW-20260418-0003). A zombie row is
// harmless to dispatch (it does not hold a worker pool slot) but noisy in the
// log, and its presence made past idle-scheduler diagnoses ambiguous.
func (h *HeartbeatMonitor) DeleteStale(threshold time.Duration) (int64, error) {
	thresholdSeconds := int(threshold.Seconds())
	res, err := h.store.DB().Exec(
		`DELETE FROM worker_heartbeats
		 WHERE last_heartbeat < datetime('now', '-' || ? || ' seconds')`,
		thresholdSeconds,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// HeartbeatCounts is the live/stale split of worker_heartbeats against a
// given staleness threshold, used for the per-tick gauge log
// (CW-20260418-0018). A row is counted as stale when last_heartbeat is more
// than threshold in the past OR when last_heartbeat IS NULL (defensive —
// Register writes CURRENT_TIMESTAMP so NULL should never occur in practice,
// but if a future migration or direct INSERT ever produces one, we'd rather
// surface it as stale than silently drop it from both counters).
type HeartbeatCounts struct {
	Live  int
	Stale int
}

// CountHeartbeats returns the live/stale split of worker_heartbeats rows
// against the given staleness threshold. Computed in a single query so the
// two counters are always consistent — splitting across two SELECTs could
// show live+stale != total during a concurrent Register/Deregister.
func (h *HeartbeatMonitor) CountHeartbeats(threshold time.Duration) (HeartbeatCounts, error) {
	thresholdSeconds := int(threshold.Seconds())
	row := h.store.DB().QueryRow(
		`SELECT
			SUM(CASE WHEN last_heartbeat IS NOT NULL AND last_heartbeat >= datetime('now', '-' || ? || ' seconds') THEN 1 ELSE 0 END) AS live,
			SUM(CASE WHEN last_heartbeat IS NULL OR last_heartbeat < datetime('now', '-' || ? || ' seconds') THEN 1 ELSE 0 END) AS stale
		 FROM worker_heartbeats`,
		thresholdSeconds, thresholdSeconds,
	)
	// SUM() over an empty table returns NULL; scan into nullable ints and
	// coalesce to zero so an empty table reports {0,0} rather than a scan
	// error.
	var live, stale sql.NullInt64
	if err := row.Scan(&live, &stale); err != nil {
		return HeartbeatCounts{}, err
	}
	return HeartbeatCounts{
		Live:  int(live.Int64),
		Stale: int(stale.Int64),
	}, nil
}

// ActiveWorkers returns all registered workers.
func (h *HeartbeatMonitor) ActiveWorkers() ([]StaleWorker, error) {
	rows, err := h.store.DB().Query(
		`SELECT worker_id, task_id, run_id, executor, last_heartbeat FROM worker_heartbeats`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []StaleWorker
	for rows.Next() {
		var w StaleWorker
		if err := rows.Scan(&w.WorkerID, &w.TaskID, &w.RunID, &w.Executor, &w.LastHeartbeat); err != nil {
			return nil, err
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

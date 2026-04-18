package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// RunRecord mirrors the runs table row.
type RunRecord struct {
	ID               int64
	TaskID           string
	Executor         string
	Status           string
	StartedAt        time.Time
	EndedAt          sql.NullTime
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	ExitCode         sql.NullInt64
	ErrorMessage     string
	Metadata         sql.NullString
}

// RunCompletion holds the fields written when a run finishes.
type RunCompletion struct {
	Status           string
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	ExitCode         *int
	ErrorMessage     string
}

// TaskRunAggregate is the per-task roll-up of run counts, token usage, and
// cost across every run recorded for the task (any status).
type TaskRunAggregate struct {
	TaskID           string
	Count            int
	PromptTokens     int
	CompletionTokens int
	Cost             float64
}

// GetTaskRunAggregate returns the run count / token / cost roll-up for a task.
// Tasks that have never been executed yield a zero-valued aggregate, not an
// error — callers render this as "0 runs / $0.00 / 0 tokens".
func (s *Store) GetTaskRunAggregate(taskID string) (*TaskRunAggregate, error) {
	const q = `SELECT COUNT(*),
		COALESCE(SUM(prompt_tokens), 0),
		COALESCE(SUM(completion_tokens), 0),
		COALESCE(SUM(cost), 0)
		FROM runs WHERE task_id = ?`
	agg := TaskRunAggregate{TaskID: taskID}
	if err := s.db.QueryRow(q, taskID).Scan(
		&agg.Count, &agg.PromptTokens, &agg.CompletionTokens, &agg.Cost,
	); err != nil {
		return nil, err
	}
	return &agg, nil
}

// CreateRun inserts a new run and returns its auto-assigned ID.
func (s *Store) CreateRun(r *RunRecord) (int64, error) {
	if r.Status == "" {
		r.Status = "running"
	}
	now := time.Now().UTC()
	r.StartedAt = now

	const q = `INSERT INTO runs (task_id, executor, status, started_at, prompt_tokens, completion_tokens, cost, exit_code, error_message, metadata)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	res, err := s.db.Exec(q,
		r.TaskID, r.Executor, r.Status, r.StartedAt,
		r.PromptTokens, r.CompletionTokens, r.Cost,
		r.ExitCode, r.ErrorMessage, r.Metadata,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	r.ID = id
	return id, nil
}

// GetRun fetches a single run by ID.
func (s *Store) GetRun(id int64) (*RunRecord, error) {
	const q = `SELECT id, task_id, executor, status, started_at, ended_at,
		prompt_tokens, completion_tokens, cost, exit_code, error_message, metadata
		FROM runs WHERE id = ?`

	var r RunRecord
	err := s.db.QueryRow(q, id).Scan(
		&r.ID, &r.TaskID, &r.Executor, &r.Status, &r.StartedAt, &r.EndedAt,
		&r.PromptTokens, &r.CompletionTokens, &r.Cost, &r.ExitCode, &r.ErrorMessage, &r.Metadata,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %d not found", id)
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRuns returns all runs for a task, newest first.
func (s *Store) ListRuns(taskID string) ([]RunRecord, error) {
	return s.ListRunsFiltered(RunFilter{TaskID: taskID})
}

// RunFilter parameterizes aggregate queries across runs. Empty fields are
// treated as "no filter"; Limit <= 0 means "no limit" at the store layer
// (HTTP handlers cap this before it gets here).
type RunFilter struct {
	TaskID    string
	ProjectID string
	Statuses  []string
	Since     time.Time
	Limit     int
}

// ListRunsFiltered returns runs matching the filter, newest first
// (ORDER BY started_at DESC). When ProjectID is set, runs are joined
// against tasks to filter on the task's project. All filters combine
// with AND. An empty filter returns every run in the store (bounded
// only by Limit).
func (s *Store) ListRunsFiltered(f RunFilter) ([]RunRecord, error) {
	var (
		where []string
		args  []interface{}
	)

	sel := `SELECT r.id, r.task_id, r.executor, r.status, r.started_at, r.ended_at,
		r.prompt_tokens, r.completion_tokens, r.cost, r.exit_code, r.error_message, r.metadata
		FROM runs r`
	if f.ProjectID != "" {
		sel += ` INNER JOIN tasks t ON t.id = r.task_id`
		where = append(where, "t.project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.TaskID != "" {
		where = append(where, "r.task_id = ?")
		args = append(args, f.TaskID)
	}
	if !f.Since.IsZero() {
		where = append(where, "r.started_at >= ?")
		args = append(args, f.Since.UTC())
	}
	if len(f.Statuses) > 0 {
		placeholders := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			placeholders[i] = "?"
			args = append(args, s)
		}
		where = append(where, "r.status IN ("+strings.Join(placeholders, ",")+")")
	}

	q := sel
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY r.started_at DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []RunRecord
	for rows.Next() {
		var r RunRecord
		if err := rows.Scan(
			&r.ID, &r.TaskID, &r.Executor, &r.Status, &r.StartedAt, &r.EndedAt,
			&r.PromptTokens, &r.CompletionTokens, &r.Cost, &r.ExitCode, &r.ErrorMessage, &r.Metadata,
		); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// Run status constants covering the full taxonomy introduced in
// migration 015. Use these rather than bare string literals so grep
// surfaces every write site and the set stays auditable.
const (
	RunStatusRunning    = "running"
	RunStatusDone       = "done"
	RunStatusFailed     = "failed"
	RunStatusBlocked    = "blocked"
	RunStatusReview     = "review"
	RunStatusCancelled  = "cancelled"
	RunStatusSuperseded = "superseded"
	RunStatusKilled     = "killed"
)

// IsOperatorTerminalRunStatus reports whether a run status was written by
// operator or scheduler housekeeping rather than by the executor finishing
// on its own. Retry paths and on_fail hooks MUST short-circuit on these —
// operator actions are silent by design (CW-20260418-0015).
func IsOperatorTerminalRunStatus(status string) bool {
	switch status {
	case RunStatusCancelled, RunStatusSuperseded, RunStatusKilled:
		return true
	}
	return false
}

// SetRunOperatorStatus stamps an operator/scheduler-driven terminal status
// onto a run row (cancelled/superseded/killed) with the provided reason
// written to error_message and ended_at set to now. Returns an error if
// the status is not in the operator-terminal set or the run does not
// exist. Existing error_message/ended_at are overwritten — these statuses
// are always authored by the operator or scheduler, not the executor.
func (s *Store) SetRunOperatorStatus(id int64, status, reason string) error {
	if !IsOperatorTerminalRunStatus(status) {
		return fmt.Errorf("SetRunOperatorStatus: %q is not an operator-terminal status", status)
	}
	const q = `UPDATE runs SET status = ?, ended_at = ?, error_message = ? WHERE id = ?`
	res, err := s.db.Exec(q, status, time.Now().UTC(), reason, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("run %d not found", id)
	}
	return nil
}

// CompleteRun updates a run's terminal state. If the run has already been
// stamped with an operator-terminal status (cancelled/superseded/killed)
// the write is skipped — those statuses are authoritative and must not be
// overwritten by a late-arriving executor result (CW-20260418-0015).
// Token/cost/exit_code updates are also skipped in that case; the
// operator intentionally took over the run and the truncated metrics
// would be misleading. Returns nil for the skipped case so callers treat
// it as a successful no-op.
func (s *Store) CompleteRun(id int64, c RunCompletion) error {
	var exitCode sql.NullInt64
	if c.ExitCode != nil {
		exitCode = sql.NullInt64{Int64: int64(*c.ExitCode), Valid: true}
	}

	// WHERE-clause guard: only update if the current status is running or
	// already matches a non-operator terminal. Operator-terminal statuses
	// are excluded so the late-arriving executor result cannot clobber them.
	const q = `UPDATE runs SET status = ?, ended_at = ?, prompt_tokens = ?,
		completion_tokens = ?, cost = ?, exit_code = ?, error_message = ?
		WHERE id = ? AND status NOT IN ('cancelled','superseded','killed')`

	res, err := s.db.Exec(q,
		c.Status, time.Now().UTC(),
		c.PromptTokens, c.CompletionTokens, c.Cost,
		exitCode, c.ErrorMessage,
		id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// Distinguish "not found" from "operator-locked". GetRun is cheap
		// and only happens on this cold path.
		if _, gerr := s.GetRun(id); gerr != nil {
			return fmt.Errorf("run %d not found", id)
		}
		// Run exists but is operator-terminal — no-op.
		return nil
	}
	return nil
}

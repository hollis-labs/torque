package sqlstore

import (
	"database/sql"
	"fmt"
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
	const q = `SELECT id, task_id, executor, status, started_at, ended_at,
		prompt_tokens, completion_tokens, cost, exit_code, error_message, metadata
		FROM runs WHERE task_id = ? ORDER BY started_at DESC`

	rows, err := s.db.Query(q, taskID)
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

// CompleteRun updates a run's terminal state.
func (s *Store) CompleteRun(id int64, c RunCompletion) error {
	var exitCode sql.NullInt64
	if c.ExitCode != nil {
		exitCode = sql.NullInt64{Int64: int64(*c.ExitCode), Valid: true}
	}

	const q = `UPDATE runs SET status = ?, ended_at = ?, prompt_tokens = ?,
		completion_tokens = ?, cost = ?, exit_code = ?, error_message = ?
		WHERE id = ?`

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
		return fmt.Errorf("run %d not found", id)
	}
	return nil
}

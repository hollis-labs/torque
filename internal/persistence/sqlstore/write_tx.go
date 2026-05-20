package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/go-sqlite/txutil"
)

// WriteTx is a transaction-scoped helper for serialized runtime state writes.
// Callers may queue multiple logical ops into one SQL transaction and defer
// transition-hook fanout until the transaction commits.
//
// The store's writer handle is opened with _txlock=immediate (via
// sqlitekit.OpenWriter), so BEGIN issued by BeginWriteTx is a BEGIN IMMEDIATE:
// the writer lock is acquired at transaction start, eliminating the
// upgrade-mid-tx → SQLITE_BUSY race. txutil.BeginImmediate is used here as the
// contract marker for that requirement.
//
// txutil.WithImmediate (the closure-shaped helper) is not used because
// WriteTx is intentionally a stateful handle: callers chain multiple
// Exec/QueryRow ops, accumulate afterCommit hooks, and decide whether to
// Commit or Rollback based on state outside the transaction.
type WriteTx struct {
	store       *Store
	tx          *sql.Tx
	afterCommit []func()
}

// BeginWriteTx opens a write transaction against the store's writer handle.
// See WriteTx for the immediate-lock contract.
func (s *Store) BeginWriteTx(ctx context.Context) (*WriteTx, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := txutil.BeginImmediate(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return &WriteTx{store: s, tx: tx}, nil
}

// Commit commits the SQL transaction, then emits any deferred hooks.
func (w *WriteTx) Commit() error {
	if err := w.tx.Commit(); err != nil {
		return err
	}
	for _, fn := range w.afterCommit {
		fn()
	}
	return nil
}

// Rollback aborts the SQL transaction. sql.ErrTxDone is ignored so callers
// can safely defer Rollback before Commit.
func (w *WriteTx) Rollback() error {
	err := w.tx.Rollback()
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

// Exec exposes tx.Exec for writeq savepoint management.
func (w *WriteTx) Exec(query string, args ...any) (sql.Result, error) {
	return w.tx.Exec(query, args...)
}

// QueryRow exposes tx.QueryRow for queue-scoped reads.
func (w *WriteTx) QueryRow(query string, args ...any) *sql.Row {
	return w.tx.QueryRow(query, args...)
}

// MarkAfterCommit snapshots the deferred-hook stack depth.
func (w *WriteTx) MarkAfterCommit() int {
	return len(w.afterCommit)
}

// RewindAfterCommit drops deferred hooks added after mark.
func (w *WriteTx) RewindAfterCommit(mark int) {
	if mark < 0 {
		mark = 0
	}
	if mark > len(w.afterCommit) {
		mark = len(w.afterCommit)
	}
	w.afterCommit = w.afterCommit[:mark]
}

// CreateRun inserts a new run row inside the transaction.
func (w *WriteTx) CreateRun(r *RunRecord) (int64, error) {
	if r.Status == "" {
		r.Status = "running"
	}
	now := time.Now().UTC()
	r.StartedAt = now

	const q = `INSERT INTO runs (task_id, executor, status, started_at, prompt_tokens, completion_tokens, cost, exit_code, error_message, metadata)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	res, err := w.tx.Exec(q,
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

// CompleteRun stamps terminal run fields inside the transaction.
func (w *WriteTx) CompleteRun(id int64, c RunCompletion) error {
	var exitCode sql.NullInt64
	if c.ExitCode != nil {
		exitCode = sql.NullInt64{Int64: int64(*c.ExitCode), Valid: true}
	}

	const q = `UPDATE runs SET status = ?, ended_at = ?, prompt_tokens = ?,
		completion_tokens = ?, cost = ?, exit_code = ?, error_message = ?
		WHERE id = ? AND status NOT IN ('cancelled','superseded','killed')`

	res, err := w.tx.Exec(q,
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
	if n > 0 {
		return nil
	}

	var status string
	if err := w.tx.QueryRow(`SELECT status FROM runs WHERE id = ?`, id).Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("run %d not found", id)
		}
		return err
	}
	return nil
}

// AppendRunEvent inserts a run_events row inside the transaction.
func (w *WriteTx) AppendRunEvent(evt *RunEventRecord) (int64, error) {
	if evt == nil {
		return 0, fmt.Errorf("run event is required")
	}
	if evt.TaskID == "" {
		return 0, fmt.Errorf("task_id is required")
	}
	if evt.Type == "" {
		return 0, fmt.Errorf("type is required")
	}

	const q = `INSERT INTO run_events (run_id, task_id, type, payload) VALUES (?, ?, ?, ?)`
	res, err := w.tx.Exec(q, evt.RunID, evt.TaskID, evt.Type, evt.Payload)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	evt.ID = id
	return id, nil
}

// CreateSession inserts a session row inside the transaction.
func (w *WriteTx) CreateSession(rec *SessionRecord) error {
	if rec.ID == "" {
		return fmt.Errorf("session id is required")
	}
	state := rec.State
	if state == "" {
		state = "launching"
	}
	meta := rec.MetaJSON
	if meta == "" {
		meta = "{}"
	}
	_, err := w.tx.Exec(`
		INSERT INTO sessions (
			id, agent_profile, provider, runtime_id, runtime_kind,
			workdir, project_id, task_id, state, pid, meta
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.AgentProfile, rec.Provider, rec.RuntimeID, rec.RuntimeKind,
		rec.Workdir, rec.ProjectID, rec.TaskID, state, rec.PID, meta,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	rec.State = state
	rec.MetaJSON = meta
	return nil
}

// UpdateSessionState mutates lifecycle columns inside the transaction.
func (w *WriteTx) UpdateSessionState(id, state string, pid int, exit *int) error {
	if state == "" {
		return fmt.Errorf("state is required")
	}
	now := time.Now().UTC()
	var endedAt sql.NullTime
	switch state {
	case "done", "failed", "crashed":
		endedAt = sql.NullTime{Time: now, Valid: true}
	}
	var exitArg sql.NullInt64
	if exit != nil {
		exitArg = sql.NullInt64{Int64: int64(*exit), Valid: true}
	}
	var (
		res sql.Result
		err error
	)
	if pid > 0 {
		res, err = w.tx.Exec(`
			UPDATE sessions
			SET state = ?, pid = ?, exit_code = COALESCE(?, exit_code),
			    updated_at = ?, last_activity = ?,
			    ended_at = COALESCE(?, ended_at)
			WHERE id = ?`,
			state, pid, exitArg, now, now, endedAt, id)
	} else {
		res, err = w.tx.Exec(`
			UPDATE sessions
			SET state = ?, exit_code = COALESCE(?, exit_code),
			    updated_at = ?, last_activity = ?,
			    ended_at = COALESCE(?, ended_at)
			WHERE id = ?`,
			state, exitArg, now, now, endedAt, id)
	}
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// TouchSession bumps session liveness fields inside the transaction.
func (w *WriteTx) TouchSession(id string) error {
	now := time.Now().UTC()
	res, err := w.tx.Exec(`UPDATE sessions SET last_activity = ?, updated_at = ? WHERE id = ?`,
		now, now, id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// UpdateSessionResumeHint persists the latest checkpoint hint.
func (w *WriteTx) UpdateSessionResumeHint(id string, hint []byte) error {
	res, err := w.tx.Exec(`UPDATE sessions SET resume_hint = ?, updated_at = ? WHERE id = ?`,
		hint, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update session resume hint: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// UpdateSessionMeta overwrites the meta column inside the transaction.
// Mirrors Store.UpdateSessionMeta for the StateWriter path.
func (w *WriteTx) UpdateSessionMeta(id, metaJSON string) error {
	res, err := w.tx.Exec(`UPDATE sessions SET meta = ?, updated_at = ? WHERE id = ?`,
		metaJSON, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update session meta: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// TransitionTask writes a status transition and defers hook fanout until commit.
func (w *WriteTx) TransitionTask(id, newStatus string) error {
	oldStatus, err := w.transitionTask(id, newStatus, nil)
	if err != nil {
		return err
	}
	w.afterCommit = append(w.afterCommit, func() {
		w.store.emitTaskTransition(TaskTransitionEvent{
			TaskID:    id,
			OldStatus: oldStatus,
			NewStatus: newStatus,
		})
	})
	return nil
}

// TransitionTaskWithReason writes a status transition with blocked_reason.
func (w *WriteTx) TransitionTaskWithReason(id, newStatus, reason string) error {
	r := reason
	oldStatus, err := w.transitionTask(id, newStatus, &r)
	if err != nil {
		return err
	}
	w.afterCommit = append(w.afterCommit, func() {
		w.store.emitTaskTransition(TaskTransitionEvent{
			TaskID:    id,
			OldStatus: oldStatus,
			NewStatus: newStatus,
			Reason:    reason,
		})
	})
	return nil
}

func (w *WriteTx) transitionTask(id, newStatus string, reason *string) (string, error) {
	var oldStatus string
	if err := w.tx.QueryRow(`SELECT status FROM tasks WHERE id = ?`, id).Scan(&oldStatus); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("task %s not found", id)
		}
		return "", err
	}

	var (
		res sql.Result
		err error
	)
	if reason != nil {
		res, err = w.tx.Exec(
			`UPDATE tasks SET status = ?, blocked_reason = ?, updated_at = ? WHERE id = ?`,
			newStatus, *reason, time.Now().UTC(), id,
		)
	} else {
		res, err = w.tx.Exec(
			`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`,
			newStatus, time.Now().UTC(), id,
		)
	}
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", fmt.Errorf("task %s not found", id)
	}
	return oldStatus, nil
}

// GetTaskRetryCount reads retry_count under the write transaction.
func (w *WriteTx) GetTaskRetryCount(id string) (int, error) {
	var retryCount int
	if err := w.tx.QueryRow(`SELECT retry_count FROM tasks WHERE id = ?`, id).Scan(&retryCount); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("task %s not found", id)
		}
		return 0, err
	}
	return retryCount, nil
}

// IncrementTaskRetryCount bumps retry_count by one.
func (w *WriteTx) IncrementTaskRetryCount(id string) error {
	res, err := w.tx.Exec(`UPDATE tasks SET retry_count = retry_count + 1, updated_at = ? WHERE id = ?`, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("task %s not found", id)
	}
	return nil
}

// GetTaskEscalationStep reads escalation_step under the write transaction.
func (w *WriteTx) GetTaskEscalationStep(id string) (int, error) {
	var step int
	if err := w.tx.QueryRow(`SELECT escalation_step FROM tasks WHERE id = ?`, id).Scan(&step); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("task %s not found", id)
		}
		return 0, err
	}
	return step, nil
}

// SetTaskEscalationStep updates escalation_step.
func (w *WriteTx) SetTaskEscalationStep(id string, step int) error {
	res, err := w.tx.Exec(`UPDATE tasks SET escalation_step = ?, updated_at = ? WHERE id = ?`, step, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("task %s not found", id)
	}
	return nil
}

// SetTaskAgentProfile updates agent_profile for the task.
func (w *WriteTx) SetTaskAgentProfile(id, agentProfile string) error {
	res, err := w.tx.Exec(`UPDATE tasks SET agent_profile = ?, updated_at = ? WHERE id = ?`, agentProfile, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("task %s not found", id)
	}
	return nil
}

// NextTaskID allocates the next sequential task ID inside the write
// transaction. Unlike Store.NextTaskID — which runs the lookup on a
// connection that is released before the caller's subsequent INSERT — this
// runs under the transaction's held writer lock. When paired with CreateTask
// in the same WriteTx, the SELECT and the INSERT are atomic against every
// other writer (writeq-serialized or direct), so two concurrent allocations
// can never collide on the same ID.
//
// The suffix lookup CASTs the numeric tail to INTEGER so MAX tracks the true
// highest sequence regardless of suffix width. A plain MAX(id) would be a
// lexicographic max over the formatted id, which only sorts correctly while
// every suffix is the same width — once any day's count crosses 9999, the
// 4-digit pad expands and string "9999" sorts greater than "10000", so MAX
// would freeze at the highest 4-digit row and re-allocate it on every call
// (UNIQUE-constraint failures on every insert).
func (w *WriteTx) NextTaskID() (string, error) {
	prefix := "CW-" + time.Now().UTC().Format("20060102") + "-"

	var maxSeq sql.NullInt64
	if err := w.tx.QueryRow(
		`SELECT MAX(CAST(substr(id, ?) AS INTEGER)) FROM tasks WHERE id LIKE ?`,
		len(prefix)+1, prefix+"%",
	).Scan(&maxSeq); err != nil {
		return "", err
	}

	seq := int64(1)
	if maxSeq.Valid {
		seq = maxSeq.Int64 + 1
	}
	return fmt.Sprintf("%s%04d", prefix, seq), nil
}

// CreateTask inserts a task row inside the write transaction. It mirrors
// Store.CreateTask field-for-field (including applyDefaults, which rewrites
// MaxRetries==0 to 3) so behavior is identical regardless of which path
// creates the task. The only difference is that the INSERT runs under the
// transaction's held writer lock — callers that allocate the ID with
// NextTaskID in the same WriteTx get a collision-free create.
func (w *WriteTx) CreateTask(t *TaskRecord) error {
	applyDefaults(t)
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now

	var manual int
	if t.Manual {
		manual = 1
	}

	const q = `INSERT INTO tasks (
		id, title, description, status, priority, manual,
		executor, agent_profile, working_dir, tools, permissions, environment,
		system_prompt, agent_file, files, cost_budget, max_retries, max_duration_ms, token_budget,
		on_done, on_fail, on_review, escalation_chain, quality_gates, deliverables,
		deliverable_preset, on_done_merge, depends_on, blocked_reason, metadata,
		sprint_id, project_id, epic_id,
		kind, source_type, source_ref, trust, checkpoint_mode, on_checkpoint_response,
		parent_id
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

	_, err := w.tx.Exec(q,
		t.ID, t.Title, t.Description, t.Status, t.Priority, manual,
		t.Executor, t.AgentProfile, t.WorkingDir, t.Tools, t.Permissions, t.Environment,
		t.SystemPrompt, t.AgentFile, t.Files, t.CostBudget, t.MaxRetries, t.MaxDurationMs, t.TokenBudget,
		t.OnDone, t.OnFail, t.OnReview, t.EscalationChain, t.QualityGates, t.Deliverables,
		t.DeliverablePreset, t.OnDoneMerge, t.DependsOn, t.BlockedReason, t.Metadata,
		t.SprintID, t.ProjectID, t.EpicID,
		t.Kind, t.SourceType, t.SourceRef, t.Trust, t.CheckpointMode, t.OnCheckpointResponse,
		t.ParentID,
	)
	return err
}

// SetTaskMaxRetries overwrites max_retries inside the write transaction. It
// is the pointer-to-zero counterpart to the MaxRetries==0→3 rewrite inside
// applyDefaults: callers that genuinely want a zero-retry task call this
// after CreateTask in the same transaction.
func (w *WriteTx) SetTaskMaxRetries(id string, maxRetries int) error {
	res, err := w.tx.Exec(`UPDATE tasks SET max_retries = ?, updated_at = ? WHERE id = ?`, maxRetries, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("task %s not found", id)
	}
	return nil
}

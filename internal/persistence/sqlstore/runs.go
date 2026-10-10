package sqlstore

import (
	"database/sql"
	"fmt"
	"time"
)

// RunRecord mirrors the runs table row.
type RunRecord struct {
	QuerySortValue   string `json:"-"`
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
	// CacheReadTokens / CacheWriteTokens are the run's prompt-cache totals,
	// and CostSource where Cost came from: provider, estimate, mixed or
	// none ('' for runs completed before migration 034). CW-20260912-0003.
	CacheReadTokens  int
	CacheWriteTokens int
	CostSource       string
	ProfileSnapshot  sql.NullString
}

// RunCompletion holds the fields written when a run finishes.
type RunCompletion struct {
	Status           string
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	CacheWriteTokens int
	Cost             float64
	CostSource       string
	ExitCode         *int
	ErrorMessage     string
	// OnlyIfRunning limits the write to a run that is still `running`. A
	// path that reclaims a run it believes is orphaned sets it, so a run that
	// finished between its check and its write keeps its status, tokens and
	// cost instead of being reset (CW-20260912-0003).
	OnlyIfRunning bool
}

// TaskRunAggregate is the per-task roll-up of run counts, token usage, and
// cost across every run recorded for the task (any status).
//
// Cost is sourced from cost_ledger (the canonical cost store per migration
// 017), NOT from runs.cost — the runs.cost column receives the executor's
// reported figure which is always 0 under subscription billing where Claude
// strips total_cost_usd. cost_ledger holds executor-reported AND models.dev-
// backfilled estimates and tags each row with cost_source so the GUI can
// render a "measured" vs "estimated" badge.
//
// CostSource is the highest-priority source across the task's ledger rows
// (`measured` > `estimated` > `unknown`); empty string when no rows exist.
type TaskRunAggregate struct {
	TaskID           string
	Count            int
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	CostSource       string
	ProfileSnapshot  sql.NullString
}

// GetTaskRunAggregate returns the run count / token / cost roll-up for a task.
// Tasks that have never been executed yield a zero-valued aggregate (Count=0,
// Cost=0, CostSource=""), not an error. Callers distinguish "never ran" from
// "ran with no measured cost" via CostSource: empty string → render cost as
// "—" (no ledger rows); "measured"/"estimated" → render the dollar figure
// (with `~` prefix for estimates) per apps/gui/src/lib/utils.ts:formatCost.
//
// Implementation: two queries — tokens/count from `runs` (canonical for
// turn-count + token totals), cost from `cost_ledger` (canonical for cost
// per migration 017). Splitting the query is cleaner than a JOIN because
// cost_ledger may have multiple rows per run in pathological cases and a
// JOIN would multiply the token sums; the FK-constrained ledger has at
// most one row per run today but the split future-proofs against either
// table growing additional per-run rows.
func (s *Store) GetTaskRunAggregate(taskID string) (*TaskRunAggregate, error) {
	const runsQ = `SELECT COUNT(*),
		COALESCE(SUM(prompt_tokens), 0),
		COALESCE(SUM(completion_tokens), 0)
		FROM runs WHERE task_id = ?`
	agg := TaskRunAggregate{TaskID: taskID}
	if err := s.ReadDB().QueryRow(runsQ, taskID).Scan(
		&agg.Count, &agg.PromptTokens, &agg.CompletionTokens,
	); err != nil {
		return nil, err
	}

	// cost_ledger lookup. Aggregates SUM(cost) and picks the highest-
	// priority cost_source via MIN() over a CASE — `provider` (and the
	// pre-034 `executor`) is measured, rank 1; `estimate` and `mixed` (and
	// the pre-034 `models_dev`) are estimated, rank 2; `none`/`unknown` is
	// rank 3, so
	// MIN selects the most authoritative source seen across the task's
	// ledger rows. NULL CostSource (no rows) leaves the field empty so the
	// GUI can decide between "—" (no data) and "$0.00" (measured-and-zero).
	const ledgerQ = `SELECT COALESCE(SUM(cost), 0),
		MIN(CASE cost_source
			WHEN 'executor' THEN 1
			WHEN 'provider' THEN 1
			WHEN 'models_dev' THEN 2
			WHEN 'estimate' THEN 2
			WHEN 'mixed' THEN 2
			ELSE 3
		END)
		FROM cost_ledger WHERE task_id = ?`
	var rank sql.NullInt64
	if err := s.ReadDB().QueryRow(ledgerQ, taskID).Scan(&agg.Cost, &rank); err != nil {
		return nil, err
	}
	if rank.Valid {
		switch rank.Int64 {
		case 1:
			agg.CostSource = "measured"
		case 2:
			agg.CostSource = "estimated"
		default:
			agg.CostSource = "unknown"
		}
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

	const q = `INSERT INTO runs (task_id, executor, status, started_at, prompt_tokens, completion_tokens, cost, exit_code, error_message, metadata, profile_snapshot)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	res, err := s.db.Exec(q,
		r.TaskID, r.Executor, r.Status, r.StartedAt,
		r.PromptTokens, r.CompletionTokens, r.Cost,
		r.ExitCode, r.ErrorMessage, r.Metadata, r.ProfileSnapshot,
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
		prompt_tokens, completion_tokens, cost, exit_code, error_message, metadata,
		cache_read_tokens, cache_write_tokens, cost_source, profile_snapshot
		FROM runs WHERE id = ?`

	var r RunRecord
	err := s.ReadDB().QueryRow(q, id).Scan(
		&r.ID, &r.TaskID, &r.Executor, &r.Status, &r.StartedAt, &r.EndedAt,
		&r.PromptTokens, &r.CompletionTokens, &r.Cost, &r.ExitCode, &r.ErrorMessage, &r.Metadata,
		&r.CacheReadTokens, &r.CacheWriteTokens, &r.CostSource, &r.ProfileSnapshot,
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
	TaskID         string
	ProjectID      string
	SprintID       string
	EpicID         string
	Executors      []string
	Profiles       []string
	Statuses       []string
	Since          time.Time
	Until          time.Time
	Limit          int
	Offset         int
	SortBy         string
	SortDir        string
	AfterSortValue string
	AfterID        int64
}

// ListRunsFiltered also serves internal lifecycle callers; a zero limit is
// unbounded there. Public list surfaces apply the shared page policy in service.
func (s *Store) ListRunsFiltered(f RunFilter) ([]RunRecord, error) {
	return s.queryRuns(s.ReadDB(), f)
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
		completion_tokens = ?, cost = ?, exit_code = ?, error_message = ?,
		cache_read_tokens = ?, cache_write_tokens = ?, cost_source = ?
		WHERE id = ? AND status NOT IN ('cancelled','superseded','killed')`

	res, err := s.db.Exec(q,
		c.Status, time.Now().UTC(),
		c.PromptTokens, c.CompletionTokens, c.Cost,
		exitCode, c.ErrorMessage,
		c.CacheReadTokens, c.CacheWriteTokens, c.CostSource,
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

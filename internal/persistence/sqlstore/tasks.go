package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// taskIDRange returns the half-open range [lo, hi) covering all task IDs that
// start with the given prefix. The prefix is expected to end in '-' (0x2D);
// hi replaces that trailing '-' with '.' (0x2E) so the range upper bound
// excludes any longer id starting with the prefix. This lets NextTaskID issue
// `WHERE id >= ? AND id < ?` queries that SQLite can satisfy with a range
// scan on the PRIMARY KEY index, with early exit when paired with MAX(id).
func taskIDRange(prefix string) (lo, hi string) {
	if prefix == "" {
		return "", ""
	}
	// Bump the final byte by 1. For our case the final byte is always '-'
	// (0x2D), so hi ends in '.' (0x2E). Done generically to keep the
	// helper safe if callers ever pass a different terminator.
	b := []byte(prefix)
	b[len(b)-1]++
	return prefix, string(b)
}

// ErrTaskNotFound is returned wrapped by GetTask when the task ID does not
// exist. Callers should use errors.Is(err, ErrTaskNotFound) to distinguish
// missing tasks from other storage errors.
var ErrTaskNotFound = errors.New("task not found")

// TaskRecord mirrors the tasks table row.
type TaskRecord struct {
	ID                string
	Title             string
	Description       string
	Status            string
	Priority          int
	Manual            bool
	Executor          string
	LaunchProfile     string
	AgentProfile      string
	WorkingDir        string
	Tools             sql.NullString
	Permissions       sql.NullString
	Environment       sql.NullString
	SystemPrompt      string
	AgentFile         string
	Files             sql.NullString
	CostBudget        sql.NullFloat64
	MaxRetries        int
	MaxDurationMs     sql.NullInt64
	TokenBudget       sql.NullInt64
	OnDone            string
	OnFail            string
	OnReview          string
	EscalationChain   sql.NullString
	QualityGates      sql.NullString
	Deliverables      sql.NullString
	DeliverablePreset string
	OnDoneMerge       string
	BlockedReason     string
	Metadata          sql.NullString
	SprintID          sql.NullString
	ProjectID         sql.NullString
	EpicID            sql.NullString
	CreatedAt         time.Time
	UpdatedAt         time.Time

	// Facet columns (migration 007)
	Kind                 string
	SourceType           string
	SourceRef            sql.NullString
	Trust                string
	CheckpointMode       string
	OnCheckpointResponse string

	// Parent linkage (migration 013). NULL = top of lineage.
	ParentID sql.NullString

	// Collection columns (migration 020). All three are managed by the
	// Collections store (collections.go) — task-layer code reads these but
	// does not write them through TaskUpdate. CollectionID NULL means the
	// task is not in a collection (could still be in inbox via
	// AddedToCollectionsAt). AddedToCollectionsAt is write-once on first
	// entry into the collections world.
	CollectionID         sql.NullString
	CollectionPosition   sql.NullInt64
	AddedToCollectionsAt sql.NullTime

	// depends_on (migration 027 / FK-003) lives in the task_dependencies
	// join table, not a column here — same shape as tags (migration 005).
	// See SetTaskDependencies / ListTaskDependencyIDs in
	// task_dependencies.go.
}

// TaskFilter holds optional filter criteria for ListTasks.
type TaskFilter struct {
	Status    string   // single status (legacy)
	Statuses  []string // multiple statuses (OR filter)
	Priority  int
	SprintID  string
	ProjectID string
	EpicID    string
	Executor  string
	TagSlugs  []string // AND-match: task must have all listed tags
	Search    string   // case-insensitive substring match on id, title, or description
	Limit     int
	Offset    int

	// Facet filters (migration 007)
	Kind           string
	SourceType     string
	SourceRef      string
	Trust          string
	CheckpointMode string

	// Parent linkage (migration 013). When set, returns tasks whose parent_id
	// matches. Use ParentIDNull=true to return root tasks (parent_id IS NULL).
	ParentID     string
	ParentIDNull bool

	// Manual-flag filter. Nil = no filter; otherwise matches manual=0/1.
	Manual *bool

	// ExcludeInternal, when true, suppresses kind='internal' rows from
	// the result. Default zero-value (false) preserves prior behavior:
	// no exclusion. Internal-call sites (picker, scheduler internals)
	// leave it false so they continue to see all kinds; user-facing
	// boundaries (HTTP /api/v1/tasks, MCP torque_task_list /
	// torque_task_search) flip it to true unless the caller passes
	// include_internal=true (CW-20260503-0011, S1.1). When the caller
	// supplies an explicit Kind filter, that exact-match takes precedence
	// over the exclusion.
	ExcludeInternal bool
}

// TaskUpdate holds optional fields to update; nil pointer = no change.
type TaskUpdate struct {
	Title             *string
	Description       *string
	Status            *string
	Priority          *int
	Manual            *bool
	Executor          *string
	LaunchProfile     *string
	AgentProfile      *string
	WorkingDir        *string
	Tools             *sql.NullString
	Permissions       *sql.NullString
	Environment       *sql.NullString
	SystemPrompt      *string
	AgentFile         *string
	Files             *sql.NullString
	CostBudget        *sql.NullFloat64
	MaxRetries        *int
	MaxDurationMs     *sql.NullInt64
	TokenBudget       *sql.NullInt64
	OnDone            *string
	OnFail            *string
	OnReview          *string
	EscalationChain   *sql.NullString
	QualityGates      *sql.NullString
	Deliverables      *sql.NullString
	DeliverablePreset *string
	OnDoneMerge       *string
	BlockedReason     *string
	Metadata          *sql.NullString
	SprintID          *sql.NullString
	ProjectID         *sql.NullString
	EpicID            *sql.NullString

	// Facet fields (migration 007)
	Kind                 *string
	SourceType           *string
	SourceRef            *sql.NullString
	Trust                *string
	CheckpointMode       *string
	OnCheckpointResponse *string

	// Parent linkage (migration 013). Non-nil pointer writes the column;
	// use a NullString with Valid=false to clear (set to NULL).
	ParentID *sql.NullString

	// depends_on (migration 027 / FK-003) is not a column here — it's
	// managed via SetTaskDependencies (task_dependencies.go), same shape as
	// Tags in service.TaskUpdateInput.
}

// applyDefaults fills zero-value fields with domain defaults.
func applyDefaults(t *TaskRecord) {
	if t.Status == "" {
		t.Status = "todo"
	}
	if t.OnDone == "" {
		t.OnDone = "review"
	}
	if t.OnFail == "" {
		t.OnFail = "retry"
	}
	if t.OnReview == "" {
		t.OnReview = "pause"
	}
	if t.OnDoneMerge == "" {
		t.OnDoneMerge = "none"
	}
	if t.MaxRetries == 0 {
		t.MaxRetries = 3
	}
	if t.Kind == "" {
		t.Kind = "agent"
	}
	if t.SourceType == "" {
		t.SourceType = "user"
	}
	if t.Trust == "" {
		t.Trust = "normal"
	}
	if t.CheckpointMode == "" {
		t.CheckpointMode = "none"
	}
	if t.OnCheckpointResponse == "" {
		t.OnCheckpointResponse = "resume"
	}
}

// The 44-column SELECT list used by GetTask, ListTasks, and SearchTasks.
const taskSelectCols = `id, title, description, status, priority, manual,
	executor, launch_profile, agent_profile, working_dir, tools, permissions, environment,
	system_prompt, agent_file, files, cost_budget, max_retries, max_duration_ms, token_budget,
	on_done, on_fail, on_review, escalation_chain, quality_gates, deliverables,
	deliverable_preset, on_done_merge, blocked_reason, metadata,
	sprint_id, project_id, epic_id, created_at, updated_at,
	kind, source_type, source_ref, trust, checkpoint_mode, on_checkpoint_response,
	parent_id,
	collection_id, collection_position, added_to_collections_at`

// scanTask scans a single row into a TaskRecord.
func scanTask(row interface {
	Scan(...any) error
}) (*TaskRecord, error) {
	var t TaskRecord
	var manual int
	err := row.Scan(
		&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &manual,
		&t.Executor, &t.LaunchProfile, &t.AgentProfile, &t.WorkingDir, &t.Tools, &t.Permissions, &t.Environment,
		&t.SystemPrompt, &t.AgentFile, &t.Files, &t.CostBudget, &t.MaxRetries, &t.MaxDurationMs, &t.TokenBudget,
		&t.OnDone, &t.OnFail, &t.OnReview, &t.EscalationChain, &t.QualityGates, &t.Deliverables,
		&t.DeliverablePreset, &t.OnDoneMerge, &t.BlockedReason, &t.Metadata,
		&t.SprintID, &t.ProjectID, &t.EpicID, &t.CreatedAt, &t.UpdatedAt,
		&t.Kind, &t.SourceType, &t.SourceRef, &t.Trust, &t.CheckpointMode, &t.OnCheckpointResponse,
		&t.ParentID,
		&t.CollectionID, &t.CollectionPosition, &t.AddedToCollectionsAt,
	)
	if err != nil {
		return nil, err
	}
	t.Manual = manual != 0
	return &t, nil
}

// CreateTask inserts a new task with defaults applied.
func (s *Store) CreateTask(t *TaskRecord) error {
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
		executor, launch_profile, agent_profile, working_dir, tools, permissions, environment,
		system_prompt, agent_file, files, cost_budget, max_retries, max_duration_ms, token_budget,
		on_done, on_fail, on_review, escalation_chain, quality_gates, deliverables,
		deliverable_preset, on_done_merge, blocked_reason, metadata,
		sprint_id, project_id, epic_id,
		kind, source_type, source_ref, trust, checkpoint_mode, on_checkpoint_response,
		parent_id
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

	_, err := s.db.Exec(q,
		t.ID, t.Title, t.Description, t.Status, t.Priority, manual,
		t.Executor, t.LaunchProfile, t.AgentProfile, t.WorkingDir, t.Tools, t.Permissions, t.Environment,
		t.SystemPrompt, t.AgentFile, t.Files, t.CostBudget, t.MaxRetries, t.MaxDurationMs, t.TokenBudget,
		t.OnDone, t.OnFail, t.OnReview, t.EscalationChain, t.QualityGates, t.Deliverables,
		t.DeliverablePreset, t.OnDoneMerge, t.BlockedReason, t.Metadata,
		t.SprintID, t.ProjectID, t.EpicID,
		t.Kind, t.SourceType, t.SourceRef, t.Trust, t.CheckpointMode, t.OnCheckpointResponse,
		t.ParentID,
	)
	return err
}

// GetTask fetches a single task by ID. Returns an error wrapping
// ErrTaskNotFound if no row matches; callers can use errors.Is to detect.
func (s *Store) GetTask(id string) (*TaskRecord, error) {
	q := `SELECT ` + taskSelectCols + ` FROM tasks WHERE id = ?`
	row := s.ReadDB().QueryRow(q, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task %s: %w", id, ErrTaskNotFound)
	}
	return t, err
}

// ListTasks returns tasks matching the filter, ordered by priority ASC, created_at ASC.
func (s *Store) ListTasks(f TaskFilter) ([]TaskRecord, error) {
	var where []string
	var args []any

	if len(f.Statuses) > 0 {
		placeholders := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			placeholders[i] = "?"
			args = append(args, s)
		}
		where = append(where, "status IN ("+strings.Join(placeholders, ",")+")")
	} else if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Priority != 0 {
		where = append(where, "priority = ?")
		args = append(args, f.Priority)
	}
	if f.SprintID != "" {
		where = append(where, "sprint_id = ?")
		args = append(args, f.SprintID)
	}
	if f.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.EpicID != "" {
		where = append(where, "epic_id = ?")
		args = append(args, f.EpicID)
	}
	if f.Executor != "" {
		where = append(where, "executor = ?")
		args = append(args, f.Executor)
	}
	if f.Kind != "" {
		// Explicit kind filter wins; exclusion is a no-op when an exact
		// match is requested.
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	} else if f.ExcludeInternal {
		where = append(where, "kind != 'internal'")
	}
	if f.SourceType != "" {
		where = append(where, "source_type = ?")
		args = append(args, f.SourceType)
	}
	if f.SourceRef != "" {
		where = append(where, "source_ref = ?")
		args = append(args, f.SourceRef)
	}
	if f.Trust != "" {
		where = append(where, "trust = ?")
		args = append(args, f.Trust)
	}
	if f.CheckpointMode != "" {
		where = append(where, "checkpoint_mode = ?")
		args = append(args, f.CheckpointMode)
	}
	if f.ParentIDNull {
		where = append(where, "parent_id IS NULL")
	} else if f.ParentID != "" {
		where = append(where, "parent_id = ?")
		args = append(args, f.ParentID)
	}
	if f.Manual != nil {
		v := 0
		if *f.Manual {
			v = 1
		}
		where = append(where, "manual = ?")
		args = append(args, v)
	}
	if len(f.TagSlugs) > 0 {
		placeholders := make([]string, len(f.TagSlugs))
		for i, slug := range f.TagSlugs {
			placeholders[i] = "?"
			args = append(args, slug)
		}
		args = append(args, len(f.TagSlugs))
		where = append(where, fmt.Sprintf(
			"id IN (SELECT task_id FROM task_tags WHERE tag_slug IN (%s) GROUP BY task_id HAVING COUNT(DISTINCT tag_slug) = ?)",
			strings.Join(placeholders, ","),
		))
	}
	if f.Search != "" {
		// SQLite's LIKE is case-insensitive for ASCII by default.
		pattern := "%" + f.Search + "%"
		where = append(where, "(id LIKE ? OR title LIKE ? OR description LIKE ?)")
		args = append(args, pattern, pattern, pattern)
	}

	q := `SELECT ` + taskSelectCols + ` FROM tasks`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY priority ASC, created_at ASC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	if f.Offset > 0 {
		q += fmt.Sprintf(" OFFSET %d", f.Offset)
	}

	rows, err := s.ReadDB().Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []TaskRecord
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

// UpdateTask applies non-nil pointer fields to the task row.
func (s *Store) UpdateTask(id string, u TaskUpdate) error {
	var setClauses []string
	var args []any

	if u.Title != nil {
		setClauses = append(setClauses, "title = ?")
		args = append(args, *u.Title)
	}
	if u.Description != nil {
		setClauses = append(setClauses, "description = ?")
		args = append(args, *u.Description)
	}
	if u.Status != nil {
		setClauses = append(setClauses, "status = ?")
		args = append(args, *u.Status)
	}
	if u.Priority != nil {
		setClauses = append(setClauses, "priority = ?")
		args = append(args, *u.Priority)
	}
	if u.Manual != nil {
		v := 0
		if *u.Manual {
			v = 1
		}
		setClauses = append(setClauses, "manual = ?")
		args = append(args, v)
	}
	if u.Executor != nil {
		setClauses = append(setClauses, "executor = ?")
		args = append(args, *u.Executor)
	}
	if u.LaunchProfile != nil {
		setClauses = append(setClauses, "launch_profile = ?")
		args = append(args, *u.LaunchProfile)
	}
	if u.AgentProfile != nil {
		setClauses = append(setClauses, "agent_profile = ?")
		args = append(args, *u.AgentProfile)
	}
	if u.WorkingDir != nil {
		setClauses = append(setClauses, "working_dir = ?")
		args = append(args, *u.WorkingDir)
	}
	if u.Tools != nil {
		setClauses = append(setClauses, "tools = ?")
		args = append(args, *u.Tools)
	}
	if u.Permissions != nil {
		setClauses = append(setClauses, "permissions = ?")
		args = append(args, *u.Permissions)
	}
	if u.Environment != nil {
		setClauses = append(setClauses, "environment = ?")
		args = append(args, *u.Environment)
	}
	if u.SystemPrompt != nil {
		setClauses = append(setClauses, "system_prompt = ?")
		args = append(args, *u.SystemPrompt)
	}
	if u.AgentFile != nil {
		setClauses = append(setClauses, "agent_file = ?")
		args = append(args, *u.AgentFile)
	}
	if u.Files != nil {
		setClauses = append(setClauses, "files = ?")
		args = append(args, *u.Files)
	}
	if u.CostBudget != nil {
		setClauses = append(setClauses, "cost_budget = ?")
		args = append(args, *u.CostBudget)
	}
	if u.MaxRetries != nil {
		setClauses = append(setClauses, "max_retries = ?")
		args = append(args, *u.MaxRetries)
	}
	if u.MaxDurationMs != nil {
		setClauses = append(setClauses, "max_duration_ms = ?")
		args = append(args, *u.MaxDurationMs)
	}
	if u.TokenBudget != nil {
		setClauses = append(setClauses, "token_budget = ?")
		args = append(args, *u.TokenBudget)
	}
	if u.OnDone != nil {
		setClauses = append(setClauses, "on_done = ?")
		args = append(args, *u.OnDone)
	}
	if u.OnFail != nil {
		setClauses = append(setClauses, "on_fail = ?")
		args = append(args, *u.OnFail)
	}
	if u.OnReview != nil {
		setClauses = append(setClauses, "on_review = ?")
		args = append(args, *u.OnReview)
	}
	if u.EscalationChain != nil {
		setClauses = append(setClauses, "escalation_chain = ?")
		args = append(args, *u.EscalationChain)
	}
	if u.QualityGates != nil {
		setClauses = append(setClauses, "quality_gates = ?")
		args = append(args, *u.QualityGates)
	}
	if u.Deliverables != nil {
		setClauses = append(setClauses, "deliverables = ?")
		args = append(args, *u.Deliverables)
	}
	if u.DeliverablePreset != nil {
		setClauses = append(setClauses, "deliverable_preset = ?")
		args = append(args, *u.DeliverablePreset)
	}
	if u.OnDoneMerge != nil {
		setClauses = append(setClauses, "on_done_merge = ?")
		args = append(args, *u.OnDoneMerge)
	}
	if u.BlockedReason != nil {
		setClauses = append(setClauses, "blocked_reason = ?")
		args = append(args, *u.BlockedReason)
	}
	if u.Metadata != nil {
		setClauses = append(setClauses, "metadata = ?")
		args = append(args, *u.Metadata)
	}
	if u.SprintID != nil {
		setClauses = append(setClauses, "sprint_id = ?")
		args = append(args, *u.SprintID)
	}
	if u.ProjectID != nil {
		setClauses = append(setClauses, "project_id = ?")
		args = append(args, *u.ProjectID)
	}
	if u.EpicID != nil {
		setClauses = append(setClauses, "epic_id = ?")
		args = append(args, *u.EpicID)
	}
	if u.Kind != nil {
		setClauses = append(setClauses, "kind = ?")
		args = append(args, *u.Kind)
	}
	if u.SourceType != nil {
		setClauses = append(setClauses, "source_type = ?")
		args = append(args, *u.SourceType)
	}
	if u.SourceRef != nil {
		setClauses = append(setClauses, "source_ref = ?")
		args = append(args, *u.SourceRef)
	}
	if u.Trust != nil {
		setClauses = append(setClauses, "trust = ?")
		args = append(args, *u.Trust)
	}
	if u.CheckpointMode != nil {
		setClauses = append(setClauses, "checkpoint_mode = ?")
		args = append(args, *u.CheckpointMode)
	}
	if u.OnCheckpointResponse != nil {
		setClauses = append(setClauses, "on_checkpoint_response = ?")
		args = append(args, *u.OnCheckpointResponse)
	}
	if u.ParentID != nil {
		setClauses = append(setClauses, "parent_id = ?")
		args = append(args, *u.ParentID)
	}

	// Always update updated_at
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, time.Now().UTC())
	args = append(args, id)

	q := `UPDATE tasks SET ` + strings.Join(setClauses, ", ") + ` WHERE id = ?`
	res, err := s.db.Exec(q, args...)
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

// TransitionTask sets a new status on the task. After a successful UPDATE,
// any task-transition hooks registered via RegisterTaskTransitionHook are
// invoked with the old/new status. The old status is read in the same
// transaction as the UPDATE so the hook payload always reflects the actual
// DB transition and no intermediate write can slip between read and update.
func (s *Store) TransitionTask(id, newStatus string) error {
	oldStatus, err := s.transitionTaskTx(id, newStatus, nil)
	if err != nil {
		return err
	}
	s.emitTaskTransition(TaskTransitionEvent{
		TaskID:    id,
		OldStatus: oldStatus,
		NewStatus: newStatus,
	})
	return nil
}

// TransitionTaskWithReason sets a new status and blocked_reason atomically.
// Used by the scheduler when parking tasks on blocking checkpoints or when
// sweeping timed-out checkpoints — both cases need the status and the
// explanatory reason set together. Emits a task-transition hook after a
// successful UPDATE, same as TransitionTask.
func (s *Store) TransitionTaskWithReason(id, newStatus, reason string) error {
	r := reason
	oldStatus, err := s.transitionTaskTx(id, newStatus, &r)
	if err != nil {
		return err
	}
	s.emitTaskTransition(TaskTransitionEvent{
		TaskID:    id,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Reason:    reason,
	})
	return nil
}

// transitionTaskTx runs the SELECT-then-UPDATE under a single transaction so
// the old status we hand to the transition hook is the exact value the
// UPDATE replaced. Pass reason=nil to skip the blocked_reason column.
func (s *Store) transitionTaskTx(id, newStatus string, reason *string) (string, error) {
	tx, err := s.beginWriteTx()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var oldStatus string
	if err := tx.QueryRow(`SELECT status FROM tasks WHERE id = ?`, id).Scan(&oldStatus); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("task %s not found", id)
		}
		return "", err
	}

	var res sql.Result
	if reason != nil {
		res, err = tx.Exec(
			`UPDATE tasks SET status = ?, blocked_reason = ?, updated_at = ? WHERE id = ?`,
			newStatus, *reason, time.Now().UTC(), id,
		)
	} else {
		res, err = tx.Exec(
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
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return oldStatus, nil
}

// ParkTaskOnCheckpoint atomically transitions a task from "doing" to "review"
// with the given BlockedReason, but only when the task's current status is
// "doing" AND its checkpoint_mode is "blocking". Returns (true, nil) when the
// row was updated; (false, nil) when the predicate didn't match (task moved
// between the caller's read and this write, or wasn't eligible to begin with).
//
// Callers use this to park a task on a newly-emitted checkpoint without
// risking a clobber of a BlockedReason that another actor wrote between
// the caller's GetTask and the UPDATE — i.e. the classic TOCTOU
// race-condition avoidance.
func (s *Store) ParkTaskOnCheckpoint(id, reason string) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE tasks SET status = 'review', blocked_reason = ?, updated_at = ?
		 WHERE id = ? AND status = 'doing' AND checkpoint_mode = 'blocking'`,
		reason, time.Now().UTC(), id,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		// Park is always doing → review for a blocking-mode task. Fire the
		// transition hook so scheduler observers (worker cancel registry)
		// see the same event shape as plain TransitionTask. Avoids a silent
		// gap where a parked checkpoint leaves a worker running.
		s.emitTaskTransition(TaskTransitionEvent{
			TaskID:    id,
			OldStatus: "doing",
			NewStatus: "review",
			Reason:    reason,
		})
	}
	return n > 0, nil
}

// DeleteTask removes a task by ID.
func (s *Store) DeleteTask(id string) error {
	res, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
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

// SearchTasks does a LIKE search on id, title, and description.
func (s *Store) SearchTasks(query string) ([]TaskRecord, error) {
	pattern := "%" + query + "%"
	q := `SELECT ` + taskSelectCols + ` FROM tasks WHERE id LIKE ? OR title LIKE ? OR description LIKE ? ORDER BY priority ASC, created_at ASC`
	rows, err := s.ReadDB().Query(q, pattern, pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []TaskRecord
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

// NextTaskID generates an ID in CW-YYYYMMDD-NNNN format. See
// WriteTx.NextTaskID for the lex-MAX pitfall at the 9999→10000 boundary and
// why the lookup uses a half-open range with a width-bucketed MAX(id) instead
// of CAST(substr(id, n) AS INTEGER). For concurrent allocations under writer
// contention, prefer WriteTx.NextTaskID + CreateTask in one transaction; this
// auto-commit variant releases the reader between SELECT and the caller's
// INSERT.
func (s *Store) NextTaskID() (string, error) {
	today := time.Now().UTC().Format("20060102")
	prefix := "CW-" + today + "-"
	lo, hi := taskIDRange(prefix)

	var maxLen sql.NullInt64
	if err := s.db.QueryRow(
		`SELECT MAX(LENGTH(id)) FROM tasks WHERE id >= ? AND id < ?`,
		lo, hi,
	).Scan(&maxLen); err != nil {
		return "", err
	}

	seq := int64(1)
	if maxLen.Valid {
		var maxID sql.NullString
		if err := s.db.QueryRow(
			`SELECT MAX(id) FROM tasks WHERE id >= ? AND id < ? AND LENGTH(id) = ?`,
			lo, hi, maxLen.Int64,
		).Scan(&maxID); err != nil {
			return "", err
		}
		if maxID.Valid && len(maxID.String) > len(prefix) {
			n, err := strconv.ParseInt(maxID.String[len(prefix):], 10, 64)
			if err != nil {
				return "", fmt.Errorf("NextTaskID: parsing suffix of %q: %w", maxID.String, err)
			}
			seq = n + 1
		}
	}
	return fmt.Sprintf("%s%04d", prefix, seq), nil
}

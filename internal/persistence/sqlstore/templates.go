package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrTemplateNotFound is returned when a template (id[, version]) can't be
// located — callers use errors.Is to map to HTTP 404.
var ErrTemplateNotFound = errors.New("template not found")

// ErrTemplateReferenced is the sentinel returned from DeleteTemplate when
// tasks still reference any version of the template. DeleteTemplate now
// returns a *TemplateReferencedError carrying the id + count; use
// errors.As to unwrap, errors.Is(err, ErrTemplateReferenced) for the
// simple presence check.
var ErrTemplateReferenced = errors.New("template has referencing tasks")

// TemplateReferencedError carries the referenced template id and the
// count of referencing tasks so the service layer can build a clean
// message without stuttering the sentinel text.
type TemplateReferencedError struct {
	ID    string
	Count int
}

func (e *TemplateReferencedError) Error() string {
	return fmt.Sprintf("template %s has %d referencing task(s)", e.ID, e.Count)
}

// Is makes errors.Is(err, ErrTemplateReferenced) succeed so existing
// sentinel-based checks keep working.
func (e *TemplateReferencedError) Is(target error) bool {
	return target == ErrTemplateReferenced
}

// TemplateRecord mirrors a row in the task_templates table (migration 009).
// JSON blob columns (Tools, Permissions, Environment, EscalationChain,
// QualityGates, Deliverables, MetadataTemplate, RequiredVars, Tags) hold
// marshaled documents — schema validation is the service layer's job.
type TemplateRecord struct {
	ID                   string
	Version              int
	Name                 string
	Description          string
	Kind                 string
	AutoExecute          bool
	Executor             sql.NullString
	AgentProfile         sql.NullString
	SystemPrompt         sql.NullString
	WorkingDir           sql.NullString // migration 010 — templates set task.WorkingDir directly
	Tools                sql.NullString
	Permissions          sql.NullString
	Environment          sql.NullString
	CostBudget           sql.NullFloat64
	MaxRetries           int
	MaxDurationMs        sql.NullInt64
	TokenBudget          sql.NullInt64
	OnDone               string
	OnFail               string
	OnReview             string
	OnDoneMerge          string
	EscalationChain      sql.NullString
	QualityGates         sql.NullString
	Deliverables         sql.NullString
	CheckpointMode       string
	OnCheckpointResponse string
	MetadataTemplate     sql.NullString
	RequiredVars         sql.NullString
	Tags                 sql.NullString
	IsArchived           bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

const templateSelectCols = `id, version, name, description, kind, auto_execute,
	executor, agent_profile, system_prompt, working_dir, tools, permissions, environment,
	cost_budget, max_retries, max_duration_ms, token_budget,
	on_done, on_fail, on_review, on_done_merge,
	escalation_chain, quality_gates, deliverables,
	checkpoint_mode, on_checkpoint_response,
	metadata_template, required_vars, tags, is_archived, created_at, updated_at`

func scanTemplate(row interface {
	Scan(...any) error
}) (*TemplateRecord, error) {
	var t TemplateRecord
	var autoExecute, isArchived int
	err := row.Scan(
		&t.ID, &t.Version, &t.Name, &t.Description, &t.Kind, &autoExecute,
		&t.Executor, &t.AgentProfile, &t.SystemPrompt, &t.WorkingDir, &t.Tools, &t.Permissions, &t.Environment,
		&t.CostBudget, &t.MaxRetries, &t.MaxDurationMs, &t.TokenBudget,
		&t.OnDone, &t.OnFail, &t.OnReview, &t.OnDoneMerge,
		&t.EscalationChain, &t.QualityGates, &t.Deliverables,
		&t.CheckpointMode, &t.OnCheckpointResponse,
		&t.MetadataTemplate, &t.RequiredVars, &t.Tags, &isArchived, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	t.AutoExecute = autoExecute != 0
	t.IsArchived = isArchived != 0
	return &t, nil
}

// CreateTemplate inserts a new template row. Caller is responsible for
// picking the version (use NextTemplateVersion). Duplicate (id, version)
// returns a PK-violation error from sqlite.
func (s *Store) CreateTemplate(t *TemplateRecord) error {
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
	if t.CheckpointMode == "" {
		t.CheckpointMode = "none"
	}
	if t.OnCheckpointResponse == "" {
		t.OnCheckpointResponse = "resume"
	}
	if t.MaxRetries == 0 {
		t.MaxRetries = 3
	}

	autoExecute := 0
	if t.AutoExecute {
		autoExecute = 1
	}
	isArchived := 0
	if t.IsArchived {
		isArchived = 1
	}
	now := time.Now().UTC()

	_, err := s.db.Exec(`
		INSERT INTO task_templates (
			id, version, name, description, kind, auto_execute,
			executor, agent_profile, system_prompt, working_dir, tools, permissions, environment,
			cost_budget, max_retries, max_duration_ms, token_budget,
			on_done, on_fail, on_review, on_done_merge,
			escalation_chain, quality_gates, deliverables,
			checkpoint_mode, on_checkpoint_response,
			metadata_template, required_vars, tags, is_archived,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Version, t.Name, t.Description, t.Kind, autoExecute,
		t.Executor, t.AgentProfile, t.SystemPrompt, t.WorkingDir, t.Tools, t.Permissions, t.Environment,
		t.CostBudget, t.MaxRetries, t.MaxDurationMs, t.TokenBudget,
		t.OnDone, t.OnFail, t.OnReview, t.OnDoneMerge,
		t.EscalationChain, t.QualityGates, t.Deliverables,
		t.CheckpointMode, t.OnCheckpointResponse,
		t.MetadataTemplate, t.RequiredVars, t.Tags, isArchived,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("insert template: %w", err)
	}
	t.CreatedAt = now
	t.UpdatedAt = now
	return nil
}

// GetTemplate returns the specific (id, version). Returns ErrTemplateNotFound
// wrapped when no row matches.
func (s *Store) GetTemplate(id string, version int) (*TemplateRecord, error) {
	row := s.ReadDB().QueryRow(`SELECT `+templateSelectCols+` FROM task_templates WHERE id = ? AND version = ?`, id, version)
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%s@%d: %w", id, version, ErrTemplateNotFound)
	}
	return t, err
}

// GetLatestTemplate returns the highest-version, non-archived row for the
// given id — the row you instantiate when no version is specified.
func (s *Store) GetLatestTemplate(id string) (*TemplateRecord, error) {
	row := s.ReadDB().QueryRow(`SELECT `+templateSelectCols+` FROM task_templates
		WHERE id = ? AND is_archived = 0 ORDER BY version DESC LIMIT 1`, id)
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%s: %w", id, ErrTemplateNotFound)
	}
	return t, err
}

// NextTemplateVersion returns max(version)+1 for the id, or 1 if no rows
// exist. Used by the service layer's Create/Update to append a new version.
func (s *Store) NextTemplateVersion(id string) (int, error) {
	var maxV sql.NullInt64
	err := s.ReadDB().QueryRow(`SELECT MAX(version) FROM task_templates WHERE id = ?`, id).Scan(&maxV)
	if err != nil {
		return 0, err
	}
	if !maxV.Valid {
		return 1, nil
	}
	return int(maxV.Int64) + 1, nil
}

// ArchiveTemplate flips is_archived=1 for a single (id, version). Historical
// references survive; the row just leaves the live catalog.
func (s *Store) ArchiveTemplate(id string, version int) error {
	res, err := s.db.Exec(`UPDATE task_templates SET is_archived = 1, updated_at = ? WHERE id = ? AND version = ?`,
		time.Now().UTC(), id, version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%s@%d: %w", id, version, ErrTemplateNotFound)
	}
	return nil
}

// DeleteTemplate removes every version of a template by id. Refuses with
// ErrTemplateReferenced when tasks still reference the template via
// metadata.template_ref — callers should archive instead.
func (s *Store) DeleteTemplate(id string) error {
	var n int
	err := s.ReadDB().QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE metadata LIKE ?`,
		`%"template_ref":{"id":"`+id+`"%`,
	).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return &TemplateReferencedError{ID: id, Count: n}
	}
	res, err := s.db.Exec(`DELETE FROM task_templates WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%s: %w", id, ErrTemplateNotFound)
	}
	return nil
}

// ListTemplates returns matching rows ordered by id then version DESC.
// Pass includeArchived=true to surface retired rows for history views.
func (s *Store) ListTemplates(includeArchived bool, kindFilter string) ([]TemplateRecord, error) {
	q := `SELECT ` + templateSelectCols + ` FROM task_templates WHERE 1=1`
	var args []any
	if !includeArchived {
		q += ` AND is_archived = 0`
	}
	if kindFilter != "" {
		q += ` AND kind = ?`
		args = append(args, kindFilter)
	}
	q += ` ORDER BY id ASC, version DESC`

	rows, err := s.ReadDB().Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TemplateRecord
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

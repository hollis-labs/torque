package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WorkstreamClaim is the lease a team run holds on a workstream task. See
// migration 039.
type WorkstreamClaim struct {
	ID             string
	TaskID         string
	ProjectID      string
	TeamID         string
	TeamRunID      string
	Actor          string
	EnvironmentID  string
	IdempotencyKey string
	Status         string // active, released
	Outcome        string // completed, failed, canceled, handoff
	ResultRefs     sql.NullString
	LeaseExpiresAt time.Time
	CreatedAt      time.Time
	ReleasedAt     sql.NullTime
}

// ErrClaimNotFound is returned when no claim has the requested id.
var ErrClaimNotFound = errors.New("workstream claim not found")

const workstreamClaimCols = `id, task_id, project_id, team_id, team_run_id, actor,
	environment_id, idempotency_key, status, outcome, result_refs,
	lease_expires_at, created_at, released_at`

func scanWorkstreamClaim(row interface{ Scan(...any) error }) (*WorkstreamClaim, error) {
	var c WorkstreamClaim
	var created sql.NullTime
	if err := row.Scan(&c.ID, &c.TaskID, &c.ProjectID, &c.TeamID, &c.TeamRunID, &c.Actor,
		&c.EnvironmentID, &c.IdempotencyKey, &c.Status, &c.Outcome, &c.ResultRefs,
		&c.LeaseExpiresAt, &created, &c.ReleasedAt); err != nil {
		return nil, err
	}
	c.CreatedAt = created.Time
	return &c, nil
}

// GetWorkstreamClaim returns one claim by id.
func (s *Store) GetWorkstreamClaim(id string) (*WorkstreamClaim, error) {
	c, err := scanWorkstreamClaim(s.ReadDB().QueryRow(
		`SELECT `+workstreamClaimCols+` FROM workstream_claims WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("claim %s: %w", id, ErrClaimNotFound)
	}
	return c, err
}

// FindWorkstreamClaimByKey returns the claim a team run recorded under an
// idempotency key, or nil.
func (w *WriteTx) FindWorkstreamClaimByKey(teamRunID, key string) (*WorkstreamClaim, error) {
	c, err := scanWorkstreamClaim(w.tx.QueryRow(
		`SELECT `+workstreamClaimCols+` FROM workstream_claims WHERE team_run_id = ? AND idempotency_key = ?`,
		teamRunID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// GetWorkstreamClaim reads a claim under the write transaction.
func (w *WriteTx) GetWorkstreamClaim(id string) (*WorkstreamClaim, error) {
	c, err := scanWorkstreamClaim(w.tx.QueryRow(
		`SELECT `+workstreamClaimCols+` FROM workstream_claims WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("claim %s: %w", id, ErrClaimNotFound)
	}
	return c, err
}

// CountActiveWorkstreamClaims counts unreleased claims in a project.
func (w *WriteTx) CountActiveWorkstreamClaims(projectID string) (int, error) {
	var n int
	err := w.tx.QueryRow(
		`SELECT COUNT(*) FROM workstream_claims WHERE project_id = ? AND status = 'active'`,
		projectID).Scan(&n)
	return n, err
}

// ClaimableWorkstream names a workstream a team may take.
type ClaimableWorkstream struct {
	TaskID    string
	ProjectID string
}

// NextClaimableWorkstream returns the highest-priority cleared workstream a
// team may take: a todo parent task that is not manual, has children, has all
// dependencies done, and holds no active claim. projectScope, when non-empty,
// limits the search; projects in atCap are skipped. Ties break on creation
// time then id, so the order is stable. It returns nil when nothing qualifies.
func (w *WriteTx) NextClaimableWorkstream(projectScope []string, atCap map[string]bool) (*ClaimableWorkstream, error) {
	const q = `SELECT id, COALESCE(project_id, '') FROM tasks
WHERE status = 'todo' AND manual = 0 AND kind = 'parent'
  AND EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = tasks.id)
  AND NOT EXISTS (SELECT 1 FROM workstream_claims wc WHERE wc.task_id = tasks.id AND wc.status = 'active')
  AND NOT EXISTS (
      SELECT 1 FROM task_dependencies td
      LEFT JOIN tasks dep ON dep.id = td.depends_on_task_id
      WHERE td.task_id = tasks.id AND (dep.id IS NULL OR dep.status <> 'done'))
ORDER BY priority ASC, created_at ASC, id ASC`
	rows, err := w.tx.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ClaimableWorkstream
		if err := rows.Scan(&c.TaskID, &c.ProjectID); err != nil {
			return nil, err
		}
		if len(projectScope) > 0 && !containsString(projectScope, c.ProjectID) {
			continue
		}
		if atCap[c.ProjectID] {
			continue
		}
		return &c, nil
	}
	return nil, rows.Err()
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// InsertWorkstreamClaim records a new active claim.
func (w *WriteTx) InsertWorkstreamClaim(c *WorkstreamClaim) error {
	_, err := w.tx.Exec(`INSERT INTO workstream_claims
		(id, task_id, project_id, team_id, team_run_id, actor, environment_id,
		 idempotency_key, status, lease_expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
		c.ID, c.TaskID, c.ProjectID, c.TeamID, c.TeamRunID, c.Actor, c.EnvironmentID,
		c.IdempotencyKey, c.LeaseExpiresAt.UTC().Format(SQLiteDatetimeLayout))
	return err
}

// RenewWorkstreamClaim extends an active claim's lease.
func (w *WriteTx) RenewWorkstreamClaim(id string, until time.Time) error {
	res, err := w.tx.Exec(`UPDATE workstream_claims SET lease_expires_at = ? WHERE id = ? AND status = 'active'`,
		until.UTC().Format(SQLiteDatetimeLayout), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("claim %s is not active", id)
	}
	return nil
}

// ReleaseWorkstreamClaim marks an active claim released with its outcome.
func (w *WriteTx) ReleaseWorkstreamClaim(id, outcome, resultRefs string) error {
	refs := sql.NullString{String: resultRefs, Valid: resultRefs != ""}
	res, err := w.tx.Exec(`UPDATE workstream_claims SET status = 'released', outcome = ?, result_refs = ?, released_at = ?
		WHERE id = ? AND status = 'active'`, outcome, refs, updatedAtNow(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("claim %s is not active", id)
	}
	return nil
}

// ProjectsWithActiveClaims lists the projects that currently hold an active claim.
func (w *WriteTx) ProjectsWithActiveClaims() ([]string, error) {
	rows, err := w.tx.Query(`SELECT DISTINCT project_id FROM workstream_claims WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

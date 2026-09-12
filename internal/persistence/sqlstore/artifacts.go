package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrArtifactNotFound is returned wrapped by GetArtifact and DeleteArtifact
// when the artifact ID does not exist. Use errors.Is to detect.
var ErrArtifactNotFound = errors.New("artifact not found")

// ArtifactRecord mirrors the artifacts table row.
type ArtifactRecord struct {
	ID        int64
	TaskID    string
	RunID     sql.NullInt64
	Type      string
	Content   string
	URL       string
	FilePath  string
	Metadata  sql.NullString
	CreatedAt time.Time
}

// ArtifactPatch is a presence-aware update for mutable artifact columns. Nil
// fields are omitted from the SQL UPDATE; non-nil nullable fields can carry
// Valid=false to clear the column.
type ArtifactPatch struct {
	RunID    *sql.NullInt64
	Type     *string
	Content  *string
	URL      *string
	FilePath *string
	Metadata *sql.NullString
}

const artifactSelectCols = `id, task_id, run_id, type, content, url, file_path, metadata, created_at`

// CreateArtifact inserts a new artifact.
func (s *Store) CreateArtifact(a *ArtifactRecord) error {
	const q = `INSERT INTO artifacts (task_id, run_id, type, content, url, file_path, metadata)
	VALUES (?, ?, ?, ?, ?, ?, ?)`

	res, err := s.db.Exec(q,
		a.TaskID, a.RunID, a.Type, a.Content, a.URL, a.FilePath, a.Metadata,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	a.ID = id
	return nil
}

// GetArtifact returns a single artifact by ID, or ErrArtifactNotFound.
func (s *Store) GetArtifact(id int64) (*ArtifactRecord, error) {
	q := `SELECT ` + artifactSelectCols + ` FROM artifacts WHERE id = ?`
	row := s.ReadDB().QueryRow(q, id)

	var a ArtifactRecord
	if err := row.Scan(
		&a.ID, &a.TaskID, &a.RunID, &a.Type, &a.Content, &a.URL, &a.FilePath, &a.Metadata, &a.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("artifact %d: %w", id, ErrArtifactNotFound)
		}
		return nil, err
	}
	return &a, nil
}

// ListArtifacts returns all artifacts for a task, oldest first.
func (s *Store) ListArtifacts(taskID string) ([]ArtifactRecord, error) {
	q := `SELECT ` + artifactSelectCols + `
		FROM artifacts WHERE task_id = ? ORDER BY created_at ASC`

	rows, err := s.ReadDB().Query(q, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []ArtifactRecord
	for rows.Next() {
		var a ArtifactRecord
		if err := rows.Scan(
			&a.ID, &a.TaskID, &a.RunID, &a.Type, &a.Content, &a.URL, &a.FilePath, &a.Metadata, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, rows.Err()
}

// UpdateArtifact patches mutable artifact columns by ID and returns the fresh
// row. ID, owning task and creation timestamp are immutable.
func (s *Store) UpdateArtifact(id int64, patch ArtifactPatch) (*ArtifactRecord, error) {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 7)
	if patch.RunID != nil {
		sets = append(sets, "run_id = ?")
		args = append(args, *patch.RunID)
	}
	if patch.Type != nil {
		sets = append(sets, "type = ?")
		args = append(args, *patch.Type)
	}
	if patch.Content != nil {
		sets = append(sets, "content = ?")
		args = append(args, *patch.Content)
	}
	if patch.URL != nil {
		sets = append(sets, "url = ?")
		args = append(args, *patch.URL)
	}
	if patch.FilePath != nil {
		sets = append(sets, "file_path = ?")
		args = append(args, *patch.FilePath)
	}
	if patch.Metadata != nil {
		sets = append(sets, "metadata = ?")
		args = append(args, *patch.Metadata)
	}
	if len(sets) == 0 {
		return s.GetArtifact(id)
	}
	args = append(args, id)
	res, err := s.db.Exec(`UPDATE artifacts SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("artifact %d: %w", id, ErrArtifactNotFound)
	}
	return s.GetArtifact(id)
}

// DeleteArtifact removes the artifact row by ID. Returns ErrArtifactNotFound
// when no row matches. Does not touch the file referenced by FilePath.
func (s *Store) DeleteArtifact(id int64) error {
	res, err := s.db.Exec(`DELETE FROM artifacts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("artifact %d: %w", id, ErrArtifactNotFound)
	}
	return nil
}

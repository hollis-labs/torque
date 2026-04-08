package sqlstore

import (
	"database/sql"
	"time"
)

// ArtifactRecord mirrors the artifacts table row.
type ArtifactRecord struct {
	ID       int64
	TaskID   string
	RunID    sql.NullInt64
	Type     string
	Content  string
	URL      string
	FilePath string
	Metadata sql.NullString
	CreatedAt time.Time
}

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

// ListArtifacts returns all artifacts for a task, oldest first.
func (s *Store) ListArtifacts(taskID string) ([]ArtifactRecord, error) {
	const q = `SELECT id, task_id, run_id, type, content, url, file_path, metadata, created_at
		FROM artifacts WHERE task_id = ? ORDER BY created_at ASC`

	rows, err := s.db.Query(q, taskID)
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

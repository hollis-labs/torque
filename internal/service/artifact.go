package service

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// ArtifactService provides business logic for task artifacts.
type ArtifactService struct {
	store *sqlstore.Store
}

// ArtifactUpdateInput is a presence-aware patch for mutable artifact fields.
// Pointer nil means omitted; empty strings intentionally clear string fields.
type ArtifactUpdateInput struct {
	RunID    *sql.NullInt64
	Type     *string
	Content  *string
	URL      *string
	FilePath *string
	Metadata *sql.NullString
}

// Create inserts a new artifact.
func (s *ArtifactService) Create(a *sqlstore.ArtifactRecord) error {
	if err := s.validateRecord(a); err != nil {
		return err
	}
	return s.store.CreateArtifact(a)
}

// Get returns the artifact with the given ID.
func (s *ArtifactService) Get(id int64) (*sqlstore.ArtifactRecord, error) {
	return s.store.GetArtifact(id)
}

// List returns all artifacts for a task.
func (s *ArtifactService) List(taskID string) ([]sqlstore.ArtifactRecord, error) {
	return s.store.ListArtifacts(taskID)
}

// Update patches mutable fields on an artifact. ID, task ownership and
// creation timestamp are preserved.
func (s *ArtifactService) Update(id int64, in ArtifactUpdateInput) (*sqlstore.ArtifactRecord, error) {
	current, err := s.store.GetArtifact(id)
	if err != nil {
		return nil, err
	}
	if in.Type != nil && strings.TrimSpace(*in.Type) == "" {
		return nil, &ValidationError{Field: "type", Message: "type is required"}
	}
	if in.RunID != nil && in.RunID.Valid {
		if err := s.validateRunLink(current.TaskID, in.RunID.Int64); err != nil {
			return nil, err
		}
	}
	if in.Metadata != nil {
		if err := validateArtifactMetadata(*in.Metadata); err != nil {
			return nil, err
		}
	}
	return s.store.UpdateArtifact(id, sqlstore.ArtifactPatch{
		RunID:    in.RunID,
		Type:     in.Type,
		Content:  in.Content,
		URL:      in.URL,
		FilePath: in.FilePath,
		Metadata: in.Metadata,
	})
}

// Delete removes the artifact row by ID. Does not remove the referenced file.
func (s *ArtifactService) Delete(id int64) error {
	return s.store.DeleteArtifact(id)
}

func (s *ArtifactService) validateRecord(a *sqlstore.ArtifactRecord) error {
	if strings.TrimSpace(a.Type) == "" {
		return &ValidationError{Field: "type", Message: "type is required"}
	}
	if a.RunID.Valid {
		if err := s.validateRunLink(a.TaskID, a.RunID.Int64); err != nil {
			return err
		}
	}
	if err := validateArtifactMetadata(a.Metadata); err != nil {
		return err
	}
	return nil
}

func (s *ArtifactService) validateRunLink(taskID string, runID int64) error {
	if runID <= 0 {
		return &ValidationError{Field: "run_id", Message: "run_id must be a positive integer"}
	}
	run, err := s.store.GetRun(runID)
	if err != nil {
		return &ValidationError{Field: "run_id", Message: fmt.Sprintf("run_id %d does not exist", runID)}
	}
	if run.TaskID != taskID {
		return &ValidationError{Field: "run_id", Message: "run_id must belong to the artifact task"}
	}
	return nil
}

func validateArtifactMetadata(meta sql.NullString) error {
	if !meta.Valid {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(meta.String)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return &ValidationError{Field: "metadata", Message: "metadata must be a JSON object"}
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return &ValidationError{Field: "metadata", Message: "metadata must contain a single JSON object"}
	}
	if _, ok := value.(map[string]any); !ok {
		return &ValidationError{Field: "metadata", Message: "metadata must be a JSON object"}
	}
	return nil
}

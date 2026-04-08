package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// ArtifactService provides business logic for task artifacts.
type ArtifactService struct {
	store *sqlstore.Store
}

// Create inserts a new artifact.
func (s *ArtifactService) Create(a *sqlstore.ArtifactRecord) error {
	return s.store.CreateArtifact(a)
}

// List returns all artifacts for a task.
func (s *ArtifactService) List(taskID string) ([]sqlstore.ArtifactRecord, error) {
	return s.store.ListArtifacts(taskID)
}

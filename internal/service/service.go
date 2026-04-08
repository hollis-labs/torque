package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// Service is the root service dispatcher that aggregates all domain services.
type Service struct {
	Task     *TaskService
	Run      *RunService
	Artifact *ArtifactService
	Comment  *CommentService
	Settings *SettingsService
}

// New constructs a Service wired to the provided store.
func New(store *sqlstore.Store) *Service {
	return &Service{
		Task:     &TaskService{store: store},
		Run:      &RunService{store: store},
		Artifact: &ArtifactService{store: store},
		Comment:  &CommentService{store: store},
		Settings: &SettingsService{store: store},
	}
}

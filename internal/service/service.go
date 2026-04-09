package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// Service is the root service dispatcher that aggregates all domain services.
type Service struct {
	Task     *TaskService
	Run      *RunService
	Artifact *ArtifactService
	Comment  *CommentService
	Settings *SettingsService
	Feature  *FeatureService
	Sprint   *SprintService
	Project  *ProjectService
	Epic     *EpicService
	Tag      *TagService
}

// New constructs a Service wired to the provided store.
func New(store *sqlstore.Store) *Service {
	feature := &FeatureService{store: store}
	tag := &TagService{store: store}
	task := &TaskService{store: store, feature: feature, tags: tag}
	return &Service{
		Task:     task,
		Run:      &RunService{store: store},
		Artifact: &ArtifactService{store: store},
		Comment:  &CommentService{store: store},
		Settings: &SettingsService{store: store},
		Feature:  feature,
		Sprint:   &SprintService{store: store, feature: feature, task: task},
		Project:  &ProjectService{store: store, feature: feature},
		Epic:     &EpicService{store: store, feature: feature},
		Tag:      tag,
	}
}

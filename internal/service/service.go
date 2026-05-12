package service

import (
	"github.com/hollis-labs/clockwork-manifold/internal/modelcatalog"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/writequeue"
)

// Service is the root service dispatcher that aggregates all domain services.
type Service struct {
	// store is the underlying SQLite store. Not exported as a field so
	// every caller funnels through the domain services where business
	// rules apply; the Store() accessor is the explicit escape hatch
	// for application-service code (planstart, etc.) that composes
	// across multiple domain services and needs the raw store handle.
	store      *sqlstore.Store

	Task       *TaskService
	Run        *RunService
	Artifact   *ArtifactService
	Comment    *CommentService
	Settings   *SettingsService
	Feature    *FeatureService
	Sprint     *SprintService
	Project    *ProjectService
	Epic       *EpicService
	Collection *CollectionService
	Tag        *TagService
	Checkpoint *CheckpointService
	Template   *TemplateService
	Plan       *PlanService

	// Models is the shared models.dev catalog. nil in test wiring; populated
	// by serve / mcp entry points. Callers that depend on model metadata
	// must nil-check before use.
	Models *modelcatalog.Catalog
}

// Store returns the underlying SQLite store. Used by application-
// service code that composes across multiple domain services.
func (s *Service) Store() *sqlstore.Store { return s.store }

// New constructs a Service wired to the provided store.
func New(store *sqlstore.Store) *Service {
	feature := &FeatureService{store: store}
	tag := &TagService{store: store}
	task := &TaskService{store: store, feature: feature, tags: tag}
	return &Service{
		store:      store,
		Task:       task,
		Run:        &RunService{store: store},
		Artifact:   &ArtifactService{store: store},
		Comment:    &CommentService{store: store, writer: writequeue.NewDirect(store)},
		Settings:   &SettingsService{store: store},
		Feature:    feature,
		Sprint:     &SprintService{store: store, feature: feature, task: task},
		Project:    &ProjectService{store: store, feature: feature},
		Epic:       &EpicService{store: store, feature: feature},
		Collection: &CollectionService{store: store, feature: feature},
		Tag:        tag,
		Checkpoint: &CheckpointService{store: store},
		Template:   &TemplateService{store: store, tasks: task},
		Plan:       &PlanService{store: store, tasks: task},
	}
}

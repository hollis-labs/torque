package service

import (
	"database/sql"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// ProjectService provides business logic for projects.
type ProjectService struct {
	store   *sqlstore.Store
	feature *FeatureService
}

// ProjectCreateInput holds user-facing fields for creating a project.
type ProjectCreateInput struct {
	Name         string
	Description  string
	RepoPath     string
	AgentPath    string
	ReadPaths    []string
	WritePaths   []string
	ContextPaths []string
	Permissions  map[string]string
	Rules        []string
	Icon         string
	Status       *string
}

type ProjectArtifactCreateInput struct {
	EntryType   string
	Title       string
	Description string
	FilePath    string
	URL         string
	Content     string
	Permissions map[string]string
	Rules       []string
	Metadata    map[string]any
}

// Create validates and creates a new project.
func (s *ProjectService) Create(input ProjectCreateInput) (*sqlstore.ProjectRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}
	if input.RepoPath == "" {
		return nil, &ValidationError{Field: "repo_path", Message: "repo_path is required"}
	}
	// repo_path drift guard (CW-20260517-0011 edge 7): reject a project whose
	// repo_path does not resolve to an existing directory at create time, so
	// stale metadata can never be born. ~ is expanded before the stat.
	if err := validateRepoPath(input.RepoPath); err != nil {
		return nil, err
	}
	status := "active"
	if input.Status != nil {
		if err := validateProjectStatus(*input.Status); err != nil {
			return nil, err
		}
		status = *input.Status
	}

	id, err := s.store.NextProjectID()
	if err != nil {
		return nil, err
	}

	record := &sqlstore.ProjectRecord{
		ID:          id,
		Name:        input.Name,
		Description: input.Description,
		RepoPath:    input.RepoPath,
		AgentPath:   input.AgentPath,
		Icon:        input.Icon,
		Status:      status,
	}
	if len(input.ReadPaths) > 0 {
		record.ReadPaths = sql.NullString{String: marshalJSON(input.ReadPaths), Valid: true}
	}
	if len(input.WritePaths) > 0 {
		record.WritePaths = sql.NullString{String: marshalJSON(input.WritePaths), Valid: true}
	}
	if len(input.ContextPaths) > 0 {
		record.ContextPaths = sql.NullString{String: marshalJSON(input.ContextPaths), Valid: true}
	}
	if len(input.Permissions) > 0 {
		record.Permissions = sql.NullString{String: marshalJSON(input.Permissions), Valid: true}
	}
	if len(input.Rules) > 0 {
		record.Rules = sql.NullString{String: marshalJSON(input.Rules), Valid: true}
	}

	if err := s.store.CreateProject(record); err != nil {
		return nil, err
	}

	return s.store.GetProject(id)
}

// Get fetches a project by ID.
func (s *ProjectService) Get(id string) (*sqlstore.ProjectRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	return s.store.GetProject(id)
}

// List returns projects optionally filtered by status. includeArchived=false
// (the default) excludes archived rows; pass true to surface them too.
func (s *ProjectService) List(status string, includeArchived bool) ([]sqlstore.ProjectRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	return s.store.ListProjects(sqlstore.ProjectFilter{Status: status, IncludeArchived: includeArchived})
}

// ListPage is List's Phase 4 (ENT-PROJECT) sibling: it passes the full
// sqlstore.ProjectFilter through, including the SortBy/SortDir/
// AfterSortValue/AfterID fields PRIM-001/PRIM-002 cursor pagination needs.
// Kept separate from List (rather than changing List's signature) so the
// HTTP handler's simpler status+includeArchived contract — and its
// existing tests — don't have to change for a capability only
// torque_project_list uses today.
func (s *ProjectService) ListPage(filter sqlstore.ProjectFilter) ([]sqlstore.ProjectRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	return s.store.ListProjects(filter)
}

// Update applies a partial update to a project.
func (s *ProjectService) Update(id string, update sqlstore.ProjectUpdate) error {
	if err := s.feature.Require("projects"); err != nil {
		return err
	}
	if update.Status != nil {
		if err := validateProjectStatus(*update.Status); err != nil {
			return err
		}
	}
	if update.RepoPath != nil {
		if *update.RepoPath == "" {
			return &ValidationError{Field: "repo_path", Message: "repo_path is required"}
		}
		// repo_path drift guard (CW-20260517-0011 edge 7): an update that
		// sets repo_path must point at an existing directory. This also
		// gives operators a clear path to *fix* a stale project — the
		// update fails loudly until repo_path names a real directory.
		if err := validateRepoPath(*update.RepoPath); err != nil {
			return err
		}
	}
	return s.store.UpdateProject(id, update)
}

func validateProjectStatus(status string) error {
	if status != "active" && status != "inactive" {
		return &ValidationError{
			Field:   "status",
			Message: "must be one of: active, inactive",
		}
	}
	return nil
}

// Delete removes a project by ID.
func (s *ProjectService) Delete(id string) error {
	if err := s.feature.Require("projects"); err != nil {
		return err
	}
	return s.store.DeleteProject(id)
}

// Archive soft-deletes a project by setting archived_at. Does not change
// status — archiving is orthogonal to the project's workflow state.
func (s *ProjectService) Archive(id string) error {
	if err := s.feature.Require("projects"); err != nil {
		return err
	}
	return s.store.ArchiveProject(id)
}

// Unarchive restores a previously archived project.
func (s *ProjectService) Unarchive(id string) error {
	if err := s.feature.Require("projects"); err != nil {
		return err
	}
	return s.store.UnarchiveProject(id)
}

func (s *ProjectService) ListArtifacts(projectID string) ([]sqlstore.ProjectArtifactRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	if _, err := s.store.GetProject(projectID); err != nil {
		return nil, err
	}
	return s.store.ListProjectArtifacts(projectID)
}

func (s *ProjectService) CreateArtifact(projectID string, input ProjectArtifactCreateInput) (*sqlstore.ProjectArtifactRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	if _, err := s.store.GetProject(projectID); err != nil {
		return nil, err
	}
	if input.FilePath == "" {
		return nil, &ValidationError{Field: "file_path", Message: "file_path is required"}
	}
	record := &sqlstore.ProjectArtifactRecord{
		ProjectID:   projectID,
		EntryType:   input.EntryType,
		Title:       input.Title,
		Description: input.Description,
		FilePath:    input.FilePath,
		URL:         input.URL,
		Content:     input.Content,
	}
	if len(input.Permissions) > 0 {
		record.Permissions = sql.NullString{String: marshalJSON(input.Permissions), Valid: true}
	}
	if len(input.Rules) > 0 {
		record.Rules = sql.NullString{String: marshalJSON(input.Rules), Valid: true}
	}
	if len(input.Metadata) > 0 {
		record.Metadata = sql.NullString{String: marshalJSON(input.Metadata), Valid: true}
	}
	if err := s.store.CreateProjectArtifact(record); err != nil {
		return nil, err
	}
	return s.store.GetProjectArtifact(record.ID)
}

func (s *ProjectService) GetArtifact(id int64) (*sqlstore.ProjectArtifactRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	return s.store.GetProjectArtifact(id)
}

func (s *ProjectService) UpdateArtifact(id int64, update sqlstore.ProjectArtifactUpdate) error {
	if err := s.feature.Require("projects"); err != nil {
		return err
	}
	if update.FilePath != nil && *update.FilePath == "" {
		return &ValidationError{Field: "file_path", Message: "file_path is required"}
	}
	return s.store.UpdateProjectArtifact(id, update)
}

func (s *ProjectService) DeleteArtifact(id int64) error {
	if err := s.feature.Require("projects"); err != nil {
		return err
	}
	return s.store.DeleteProjectArtifact(id)
}

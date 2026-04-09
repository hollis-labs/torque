package service

import "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"

// ProjectService provides business logic for projects.
type ProjectService struct {
	store   *sqlstore.Store
	feature *FeatureService
}

// ProjectCreateInput holds user-facing fields for creating a project.
type ProjectCreateInput struct {
	Name        string
	Description string
	RepoPath    string
	Icon        string
}

// Create validates and creates a new project.
func (s *ProjectService) Create(input ProjectCreateInput) (*sqlstore.ProjectRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
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
		Icon:        input.Icon,
		Status:      "active",
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

// List returns projects optionally filtered by status.
func (s *ProjectService) List(status string) ([]sqlstore.ProjectRecord, error) {
	if err := s.feature.Require("projects"); err != nil {
		return nil, err
	}
	return s.store.ListProjects(sqlstore.ProjectFilter{Status: status})
}

// Update applies a partial update to a project.
func (s *ProjectService) Update(id string, update sqlstore.ProjectUpdate) error {
	if err := s.feature.Require("projects"); err != nil {
		return err
	}
	if update.Status != nil {
		if *update.Status != "active" && *update.Status != "inactive" {
			return &ValidationError{
				Field:   "status",
				Message: "must be one of: active, inactive",
			}
		}
	}
	return s.store.UpdateProject(id, update)
}

// Delete removes a project by ID.
func (s *ProjectService) Delete(id string) error {
	if err := s.feature.Require("projects"); err != nil {
		return err
	}
	return s.store.DeleteProject(id)
}

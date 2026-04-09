package service_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectCreateRequiresFeature(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Project.Create(service.ProjectCreateInput{Name: "Project 1"})
	assert.Error(t, err)
	assert.IsType(t, &service.FeatureDisabledError{}, err)
}

func TestProjectCreate(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	proj, err := svc.Project.Create(service.ProjectCreateInput{
		Name:        "Clockwork Manifold",
		Description: "Task orchestration engine",
		RepoPath:    "~/Projects-apps/clockwork-manifold",
	})
	require.NoError(t, err)
	assert.Contains(t, proj.ID, "PRJ-")
	assert.Equal(t, "Clockwork Manifold", proj.Name)
	assert.Equal(t, "~/Projects-apps/clockwork-manifold", proj.RepoPath)
}

func TestProjectCreateValidation(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	_, err := svc.Project.Create(service.ProjectCreateInput{})
	assert.Error(t, err)
	assert.IsType(t, &service.ValidationError{}, err)
}

func TestProjectList(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	svc.Project.Create(service.ProjectCreateInput{Name: "Project A"})
	svc.Project.Create(service.ProjectCreateInput{Name: "Project B"})

	projects, err := svc.Project.List("")
	require.NoError(t, err)
	assert.Len(t, projects, 2)
}

func TestProjectDelete(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	proj, _ := svc.Project.Create(service.ProjectCreateInput{Name: "Project 1"})

	err := svc.Project.Delete(proj.ID)
	require.NoError(t, err)

	_, err = svc.Project.Get(proj.ID)
	assert.Error(t, err)
}

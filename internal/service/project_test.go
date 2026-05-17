package service_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
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

	repo := t.TempDir()
	proj, err := svc.Project.Create(service.ProjectCreateInput{
		Name:        "Torque",
		Description: "Task orchestration engine",
		RepoPath:    repo,
	})
	require.NoError(t, err)
	assert.Contains(t, proj.ID, "PRJ-")
	assert.Equal(t, "Torque", proj.Name)
	assert.Equal(t, repo, proj.RepoPath)
}

// TestProjectCreateRejectsMissingRepoPath covers CW-20260517-0011 edge 7:
// a repo_path that does not resolve to an existing directory must fail
// create with a typed *RepoPathError, not silently persist stale metadata.
func TestProjectCreateRejectsMissingRepoPath(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	_, err := svc.Project.Create(service.ProjectCreateInput{
		Name:     "Stale",
		RepoPath: "/tmp/torque-nonexistent-" + t.Name(),
	})
	require.Error(t, err)
	var rpe *service.RepoPathError
	require.ErrorAs(t, err, &rpe)
	assert.Contains(t, err.Error(), "does not exist")
}

// TestProjectCreateRejectsFileRepoPath checks that a repo_path pointing at
// a file (not a directory) is also rejected.
func TestProjectCreateRejectsFileRepoPath(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	_, err := svc.Project.Create(service.ProjectCreateInput{Name: "FileRepo", RepoPath: file})
	require.Error(t, err)
	var rpe *service.RepoPathError
	require.ErrorAs(t, err, &rpe)
	assert.Contains(t, err.Error(), "not a directory")
}

// TestProjectUpdateRejectsMissingRepoPath covers the update half of edge 7:
// an update that sets repo_path to a missing directory must fail loudly.
func TestProjectUpdateRejectsMissingRepoPath(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	proj, err := svc.Project.Create(service.ProjectCreateInput{Name: "P", RepoPath: t.TempDir()})
	require.NoError(t, err)

	stale := "/tmp/torque-nonexistent-update-" + t.Name()
	err = svc.Project.Update(proj.ID, sqlstore.ProjectUpdate{RepoPath: &stale})
	require.Error(t, err)
	var rpe *service.RepoPathError
	require.ErrorAs(t, err, &rpe)
}

// TestCheckRepoPath exercises the non-mutating doctor helper.
func TestCheckRepoPath(t *testing.T) {
	dir := t.TempDir()
	resolved, err := service.CheckRepoPath(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, resolved)

	_, err = service.CheckRepoPath("/tmp/torque-nonexistent-doctor-" + t.Name())
	require.Error(t, err)
	var rpe *service.RepoPathError
	require.ErrorAs(t, err, &rpe)
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

	svc.Project.Create(service.ProjectCreateInput{Name: "Project A", RepoPath: t.TempDir()})
	svc.Project.Create(service.ProjectCreateInput{Name: "Project B", RepoPath: t.TempDir()})

	projects, err := svc.Project.List("")
	require.NoError(t, err)
	assert.Len(t, projects, 2)
}

func TestProjectCreateWithIcon(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	proj, err := svc.Project.Create(service.ProjectCreateInput{
		Name:     "Iconic Project",
		RepoPath: t.TempDir(),
		Icon:     "rocket",
	})
	require.NoError(t, err)
	assert.Equal(t, "rocket", proj.Icon)
	assert.Equal(t, "active", proj.Status)
}

func TestProjectUpdate(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	proj, _ := svc.Project.Create(service.ProjectCreateInput{Name: "Project 1", RepoPath: t.TempDir()})

	inactive := "inactive"
	err := svc.Project.Update(proj.ID, sqlstore.ProjectUpdate{Status: &inactive})
	require.NoError(t, err)

	got, _ := svc.Project.Get(proj.ID)
	assert.Equal(t, "inactive", got.Status)
}

func TestProjectUpdateInvalidStatus(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	proj, _ := svc.Project.Create(service.ProjectCreateInput{Name: "Project 1", RepoPath: t.TempDir()})

	bad := "deleted"
	err := svc.Project.Update(proj.ID, sqlstore.ProjectUpdate{Status: &bad})
	assert.Error(t, err)
	assert.IsType(t, &service.ValidationError{}, err)
}

func TestProjectDelete(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	proj, _ := svc.Project.Create(service.ProjectCreateInput{Name: "Project 1", RepoPath: t.TempDir()})

	err := svc.Project.Delete(proj.ID)
	require.NoError(t, err)

	_, err = svc.Project.Get(proj.ID)
	assert.Error(t, err)
}

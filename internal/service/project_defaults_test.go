package service_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func intp(i int) *int { return &i }

// CW-20261030-0208: a task in a project with execution defaults needs only a
// title; task > template > project > global.
func TestProjectExecutionDefaults_AppliedAndPrecedence(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")
	svc.Task.SetProfileValidator(profileCheck("implementer", "other"))

	proj, err := svc.Project.Create(service.ProjectCreateInput{
		Name:              "P",
		RepoPath:          t.TempDir(),
		ExecutionDefaults: `{"agent_profile":"implementer","on_done":"close","max_retries":5,"quality_gates":["subtodos"]}`,
	})
	require.NoError(t, err)

	// Project fills everything the task leaves unset.
	rec, err := svc.Task.Create(service.TaskCreateInput{Title: "t", ProjectID: proj.ID})
	require.NoError(t, err)
	assert.Equal(t, "implementer", rec.AgentProfile)
	assert.Equal(t, "close", rec.OnDone)
	assert.Equal(t, 5, rec.MaxRetries)
	assert.JSONEq(t, `["subtodos"]`, rec.QualityGates.String)

	// Task values win.
	rec, err = svc.Task.Create(service.TaskCreateInput{
		Title: "t2", ProjectID: proj.ID, AgentProfile: "other", OnDone: "notify", MaxRetries: intp(0), QualityGates: []string{"deliverables"},
	})
	require.NoError(t, err)
	assert.Equal(t, "other", rec.AgentProfile)
	assert.Equal(t, "notify", rec.OnDone)
	assert.Equal(t, 0, rec.MaxRetries)
	assert.JSONEq(t, `["deliverables"]`, rec.QualityGates.String)

	// A task outside the project gets the global defaults.
	rec, err = svc.Task.Create(service.TaskCreateInput{Title: "t3"})
	require.NoError(t, err)
	assert.Equal(t, "review", rec.OnDone)
	assert.Equal(t, 3, rec.MaxRetries)
	assert.Empty(t, rec.AgentProfile)
}

func TestProjectExecutionDefaults_TemplateBeatsProject(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")
	svc.Task.SetProfileValidator(profileCheck("implementer", "other"))

	proj, err := svc.Project.Create(service.ProjectCreateInput{
		Name:              "P",
		RepoPath:          t.TempDir(),
		ExecutionDefaults: `{"agent_profile":"implementer","on_done":"close","max_retries":5}`,
	})
	require.NoError(t, err)

	// Template sets only the profile and max_retries; on_done is its stored
	// global default, so the project's applies.
	_, err = svc.Template.Create(service.TemplateCreateInput{ID: "tpl", Name: "tpl", AgentProfile: "other", MaxRetries: intp(1)})
	require.NoError(t, err)
	rec, err := svc.Template.Instantiate(service.TemplateInstantiateInput{TemplateID: "tpl", Title: "x", ProjectID: proj.ID})
	require.NoError(t, err)
	assert.Equal(t, "other", rec.AgentProfile, "template beats project")
	assert.Equal(t, 1, rec.MaxRetries, "template beats project")
	assert.Equal(t, "close", rec.OnDone, "project fills what the template leaves at the global default")

	// A bare template inherits the project entirely.
	_, err = svc.Template.Create(service.TemplateCreateInput{ID: "bare", Name: "bare"})
	require.NoError(t, err)
	rec, err = svc.Template.Instantiate(service.TemplateInstantiateInput{TemplateID: "bare", Title: "y", ProjectID: proj.ID})
	require.NoError(t, err)
	assert.Equal(t, "implementer", rec.AgentProfile)
	assert.Equal(t, 5, rec.MaxRetries)
}

func TestProjectExecutionDefaults_Validation(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")
	svc.Task.SetProfileValidator(profileCheck("implementer"))
	repo := t.TempDir()

	for name, bad := range map[string]string{
		"unknown key":     `{"onDone":"close"}`,
		"bad on_done":     `{"on_done":"explode"}`,
		"negative":        `{"max_retries":-2}`,
		"unknown gate":    `{"quality_gates":["vibes"]}`,
		"unknown profile": `{"agent_profile":"typo"}`,
		"not json":        `nope`,
	} {
		_, err := svc.Project.Create(service.ProjectCreateInput{Name: name, RepoPath: repo, ExecutionDefaults: bad})
		assert.Error(t, err, name)
	}

	proj, err := svc.Project.Create(service.ProjectCreateInput{Name: "ok", RepoPath: repo})
	require.NoError(t, err)
	bad := sql.NullString{String: `{"agent_profile":"typo"}`, Valid: true}
	assert.Error(t, svc.Project.Update(proj.ID, sqlstore.ProjectUpdate{ExecutionDefaults: &bad}))
	good := sql.NullString{String: `{"agent_profile":"implementer"}`, Valid: true}
	require.NoError(t, svc.Project.Update(proj.ID, sqlstore.ProjectUpdate{ExecutionDefaults: &good}))
	got, err := svc.Project.Get(proj.ID)
	require.NoError(t, err)
	assert.JSONEq(t, good.String, got.ExecutionDefaults.String)
}

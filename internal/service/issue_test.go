package service_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

func TestIssueCreate_MinimalFields(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	project, err := svc.Project.Create(service.ProjectCreateInput{Name: "Issues Project", RepoPath: "/tmp/issues"})
	require.NoError(t, err)

	issue, err := svc.Issue.Create(service.IssueCreateInput{
		Title:     "Bug in login",
		Body:      "Callback returns 500.",
		ProjectID: project.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "issue", issue.Kind)
	require.Equal(t, "backlog", issue.Status)
	require.True(t, issue.Manual)
	require.Equal(t, "", issue.Executor)
	require.Equal(t, project.ID, issue.ProjectID.String)
	require.Equal(t, "Callback returns 500.", issue.Description)
}

func TestIssueCreate_RequiresProject(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Issue.Create(service.IssueCreateInput{
		Title:     "Bug",
		Body:      "Details",
		ProjectID: "PRJ-missing",
	})
	require.Error(t, err)
	var disabled *service.FeatureDisabledError
	require.ErrorAs(t, err, &disabled)

	require.NoError(t, svc.Feature.Enable("projects"))
	_, err = svc.Issue.Create(service.IssueCreateInput{
		Title:     "Bug",
		Body:      "Details",
		ProjectID: "PRJ-missing",
	})
	require.Error(t, err)
	var validation *service.ValidationError
	require.ErrorAs(t, err, &validation)
	require.Equal(t, "project_id", validation.Field)
}

func TestIssueListAndSearchAreKindScoped(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	project, err := svc.Project.Create(service.ProjectCreateInput{Name: "Issues Project", RepoPath: "/tmp/issues"})
	require.NoError(t, err)

	issue, err := svc.Issue.Create(service.IssueCreateInput{
		Title:     "Rocket bug",
		Body:      "Explodes on launch.",
		ProjectID: project.ID,
	})
	require.NoError(t, err)
	_, err = svc.Task.Create(service.TaskCreateInput{
		Title:       "Rocket task",
		Description: "Same term, different kind.",
		ProjectID:   project.ID,
	})
	require.NoError(t, err)

	listed, err := svc.Issue.List(project.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, issue.ID, listed[0].ID)

	found, err := svc.Issue.Search("Rocket", project.ID, 10)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, issue.ID, found[0].ID)
}

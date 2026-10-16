package service_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProgressDetailTemplateSnapshotAndOverride(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{ID: "progress", Name: "Progress", Kind: "agent", ProgressDetail: "step"})
	require.NoError(t, err)
	create := func(overrides map[string]any) *sqlstore.TaskRecord {
		t.Helper()
		task, err := svc.Template.Instantiate(service.TemplateInstantiateInput{TemplateID: "progress", Title: "work", Overrides: overrides})
		require.NoError(t, err)
		return task
	}
	inherited := create(nil)
	require.Equal(t, sql.NullString{String: "step", Valid: true}, inherited.ProgressDetail)
	require.Equal(t, "template", inherited.ProgressDetailSource.String)
	explicit := create(map[string]any{"progress_detail": "step"})
	require.Equal(t, "task", explicit.ProgressDetailSource.String, "equal value still has explicit origin")
	cleared := create(map[string]any{"progress_detail": ""})
	require.False(t, cleared.ProgressDetail.Valid)
	require.False(t, cleared.ProgressDetailSource.Valid)

	verbose := "verbose"
	_, err = svc.Template.Update("progress", service.TemplateUpdateInput{ProgressDetail: &verbose})
	require.NoError(t, err)
	original, err := svc.Task.Get(inherited.ID)
	require.NoError(t, err)
	require.Equal(t, "step", original.ProgressDetail.String, "later template edits cannot change an existing task")
	require.Equal(t, "verbose", create(nil).ProgressDetail.String)

	same := sql.NullString{String: "step", Valid: true}
	require.NoError(t, svc.Task.Update(inherited.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{ProgressDetail: &same}}))
	updated, err := svc.Task.Get(inherited.ID)
	require.NoError(t, err)
	require.Equal(t, "task", updated.ProgressDetailSource.String)
	empty := sql.NullString{}
	require.NoError(t, svc.Task.Update(inherited.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{ProgressDetail: &empty}}))
	updated, err = svc.Task.Get(inherited.ID)
	require.NoError(t, err)
	require.False(t, updated.ProgressDetail.Valid)
	require.False(t, updated.ProgressDetailSource.Valid)

	blank := ""
	_, err = svc.Template.Update("progress", service.TemplateUpdateInput{ProgressDetail: &blank})
	require.NoError(t, err)
	require.False(t, create(nil).ProgressDetail.Valid)
}

func TestProgressDetailValidation(t *testing.T) {
	svc := setupService(t)
	for _, level := range []string{"", "off", "milestone", "step", "verbose"} {
		task, err := svc.Task.Create(service.TaskCreateInput{Title: "valid", ProgressDetail: level})
		require.NoError(t, err)
		require.Equal(t, level != "", task.ProgressDetail.Valid)
		require.Equal(t, level != "", task.ProgressDetailSource.Valid)
	}
	_, err := svc.Task.Create(service.TaskCreateInput{Title: "invalid", ProgressDetail: "typo"})
	require.Error(t, err)
	_, err = svc.Template.Create(service.TemplateCreateInput{ID: "invalid", Name: "invalid", ProgressDetail: "typo"})
	require.Error(t, err)
	_, err = svc.Template.Create(service.TemplateCreateInput{ID: "valid", Name: "valid", ProgressDetail: "off"})
	require.NoError(t, err)
	invalid := "typo"
	_, err = svc.Template.Update("valid", service.TemplateUpdateInput{ProgressDetail: &invalid})
	require.Error(t, err)
	for _, v := range []any{false, 42, "typo"} {
		_, err = svc.Template.Instantiate(service.TemplateInstantiateInput{TemplateID: "valid", Title: "invalid override", Overrides: map[string]any{"progress_detail": v}})
		require.Error(t, err)
	}
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "update"})
	require.NoError(t, err)
	invalidValue := sql.NullString{String: "typo", Valid: true}
	require.Error(t, svc.Task.Update(task.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{ProgressDetail: &invalidValue}}))
	fakeSource := sql.NullString{String: "template", Valid: true}
	require.Error(t, svc.Task.Update(task.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{ProgressDetailSource: &fakeSource}}))
}

package service_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func registered(names ...string) func() []string {
	return func() []string { return names }
}

func strp(s string) *string { return &s }

// CW-20260910-0087: a task names only an executor the process registers.
func TestTaskCreate_RejectsUnregisteredExecutor(t *testing.T) {
	svc := setupService(t)
	svc.Task.SetRegisteredExecutors(registered("api", "cli", "mock"))

	_, err := svc.Task.Create(service.TaskCreateInput{Title: "provider in the executor column", Executor: "opencode"})
	var verr *service.ValidationError
	require.True(t, errors.As(err, &verr), "got %v", err)
	assert.Equal(t, "executor", verr.Field)
	assert.Equal(t, "invalid executor: got 'opencode', expected one of: api, cli, mock (or empty for the default)", verr.Message)

	rec, err := svc.Task.Create(service.TaskCreateInput{Title: "registered", Executor: "api"})
	require.NoError(t, err)
	assert.Equal(t, "api", rec.Executor)

	// Empty means the default, which for kind=agent is cli.
	rec, err = svc.Task.Create(service.TaskCreateInput{Title: "default"})
	require.NoError(t, err)
	assert.Equal(t, "cli", rec.Executor)
}

func TestTaskUpdate_RejectsUnregisteredExecutorButLeavesExistingRowsEditable(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	svc.Task.SetRegisteredExecutors(registered("api", "cli", "mock"))

	rec, err := svc.Task.Create(service.TaskCreateInput{Title: "t"})
	require.NoError(t, err)
	err = svc.Task.Update(rec.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Executor: strp("codex")}})
	var verr *service.ValidationError
	require.True(t, errors.As(err, &verr), "got %v", err)
	assert.Equal(t, "executor", verr.Field)

	// A row written before validation, or before the registry changed,
	// keeps its executor and can still be edited.
	legacy := &sqlstore.TaskRecord{ID: "CW-20260101-0001", Title: "legacy", Priority: 2, Kind: "agent", Executor: "opencode"}
	require.NoError(t, store.CreateTask(legacy))
	require.NoError(t, svc.Task.Update(legacy.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Title: strp("legacy, retitled")}}))
	got, err := svc.Task.Get(legacy.ID)
	require.NoError(t, err)
	assert.Equal(t, "opencode", got.Executor)
	assert.Equal(t, "legacy, retitled", got.Title)

	// Moving it onto a registered executor is allowed.
	require.NoError(t, svc.Task.Update(legacy.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Executor: strp("cli")}}))
}

// A process with no executor registry (or an empty one) checks nothing,
// rather than refusing every executor.
func TestTaskCreate_NoRegistryAcceptsAnyExecutor(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Task.Create(service.TaskCreateInput{Title: "no registry", Executor: "opencode"})
	require.NoError(t, err)

	svc.Task.SetRegisteredExecutors(registered())
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "empty registry", Executor: "opencode"})
	require.NoError(t, err)
}

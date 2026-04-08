package sqlstore_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAndListRuns(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	r1 := &sqlstore.RunRecord{
		TaskID:   task.ID,
		Executor: "cli",
	}
	id1, err := store.CreateRun(r1)
	require.NoError(t, err)
	assert.Greater(t, id1, int64(0))
	assert.Equal(t, "running", r1.Status)

	r2 := &sqlstore.RunRecord{
		TaskID:   task.ID,
		Executor: "agent",
		Status:   "done",
	}
	id2, err := store.CreateRun(r2)
	require.NoError(t, err)
	assert.Greater(t, id2, id1)

	runs, err := store.ListRuns(task.ID)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	// newest first
	assert.Equal(t, id2, runs[0].ID)
	assert.Equal(t, id1, runs[1].ID)
}

func TestCompleteRun(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	r := &sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"}
	id, err := store.CreateRun(r)
	require.NoError(t, err)

	exitCode := 0
	err = store.CompleteRun(id, sqlstore.RunCompletion{
		Status:           "done",
		PromptTokens:     100,
		CompletionTokens: 200,
		Cost:             0.05,
		ExitCode:         &exitCode,
		ErrorMessage:     "",
	})
	require.NoError(t, err)

	got, err := store.GetRun(id)
	require.NoError(t, err)
	assert.Equal(t, "done", got.Status)
	assert.Equal(t, 100, got.PromptTokens)
	assert.Equal(t, 200, got.CompletionTokens)
	assert.InDelta(t, 0.05, got.Cost, 0.0001)
	assert.True(t, got.EndedAt.Valid)
	assert.True(t, got.ExitCode.Valid)
	assert.Equal(t, int64(0), got.ExitCode.Int64)
}

func TestGetRun_NotFound(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.GetRun(9999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

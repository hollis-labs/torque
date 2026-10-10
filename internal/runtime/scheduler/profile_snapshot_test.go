package scheduler_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfileSnapshotRedaction_DispatchPath(t *testing.T) {
	sched, store, _ := setupScheduler(t)

	sched.Profiles = config.ProfileMap{
		"mock": {
			Provider:         "openai",
			Model:            "gpt-4",
			Executor:         "cli",
			RuntimeKind:      "pty",
			PermissionMode:   "acceptEdits",
			SystemPrompt:     "sentinel-system-prompt",
			APIKey:           "sentinel-api-key",
			Args:             []string{"--secret=sentinel-args"},
			EnvStripPrefixes: []string{"sentinel-env"},
			BaseURL:          "https://sentinel-url.com",
		},
	}

	store.CreateTask(&sqlstore.TaskRecord{
		ID:            "CW-0001",
		Title:         "T1",
		Status:        "todo",
		Priority:      1,
		Executor:      "mock",
		LaunchProfile: "",
		AgentProfile:  "mock",
	})

	require.NoError(t, sched.Tick(context.Background()))

	runs, err := store.ListRuns("CW-0001")
	require.NoError(t, err)
	require.Len(t, runs, 1)

	run, err := store.GetRun(runs[0].ID)
	require.NoError(t, err)

	snapshot := run.ProfileSnapshot.String
	assert.Contains(t, snapshot, `"provider":"openai"`)
	assert.Contains(t, snapshot, `"model":"gpt-4"`)
	assert.Contains(t, snapshot, `"executor":"cli"`)
	assert.Contains(t, snapshot, `"runtime_kind":"pty"`)
	assert.Contains(t, snapshot, `"permission_mode":"acceptEdits"`)
	assert.Contains(t, snapshot, `"launch_profile_id":"mock"`)
	assert.Contains(t, snapshot, `"role":"mock"`)
	assert.NotContains(t, snapshot, "sentinel-api-key")
	assert.NotContains(t, snapshot, "sentinel-system-prompt")
	assert.NotContains(t, snapshot, "sentinel-args")
	assert.NotContains(t, snapshot, "sentinel-env")
	assert.NotContains(t, snapshot, "sentinel-url")
}

func TestProfileSnapshotRedaction_BlockedPath(t *testing.T) {
	sched, store, _ := setupScheduler(t)

	sched.Profiles = config.ProfileMap{
		"mock": {
			Provider:         "openai",
			Model:            "gpt-4",
			Executor:         "cli",
			RuntimeKind:      "pty",
			PermissionMode:   "acceptEdits",
			SystemPrompt:     "sentinel-system-prompt",
			APIKey:           "sentinel-api-key",
			Args:             []string{"--secret=sentinel-args"},
			EnvStripPrefixes: []string{"sentinel-env"},
			BaseURL:          "https://sentinel-url.com",
		},
	}

	// Missing executor causes a permanent validation error
	store.CreateTask(&sqlstore.TaskRecord{
		ID:            "CW-0001",
		Title:         "T1",
		Status:        "todo",
		Priority:      1,
		Executor:      "missing-executor",
		LaunchProfile: "",
		AgentProfile:  "mock",
	})

	require.NoError(t, sched.Tick(context.Background()))

	task, _ := store.GetTask("CW-0001")
	require.Equal(t, "blocked", task.Status)

	runs, err := store.ListRuns("CW-0001")
	require.NoError(t, err)
	require.Len(t, runs, 1)

	run, err := store.GetRun(runs[0].ID)
	require.NoError(t, err)

	snapshot := run.ProfileSnapshot.String
	assert.Contains(t, snapshot, `"provider":"openai"`)
	assert.Contains(t, snapshot, `"model":"gpt-4"`)
	assert.Contains(t, snapshot, `"executor":"cli"`)
	assert.Contains(t, snapshot, `"runtime_kind":"pty"`)
	assert.Contains(t, snapshot, `"permission_mode":"acceptEdits"`)
	assert.Contains(t, snapshot, `"launch_profile_id":"mock"`)
	assert.Contains(t, snapshot, `"role":"mock"`)
	assert.NotContains(t, snapshot, "sentinel-api-key")
	assert.NotContains(t, snapshot, "sentinel-system-prompt")
	assert.NotContains(t, snapshot, "sentinel-args")
	assert.NotContains(t, snapshot, "sentinel-env")
	assert.NotContains(t, snapshot, "sentinel-url")
}

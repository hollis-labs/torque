package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerImportCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TORQUE_DB_PATH", filepath.Join(tmp, "torque.db"))
	t.Setenv("TORQUE_QUEUE_DB_PATH", filepath.Join(tmp, "queue.db"))

	data := filepath.Join(tmp, "data")
	require.NoError(t, os.Mkdir(data, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(data, "inbox.json"),
		[]byte(`{"schema":"portfolio/inbox@1","items":[{"id":"IN-001","title":"t","rev":2}]}`), 0o644))

	var out bytes.Buffer
	require.NoError(t, runPlannerImport(context.Background(), &out, data, false, false, false))
	assert.Contains(t, out.String(), "DRY RUN")

	out.Reset()
	require.NoError(t, runPlannerImport(context.Background(), &out, data, true, false, false))
	assert.Contains(t, out.String(), "Verified")
	assert.Contains(t, out.String(), "features.planner is off")

	out.Reset()
	require.NoError(t, runPlannerImport(context.Background(), &out, data, true, false, true))
	assert.Contains(t, out.String(), `"verified": true`)

	require.Error(t, runPlannerImport(context.Background(), &out, "", false, false, false))
}

func TestPlannerBoardCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TORQUE_DB_PATH", filepath.Join(tmp, "torque.db"))
	t.Setenv("TORQUE_QUEUE_DB_PATH", filepath.Join(tmp, "queue.db"))

	// The planner is off by default: the command says how to turn it on.
	var out bytes.Buffer
	err := runPlannerBoard(context.Background(), &out, service.PlannerBoardOptions{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "features.planner")

	// Enable it and add one inbox item through the same database.
	store, closeStore, err := openStoreForCLI()
	require.NoError(t, err)
	svc := service.New(store)
	require.NoError(t, svc.Feature.Enable("planner"))
	_, err = svc.Planner.InboxAdd(map[string]any{"title": "Look at the board"}, "", "tester")
	require.NoError(t, err)
	closeStore()

	out.Reset()
	require.NoError(t, runPlannerBoard(context.Background(), &out, service.PlannerBoardOptions{}, false))
	assert.Contains(t, out.String(), "DEPARTURES")
	assert.Contains(t, out.String(), "TRIAGE")
	assert.Contains(t, out.String(), "IN-001")

	out.Reset()
	require.NoError(t, runPlannerBoard(context.Background(), &out, service.PlannerBoardOptions{Limit: 5}, true))
	assert.Contains(t, out.String(), `"limit": 5`)
}

func TestJudgeCommands(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TORQUE_DB_PATH", filepath.Join(tmp, "torque.db"))
	t.Setenv("TORQUE_QUEUE_DB_PATH", filepath.Join(tmp, "queue.db"))

	run := func(args ...string) (string, error) {
		cmd := judgeCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.ExecuteContext(context.Background())
		return out.String(), err
	}

	out, err := run("sets")
	require.NoError(t, err)
	for _, want := range []string{"task-kind", "task-decision-state", "task-layer", "suggest>=0.40"} {
		assert.Contains(t, out, want)
	}

	// A task with a kind tag, so there is something to label.
	store, closeStore, err := openStoreForCLI()
	require.NoError(t, err)
	svc := service.New(store)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "Fix the crash", Description: "It is broken: a bug that must be fixed.", Tags: []string{"bug"}})
	require.NoError(t, err)
	closeStore()

	out, err = run("eval", "--set", "task-kind", "--n", "10")
	require.NoError(t, err)
	assert.Contains(t, out, "Set task-kind v1")
	assert.Contains(t, out, "keyword-baseline")
	assert.Contains(t, out, "1 labeled")

	out, err = run("enrich", "--set", "task-kind", "--task", task.ID)
	require.NoError(t, err)
	assert.Contains(t, out, "Dry run")
	out, _ = run("ledger")
	assert.NotContains(t, out, task.ID, "a dry run records nothing")

	out, err = run("enrich", "--set", "task-kind", "--task", task.ID, "--record")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded as suggestions by keyword-baseline")
	out, err = run("ledger", "--subject", task.ID)
	require.NoError(t, err)
	assert.Contains(t, out, task.ID)
	assert.Contains(t, out, "suggested")

	_, err = run("eval", "--set", "task-kind", "--decider", "jev")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CW-20261010-0041")
	_, err = run("eval", "--set", "nope")
	require.Error(t, err)
	_, err = run("eval")
	require.Error(t, err, "--set is required")
}

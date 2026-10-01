package scheduler_test

import (
	"fmt"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/require"
)

func TestStaticEligibilityMatchesConstraintFreePicker(t *testing.T) {
	store := setupPickerStore(t)
	fixtures := []struct {
		id, status, kind, agent, launch string
		manual, eligible                bool
	}{
		{"agent", "todo", "agent", "profile", "", false, true},
		{"launch", "todo", "agent", "", "launch", false, true},
		{"internal", "todo", "internal", "profile", "", false, true},
		{"internal-launch", "todo", "internal", "", "launch", false, true},
		{"whitespace-profile", "todo", "agent", " ", "", false, true},
		{"external", "todo", "external", "", "", false, true},
		{"wait", "todo", "wait", "", "", false, true},
		{"decision", "todo", "decision", "", "", false, true},
		{"manual", "todo", "agent", "profile", "", true, false},
		{"parent", "todo", "parent", "profile", "", false, false},
		{"plan", "todo", "plan", "profile", "", false, false},
		{"issue", "todo", "issue", "profile", "", false, false},
		{"no-agent", "todo", "agent", "", "", false, false},
		{"no-internal", "todo", "internal", "", "", false, false},
		{"doing", "doing", "agent", "profile", "", false, false},
		{"done", "done", "agent", "profile", "", false, false},
		{"blocked", "blocked", "agent", "profile", "", false, false},
		{"archived", "archived", "agent", "profile", "", false, false},
		{"dep-done", "todo", "agent", "profile", "", false, true},
		{"dep-todo", "todo", "agent", "profile", "", false, false},
		{"dep-archived", "todo", "agent", "profile", "", false, false},
		{"dep-mixed", "todo", "agent", "profile", "", false, false},
		{"dep-missing", "todo", "agent", "profile", "", false, false},
	}
	for _, status := range []string{"backlog", "queued", "review", "paused", "abandoned", "cancelled"} {
		fixtures = append(fixtures, struct {
			id, status, kind, agent, launch string
			manual, eligible                bool
		}{status, status, "agent", "profile", "", false, false})
	}
	var expected []string
	for _, f := range fixtures {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: f.id, Title: f.id, Status: f.status, Kind: f.kind, AgentProfile: f.agent, LaunchProfile: f.launch, Manual: f.manual, Executor: "cli"}))
		if f.eligible {
			expected = append(expected, f.id)
		}
	}
	for id, dep := range map[string]string{"dep-done": "done", "dep-todo": "agent", "dep-archived": "archived"} {
		require.NoError(t, store.SetTaskDependencies(id, []string{dep}))
	}
	require.NoError(t, store.SetTaskDependencies("dep-mixed", []string{"done", "archived"}))
	// FK enforcement is disabled in this fixture to exercise fail-closed
	// behavior for a corrupt/missing dependency, as Picker does.
	_, err := store.DB().Exec("INSERT INTO task_dependencies (task_id, depends_on_task_id, sort_order, created_at) VALUES (?, ?, 0, CURRENT_TIMESTAMP)", "dep-missing", "missing")
	require.NoError(t, err)
	// All tasks are projectless; no project contention/allowlist or worker cap.
	picked, _, err := scheduler.NewPicker(store).Pick(len(fixtures) + 1)
	require.NoError(t, err)
	queried, err := store.ListTasks(sqlstore.TaskFilter{Eligible: true})
	require.NoError(t, err)
	ids := func(rows []sqlstore.TaskRecord) []string {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return out
	}
	require.ElementsMatch(t, expected, ids(picked))
	require.ElementsMatch(t, expected, ids(queried))
	for _, f := range fixtures {
		t.Run(fmt.Sprintf("condition/%s", f.id), func(t *testing.T) { require.Equal(t, f.eligible, containsTask(queried, f.id)) })
	}
	all, err := store.ListTasks(sqlstore.TaskFilter{Eligible: false})
	require.NoError(t, err)
	require.Len(t, all, len(fixtures), "false means no eligibility restriction")
}

func containsTask(rows []sqlstore.TaskRecord, id string) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

package agent

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
)

// A caller cannot forge the torque.resumed meta Boot stamps when a launch
// carries a provider session id (CW-20261001-0203): callerSessionMeta drops
// it with the other Torque-owned keys.
func TestCallerSessionMeta_StripsTorqueResumed(t *testing.T) {
	out := callerSessionMeta(map[string]string{
		"torque.resumed": "true", "torque.mode": "x", "role": "implementer",
	})
	assert.NotContains(t, out, "torque.resumed")
	assert.NotContains(t, out, "torque.mode")
	assert.Equal(t, "implementer", out["role"], "a caller's own keys stay")
}

// A session row's torque.resumed meta reads back as Session.Resumed, and
// only "true" does.
func TestSessionFromRecord_Resumed(t *testing.T) {
	assert.True(t, sessionFromRecord(&sqlstore.SessionRecord{MetaJSON: `{"torque.resumed":"true"}`}).Resumed)
	assert.False(t, sessionFromRecord(&sqlstore.SessionRecord{MetaJSON: `{"torque.resumed":"false"}`}).Resumed)
	assert.False(t, sessionFromRecord(&sqlstore.SessionRecord{MetaJSON: `{}`}).Resumed)
}

// A task-linked session is re-launched with its task the way a dispatch
// hands it (CW-20261001-0249): the scheduler's own mapping fills the task's
// title, description, kind, status, priority, relationships, system prompt,
// agent file and environment, so the planted task bundle and the kickoff are
// not blank, and the kind=agent idle nudge is armed. The session's own
// profile, workdir and role stay.
func TestSourceBootOptions_CarriesTheTask(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	defer store.Close()
	mgr := NewManager(&Dependencies{Store: store, StateWriter: writeq.NewDirect(store)})

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-SRC-PARENT", Title: "parent", Priority: 2}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-SRC-DEP", Title: "dependency", Priority: 2}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-SRC-TASK", Title: "Summarize the README", Description: "Read README.md and comment a three line summary.",
		Kind: "agent", Status: "doing", Priority: 3, SystemPrompt: "Be brief.",
		Environment: sql.NullString{String: `{"TASK_ENV":"from-task"}`, Valid: true},
		ParentID:    sql.NullString{String: "CW-SRC-PARENT", Valid: true},
		Metadata:    sql.NullString{String: `{"idle_nudge_seconds":45}`, Valid: true},
	}))
	require.NoError(t, store.SetTaskDependencies("CW-SRC-TASK", []string{"CW-SRC-DEP"}))
	rec := &sqlstore.SessionRecord{
		ID: "SES-SRC", AgentProfile: "implementer", Workdir: "/work/run-7", MetaJSON: `{"role":"implementer"}`,
		TaskID: sql.NullString{String: "CW-SRC-TASK", Valid: true},
	}

	opts := mgr.sourceBootOptions(rec)
	assert.Equal(t, ModeLongLived, opts.Mode)
	assert.Equal(t, "CW-SRC-TASK", opts.TaskID)
	assert.Equal(t, "Summarize the README", opts.TaskTitle)
	assert.Equal(t, "Read README.md and comment a three line summary.", opts.Description)
	assert.Equal(t, "agent", opts.TaskKind)
	assert.Equal(t, "doing", opts.TaskStatus)
	assert.Equal(t, 3, opts.TaskPriority)
	assert.Equal(t, "CW-SRC-PARENT", opts.ParentID)
	assert.Equal(t, []string{"CW-SRC-DEP"}, opts.DependsOn)
	assert.Equal(t, "Be brief.", opts.SystemPrompt)
	assert.Equal(t, map[string]string{"TASK_ENV": "from-task"}, opts.Env)
	assert.EqualValues(t, 0, opts.RunID, "no running run: no run id")
	// The session's own launch stays.
	assert.Equal(t, "implementer", opts.AgentProfile)
	assert.Equal(t, "/work/run-7", opts.Workdir)
	assert.Equal(t, "implementer", opts.Role)
	assert.Empty(t, opts.LaunchProfile)
	// kind=agent arms the idle-after-done nudge, with the task's window.
	assert.Equal(t, 45*time.Second, resolveIdleNudgeWindow(opts))

	// A running run is the session's run.
	runID, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-SRC-TASK", Executor: "cli", Status: sqlstore.RunStatusRunning})
	require.NoError(t, err)
	_, err = store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-SRC-TASK", Executor: "cli", Status: sqlstore.RunStatusDone})
	require.NoError(t, err)
	assert.Equal(t, runID, mgr.sourceBootOptions(rec).RunID)
}

// Without a readable task the re-launch keeps what it had, the bare task id
// (and none of the task fields), rather than failing; a session with no task
// is unchanged.
func TestSourceBootOptions_NoReadableTask(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	defer store.Close()
	mgr := NewManager(&Dependencies{Store: store, StateWriter: writeq.NewDirect(store)})

	opts := mgr.sourceBootOptions(&sqlstore.SessionRecord{
		AgentProfile: "worker", Workdir: "/w", TaskID: sql.NullString{String: "CW-GONE", Valid: true},
		ProjectID: sql.NullString{String: "PRJ-1", Valid: true}, MetaJSON: `{"role":"planner"}`,
	})
	assert.Equal(t, Options{Mode: ModeLongLived, AgentProfile: "worker", Workdir: "/w", TaskID: "CW-GONE", ProjectID: "PRJ-1", Role: "planner"}, opts)

	opts = mgr.sourceBootOptions(&sqlstore.SessionRecord{AgentProfile: "worker", Workdir: "/w"})
	assert.Equal(t, Options{Mode: ModeLongLived, AgentProfile: "worker", Workdir: "/w"}, opts)
	assert.Zero(t, resolveIdleNudgeWindow(opts), "no task kind: no nudge")
}

// A resume's diagnostic note (or request prompt) goes ahead of the task's
// system prompt, not in place of it.
func TestPrependSystemPrompt(t *testing.T) {
	assert.Equal(t, "note\n\ntask prompt", prependSystemPrompt("note", "task prompt"))
	assert.Equal(t, "task prompt", prependSystemPrompt("", "task prompt"))
	assert.Equal(t, "note", prependSystemPrompt("note", ""))
	assert.Equal(t, "", prependSystemPrompt("", ""))
}

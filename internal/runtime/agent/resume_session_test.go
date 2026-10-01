package agent

import (
	"bytes"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// title, description, kind, status, priority, relationships, system prompt and
// environment, so the planted task bundle and the kickoff are not blank. The
// session's own profile, workdir and role stay, and it is not given the task's
// running run: only the scheduler's dispatch carries a run id.
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
	// The session's own launch stays.
	assert.Equal(t, "implementer", opts.AgentProfile)
	assert.Equal(t, "/work/run-7", opts.Workdir)
	assert.Equal(t, "implementer", opts.Role)
	assert.Empty(t, opts.LaunchProfile)

	// A running run is the scheduler's dispatch, not this session's: a boot
	// that carries a run id is one the scheduler dispatched, and a re-launch
	// is not the run's worker, so it stays 0.
	_, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-SRC-TASK", Executor: "cli", Status: sqlstore.RunStatusRunning})
	require.NoError(t, err)
	assert.EqualValues(t, 0, mgr.sourceBootOptions(rec).RunID, "even with a running run")
}

// The row's project wins; a row with none inherits the task's.
func TestSourceBootOptions_ProjectInheritance(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	defer store.Close()
	mgr := NewManager(&Dependencies{Store: store, StateWriter: writeq.NewDirect(store)})
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-TASK", Name: "task project"}))
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-ROW", Name: "row project"}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PRJ-TASK", Title: "t", Priority: 2, ProjectID: sql.NullString{String: "PRJ-TASK", Valid: true},
	}))
	rec := &sqlstore.SessionRecord{AgentProfile: "w", Workdir: "/w", TaskID: sql.NullString{String: "CW-PRJ-TASK", Valid: true}}

	assert.Equal(t, "PRJ-TASK", mgr.sourceBootOptions(rec).ProjectID, "inherited from the task")
	rec.ProjectID = sql.NullString{String: "PRJ-ROW", Valid: true}
	assert.Equal(t, "PRJ-ROW", mgr.sourceBootOptions(rec).ProjectID, "the row's own project wins")
}

// A task's agent file that cannot be loaded (missing, or relative with no
// workdir to anchor it) is dropped with a log line instead of failing the
// re-launch: a resume exists so the task is not stranded. A loadable one is
// carried.
func TestSourceBootOptions_AgentFileDegrades(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	defer store.Close()
	mgr := NewManager(&Dependencies{Store: store, StateWriter: writeq.NewDirect(store)})
	good := filepath.Join(t.TempDir(), "agent.yaml")
	require.NoError(t, os.WriteFile(good, []byte("name: a\nsystem_prompt: Be careful.\n"), 0o644))

	for _, tc := range []struct {
		name, agentFile, workdir, want string
	}{
		{"loadable", good, "/w", good},
		{"missing", "/nonexistent/agent.yaml", "/w", ""},
		{"relative, no workdir to anchor it", "agent.yaml", "", ""},
		{"no system_prompt", writeAgentFile(t, "name: a\n"), "/w", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })
			id := "CW-AF-" + strings.ReplaceAll(tc.name, " ", "-")
			require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: id, Title: "kept", Priority: 2, AgentFile: tc.agentFile}))
			opts := mgr.sourceBootOptions(&sqlstore.SessionRecord{
				ID: "SES-AF", AgentProfile: "w", Workdir: tc.workdir, TaskID: sql.NullString{String: id, Valid: true},
			})
			assert.Equal(t, tc.want, opts.AgentFile)
			assert.Equal(t, "kept", opts.TaskTitle, "the rest of the task is still carried")
			if tc.want == "" {
				assert.Contains(t, logs.String(), "agent file cannot be loaded, booting without it")
			} else {
				assert.NotContains(t, logs.String(), "agent file")
			}
		})
	}
}

func writeAgentFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agent.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
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

// A boot that continues a provider conversation does not repeat the task's
// description as a "## First turn": the conversation already holds the task,
// and a continuation could read it as "restart the task". A fresh boot does,
// and an explicit one-shot prompt always does.
func TestKickoffFirstTurn_ContinuationOmitsTheDescription(t *testing.T) {
	task := Options{TaskID: "CW-T", Description: "Summarize the README."}

	assert.Contains(t, kickoffFirstTurn(task, continuesConversation(task)), "## First turn\n\nSummarize the README.")

	resumed := task
	resumed.ProviderSessionIDOverride = "ses_prior"
	assert.NotContains(t, kickoffFirstTurn(resumed, continuesConversation(resumed)), "Summarize the README.")
	assert.NotContains(t, kickoffFirstTurn(resumed, continuesConversation(resumed)), "## First turn")

	checkpoint := task
	checkpoint.Mode = ModeResume
	assert.NotContains(t, kickoffFirstTurn(checkpoint, continuesConversation(checkpoint)), "Summarize the README.")

	resumed.OneShotPrompt = "Answer the operator."
	assert.Contains(t, kickoffFirstTurn(resumed, continuesConversation(resumed)), "## First turn\n\nAnswer the operator.")
	resumed.SystemPrompt = "NOTE: be careful."
	assert.Contains(t, kickoffFirstTurn(resumed, continuesConversation(resumed)), "## Task framing\n\nNOTE: be careful.", "the framing stays")
}

// An ACP session keeps the description as its first turn even when it is
// resumed: an agent that does not advertise loadSession starts a new session
// without saying so, and would otherwise boot with no task description.
func TestACPKickoff_ResumedKeepsTheDescription(t *testing.T) {
	resumed := Options{TaskID: "CW-T", Description: "Summarize the README.", ProviderSessionIDOverride: "ses_prior"}
	assert.Contains(t, acpKickoff(resumed, "worker", nil, true), "## First turn\n\nSummarize the README.")
	assert.NotContains(t, kickoffMarkdown(resumed, "worker", false), "Summarize the README.", "a native resume does not repeat it")
}

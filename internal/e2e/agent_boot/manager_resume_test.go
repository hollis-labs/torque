package agent_boot

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/harness/adapters/providertest"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// Manager.Resume, the HTTP and MCP session resume, makes the same decision
// as ResumeSession (CW-20261001-0203): it continues the checkpoint's
// provider conversation only when there is an id, the profile still boots
// the runtime that stored it, and Torque wires that runtime's resume.
// Otherwise it boots fresh, long-lived, with the kickoff. The new session's
// Resumed, as Get returns it, says which.

// resumeSourceTask is the task the planted source sessions are bound to, with
// the title and body its planted task bundle must carry on a re-launch.
const (
	resumeSourceTask  = "CW-MGR-RESUME-SRC"
	resumeSourceTitle = "Summarize the README"
	resumeSourceBody  = "Read README.md and comment a three line summary on this task."
)

// assertPlantedTaskBundle checks a re-launched session's planted bundle holds
// the task itself (CW-20261001-0249): task.md carries its title and body, and
// task.json its kind and status, as a normal dispatch plants them.
func assertPlantedTaskBundle(t *testing.T, bootDir string) {
	t.Helper()
	dir := filepath.Join(bootDir, "tasks", resumeSourceTask)
	md, err := os.ReadFile(filepath.Join(dir, "task.md"))
	require.NoError(t, err, "the task bundle is planted in %s", bootDir)
	assert.Contains(t, string(md), resumeSourceTitle, "task.md carries the title")
	assert.Contains(t, string(md), resumeSourceBody, "task.md carries the body")
	raw, err := os.ReadFile(filepath.Join(dir, "task.json"))
	require.NoError(t, err)
	var ctx struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Kind        string `json:"kind"`
		Status      string `json:"status"`
		Priority    int    `json:"priority"`
	}
	require.NoError(t, json.Unmarshal(raw, &ctx), string(raw))
	assert.Equal(t, resumeSourceTitle, ctx.Title)
	assert.Equal(t, resumeSourceBody, ctx.Description)
	assert.Equal(t, "agent", ctx.Kind)
	assert.Equal(t, "doing", ctx.Status)
	assert.Equal(t, 3, ctx.Priority)
}

// plantResumeCheckpoint stores a session row booted by provider under
// agentProfile, bound to resumeSourceTask with the "implementer" role, and a
// checkpoint of it holding hint ("" for none).
func plantResumeCheckpoint(t *testing.T, store *sqlstore.Store, sessID, provider, agentProfile, hint string) {
	t.Helper()
	if _, err := store.GetTask(resumeSourceTask); err != nil {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: resumeSourceTask, Title: resumeSourceTitle, Description: resumeSourceBody,
			Kind: "agent", Status: "doing", Priority: 3,
		}))
	}
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID: sessID, AgentProfile: agentProfile, Provider: provider,
		RuntimeID: "torque-cli/" + provider, RuntimeKind: "cli",
		Workdir: t.TempDir(), State: "done", MetaJSON: `{"role":"implementer"}`,
		TaskID: sql.NullString{String: resumeSourceTask, Valid: true},
	}))
	require.NoError(t, store.CreateSessionCheckpoint(&sqlstore.SessionCheckpointRecord{
		ID: "SCP-" + ulid.Make().String(), SessionID: sessID,
		Payload: `{}`, ResumeHint: []byte(hint), Note: "manager resume test",
	}))
}

// codex app-server's resume is not wired (CW-20261001-0180): a checkpoint
// with a thread id boots fresh, and the kickoff fires on a new thread. It
// used to boot with the id the app-server ignores and skip the kickoff, so
// the resumed session sat silent.
func TestManagerResume_Codex_FreshBootWithKickoff(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{
		JsonRpcResponses: map[string]json.RawMessage{
			"thread/start": json.RawMessage(`{"thread": {"id": "thread-fresh-2"}}`),
		},
	}, "codex")
	plantResumeCheckpoint(t, cd.Store, "SES-MGR-RESUME-CODEX", "codex", "torque-backend", "codex-thread-old")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	newID, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: "SES-MGR-RESUME-CODEX"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), newID) })

	assert.Nil(t, cd.Runtime.sessionIDPreset.Load(), "no provider id is passed to a runtime that ignores it")
	var methods []string
	for _, c := range cd.Runtime.lastSession().recordedJsonRpcCalls() {
		methods = append(methods, c.Method)
	}
	assert.Contains(t, methods, "thread/start")
	assert.Contains(t, methods, "turn/start", "the kickoff fires")
	assert.NotContains(t, methods, "thread/resume")

	sess, err := cd.Manager.Get(newID)
	require.NoError(t, err)
	assert.False(t, sess.Resumed)
	assert.Equal(t, agent.ModeLongLived, sess.Mode)
	assert.Equal(t, resumeSourceTask, sess.TaskID, "the fresh session is the source task's, not an unlinked one")
	assertPlantedTaskBundle(t, sess.BootDir)
}

// claude-code resumes: the checkpoint's id reaches the CLI as --resume, and
// the session reports Resumed.
func TestManagerResume_ClaudeCode_ResumesTheCheckpointsSession(t *testing.T) {
	const hint = "00000000-0000-4000-8000-000000000039"
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/stream_resume"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", PermissionMode: "acceptEdits"}}
	plantResumeCheckpoint(t, cd.Store, "SES-MGR-RESUME-CLAUDE", "claude-code", "worker", hint)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	newID, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: "SES-MGR-RESUME-CLAUDE"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), newID) })

	_ = firstCall(t, fake)
	got, ok := fake.Call(0).ArgAfter("--resume")
	require.True(t, ok, "claude must be launched with --resume: %v", fake.Call(0).Args)
	assert.Equal(t, hint, got)
	assertStrictOnce(t, fake.Call(0).Args)

	sess, err := cd.Manager.Get(newID)
	require.NoError(t, err)
	assert.True(t, sess.Resumed)
	assert.Equal(t, agent.ModeLongLived, sess.Mode, "a resume is a long-lived boot, as ResumeSession's is")
	assert.Equal(t, resumeSourceTask, sess.TaskID)

	// The conversation already holds the task: the kickoff does not repeat its
	// description as a first turn, which a continuation could read as "restart
	// the task". The planted task.md and task.json keep it in full.
	boot, err := os.ReadFile(filepath.Join(sess.BootDir, "boot.md"))
	require.NoError(t, err)
	assert.Contains(t, string(boot), resumeSourceTask)
	assert.NotContains(t, string(boot), resumeSourceBody)
	assert.NotContains(t, string(boot), "## First turn")
	assertPlantedTaskBundle(t, sess.BootDir)
}

// Without a stored id, or when the profile now boots another runtime than
// the one that stored it, the resume boots fresh with the kickoff, as the
// source session's task, project and role: on both claude runtime kinds.
func TestManagerResume_NoIDOrOtherRuntime_FreshBootWithKickoff(t *testing.T) {
	for _, kind := range []string{"", "subprocess"} {
		for _, tc := range []struct{ name, recordedBy, hint string }{
			{"no stored id", "claude-code", ""},
			{"the profile now boots claude-code; opencode stored the id", "opencode", "ses_from_opencode"},
		} {
			name := kind
			if name == "" {
				name = "streaming-stdio"
			}
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				fake := providertest.New(t, runtimes.Claude, claudeFreshRun(kind))
				fake.Install()
				cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
				cd.Deps.RuntimeFactory = nil
				cd.Deps.Profiles = config.ProfileMap{"worker": {
					Executor: "cli", Provider: "claude-code", RuntimeKind: kind, PermissionMode: "acceptEdits",
				}}
				plantResumeCheckpoint(t, cd.Store, "SES-MGR-RESUME-FRESH", tc.recordedBy, "worker", tc.hint)

				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				newID, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: "SES-MGR-RESUME-FRESH"})
				require.NoError(t, err)
				t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), newID) })

				_ = waitCalls(t, fake, 1)
				assert.False(t, fake.Call(0).HasArg("--resume"), "a fresh boot: %v", fake.Call(0).Args)

				sess, err := cd.Manager.Get(newID)
				require.NoError(t, err)
				assert.False(t, sess.Resumed)
				assert.Equal(t, agent.ModeLongLived, sess.Mode)
				assert.Equal(t, resumeSourceTask, sess.TaskID, "the fresh session is the source task's")
				// The task bundle planted for the session names the task and the
				// source session's role; an unlinked session planted none.
				assertPlantedTaskBundle(t, sess.BootDir)
				boot, err := os.ReadFile(filepath.Join(sess.BootDir, "boot.md"))
				require.NoError(t, err)
				assert.Contains(t, string(boot), "## First turn\n\n"+resumeSourceBody, "a fresh boot is given the task as its first turn")
				bundle, err := os.ReadFile(filepath.Join(sess.BootDir, "tasks", resumeSourceTask, "task.md"))
				require.NoError(t, err)
				assert.Contains(t, string(bundle), "implementer", "the source session's role")
			})
		}
	}
}

// claudeFreshRun is the fake claude run for a fresh boot of the given kind.
func claudeFreshRun(kind string) providertest.Run {
	if kind == "subprocess" {
		return providertest.Replay("claude/print_turn1")
	}
	return providertest.Script(providertest.AwaitEOF())
}

// A checkpoint whose provider session is gone: the resume's first turn fails
// with a lost session (claude: "No conversation found"), and Resume boots
// fresh once, with the kickoff, rather than failing. A subprocess-per-turn
// Boot runs the kickoff turn, so the loss is seen before it returns.
func TestManagerResume_LostProviderSession_BootsFreshOnce(t *testing.T) {
	const lostID = "00000000-0000-4000-8000-0000000000ff"
	fake := providertest.New(t, runtimes.Claude,
		providertest.Replay("claude/print_resume_unknown_id").When("--resume"),
		providertest.Replay("claude/print_turn1"),
	)
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {
		Executor: "cli", Provider: "claude-code", RuntimeKind: "subprocess", PermissionMode: "acceptEdits",
	}}
	plantResumeCheckpoint(t, cd.Store, "SES-MGR-RESUME-LOST", "claude-code", "worker", lostID)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	newID, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: "SES-MGR-RESUME-LOST"})
	require.NoError(t, err, "a lost provider session boots fresh rather than failing the resume")
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), newID) })

	_ = waitCalls(t, fake, 2)
	got, ok := fake.Call(0).ArgAfter("--resume")
	require.True(t, ok)
	assert.Equal(t, lostID, got, "the resume was tried first")
	assert.False(t, fake.Call(1).HasArg("--resume"), "then a fresh boot: %v", fake.Call(1).Args)
	assert.Len(t, fake.Calls(), 2, "once")

	sess, err := cd.Manager.Get(newID)
	require.NoError(t, err)
	assert.False(t, sess.Resumed)
	assert.Equal(t, resumeSourceTask, sess.TaskID)
	// The fresh boot is given the task as its first turn: it has no conversation
	// that holds it, so the description is not omitted as it is on a resume.
	boot, err := os.ReadFile(filepath.Join(sess.BootDir, "boot.md"))
	require.NoError(t, err)
	assert.Contains(t, string(boot), "## First turn\n\n"+resumeSourceBody)
}

// The real flow, with nothing planted by hand: a session boots and reports
// its provider session id, Checkpoint records it, and Resume launches the
// CLI with it (claude --resume <id>). A checkpoint written before checkpoints
// carried the id falls back to the source session's own.
func TestManagerResume_RealCheckpointThenResume_ThreadsTheID(t *testing.T) {
	const capturedID = "00000000-0000-4000-8000-000000000001" // go-providers' claude print_turn1
	for _, legacyCheckpoint := range []bool{false, true} {
		name := "checkpoint records the id"
		if legacyCheckpoint {
			name = "legacy checkpoint without one"
		}
		t.Run(name, func(t *testing.T) {
			fake := providertest.New(t, runtimes.Claude,
				providertest.Replay("claude/print_turn1"),
				providertest.Replay("claude/print_turn2_resume").When("--resume"),
			)
			fake.Install()
			cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
			cd.Deps.RuntimeFactory = nil
			cd.Deps.Profiles = config.ProfileMap{"worker": {
				Executor: "cli", Provider: "claude-code", RuntimeKind: "subprocess", PermissionMode: "acceptEdits",
			}}
			require.NoError(t, cd.Store.CreateTask(&sqlstore.TaskRecord{ID: "CW-MGR-REAL", Title: "real checkpoint", Priority: 2}))

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			const firstID = "SES-MGR-REAL"
			first, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID: "CW-MGR-REAL", AgentProfile: "worker", Workdir: t.TempDir(),
				Mode: agent.ModeLongLived, IDFn: func() string { return firstID },
			})
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				rec, err := cd.Store.GetSession(firstID)
				return err == nil && string(rec.ResumeHint) == capturedID
			}, 5*time.Second, 20*time.Millisecond, "the first turn's provider session id is stored")

			if legacyCheckpoint {
				require.NoError(t, cd.Store.CreateSessionCheckpoint(&sqlstore.SessionCheckpointRecord{
					ID: "SCP-" + ulid.Make().String(), SessionID: first.ID, Payload: `{}`, Note: "written before checkpoints carried an id",
				}))
			} else {
				cp, err := cd.Manager.Checkpoint(agent.CheckpointRequest{SessionID: first.ID, Payload: `{}`, Note: "real"})
				require.NoError(t, err)
				assert.Equal(t, capturedID, string(cp.ResumeHint), "the checkpoint records the provider conversation")
				rec, err := cd.Store.GetSessionCheckpoint(cp.ID)
				require.NoError(t, err)
				assert.Equal(t, capturedID, string(rec.ResumeHint), "and persists it")
			}
			require.NoError(t, cd.Manager.Stop(context.Background(), first.ID))

			newID, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: first.ID})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), newID) })

			_ = waitCalls(t, fake, 2)
			got, ok := fake.Call(1).ArgAfter("--resume")
			require.True(t, ok, "the resumed launch carries --resume: %v", fake.Call(1).Args)
			assert.Equal(t, capturedID, got)
			sess, err := cd.Manager.Get(newID)
			require.NoError(t, err)
			assert.True(t, sess.Resumed)
			assert.Equal(t, "CW-MGR-REAL", sess.TaskID)
		})
	}
}

// A pi ACP boot handed a provider session id loads it (session/load) and the
// session is marked resumed; one without is not. The ACP boot path stamps
// the same torque.resumed meta the other paths do.
func TestBootPiACP_ResumedMetaFollowsTheSessionIDPreset(t *testing.T) {
	fake := providertest.New(t, runtimes.Pi, providertest.Replay("pi/acp_resume"))
	fake.Install()
	cd := composeACPDeps(t, "pi")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeOneShot, Description: "carry on",
		ProviderSessionIDOverride: "00000000-0000-4000-8000-000000000001",
	})
	require.NoError(t, err)
	assert.True(t, sess.Resumed)
	assert.Equal(t, "true", sess.Meta["torque.resumed"])
	got, err := cd.Manager.Get(sess.ID)
	require.NoError(t, err)
	assert.True(t, got.Resumed, "and it reads back from the row")
}

// ResumeSession's fresh boot (the HITL and stuck-task path) is handed its
// task the same way: the planted bundle is not blank. A diagnostic note goes
// ahead of the task's own system prompt in the planted CLAUDE.md.
func TestResumeSession_FreshBoot_PlantsTheTask(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, claudeFreshRun(""))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", PermissionMode: "acceptEdits"}}
	plantResumeCheckpoint(t, cd.Store, "SES-RESUME-TASKCTX", "claude-code", "worker", "")
	task, err := cd.Store.GetTask(resumeSourceTask)
	require.NoError(t, err)
	require.NoError(t, cd.Store.UpdateTask(task.ID, sqlstore.TaskUpdate{SystemPrompt: ptr("Be brief.")}))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-TASKCTX", agent.ResumeOptions{DiagnosticNote: "NOTE: the previous turn went silent."})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	assert.False(t, sess.Resumed)
	assertPlantedTaskBundle(t, sess.BootDir)
	claudeMD, err := os.ReadFile(filepath.Join(sess.BootDir, "CLAUDE.md"))
	require.NoError(t, err)
	note, brief := strings.Index(string(claudeMD), "NOTE: the previous turn went silent."), strings.Index(string(claudeMD), "Be brief.")
	require.NotEqual(t, -1, note, "the diagnostic note is planted")
	require.NotEqual(t, -1, brief, "so is the task's own system prompt")
	assert.Less(t, note, brief, "the note comes first")
}

func ptr[T any](v T) *T { return &v }

// resumeFreshClaude boots a fresh (no recorded id) claude Manager.Resume or
// ResumeSession against a task built by mutate, and returns the fake CLI's
// first launch and the new session.
func freshClaudeRelaunch(t *testing.T, mutate func(*sqlstore.TaskRecord), relaunch func(*composedDeps, context.Context) (string, error)) (*providertest.Fake, *agent.Session) {
	t.Helper()
	fake := providertest.New(t, runtimes.Claude, claudeFreshRun(""))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", PermissionMode: "acceptEdits"}}
	task := &sqlstore.TaskRecord{
		ID: resumeSourceTask, Title: resumeSourceTitle, Description: resumeSourceBody,
		Kind: "agent", Status: "doing", Priority: 3,
	}
	mutate(task)
	require.NoError(t, cd.Store.CreateTask(task))
	plantResumeCheckpoint(t, cd.Store, "SES-RELAUNCH", "claude-code", "worker", "")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	id, err := relaunch(cd, ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), id) })
	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)
	sess, err := cd.Manager.Get(id)
	require.NoError(t, err)
	return fake, sess
}

// The request's env wins over the task's, the task's other env still reaches
// the CLI, and the request's system prompt goes ahead of the task's own in the
// planted prompt (neither replaces the other).
func TestManagerResume_RequestEnvAndSystemPromptPrecedence(t *testing.T) {
	fake, sess := freshClaudeRelaunch(t,
		func(task *sqlstore.TaskRecord) {
			task.SystemPrompt = "TASK PROMPT: be brief."
			task.Environment = sql.NullString{String: `{"RELAUNCH_ENV":"from-task","RELAUNCH_TASK_ONLY":"task-only"}`, Valid: true}
		},
		func(cd *composedDeps, ctx context.Context) (string, error) {
			return cd.Manager.Resume(ctx, agent.ResumeRequest{
				SessionID: "SES-RELAUNCH", SystemPrompt: "REQUEST PROMPT: be careful.",
				Env: []string{"RELAUNCH_ENV=from-request"},
			})
		})

	env := fake.Call(0).Env
	assert.Contains(t, env, "RELAUNCH_ENV=from-request", "the request's env wins")
	assert.NotContains(t, env, "RELAUNCH_ENV=from-task")
	assert.Contains(t, env, "RELAUNCH_TASK_ONLY=task-only", "the task's other env is carried")

	claudeMD, err := os.ReadFile(filepath.Join(sess.BootDir, "CLAUDE.md"))
	require.NoError(t, err)
	req, task := strings.Index(string(claudeMD), "REQUEST PROMPT"), strings.Index(string(claudeMD), "TASK PROMPT")
	require.NotEqual(t, -1, req, "the request's prompt is planted")
	require.NotEqual(t, -1, task, "and so is the task's")
	assert.Less(t, req, task, "the request's prompt comes first")
}

// A request that moves the session to another runtime than the one that
// recorded it does not hand the task's environment to the other provider's
// CLI: it was set for the original's. The request's own env still goes.
func TestManagerResume_ProviderOverrideDoesNotCarryTheTaskEnv(t *testing.T) {
	fake := providertest.New(t, runtimes.OpenCode, providertest.Replay("opencode/run_turn1"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "opencode")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{
		"worker": {Executor: "cli", Provider: "claude-code", PermissionMode: "acceptEdits"},
		"oc":     {Executor: "cli", Provider: "opencode"},
	}
	require.NoError(t, cd.Store.CreateTask(&sqlstore.TaskRecord{
		ID: resumeSourceTask, Title: resumeSourceTitle, Description: resumeSourceBody, Kind: "agent", Status: "doing", Priority: 3,
		Environment: sql.NullString{String: `{"CLAUDE_ONLY_ENV":"for-claude"}`, Valid: true},
	}))
	plantResumeCheckpoint(t, cd.Store, "SES-OVERRIDE", "claude-code", "worker", "")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	id, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: "SES-OVERRIDE", AgentProfile: "oc", Env: []string{"REQUEST_ENV=1"}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), id) })
	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)

	env := fake.Call(0).Env
	assert.NotContains(t, env, "CLAUDE_ONLY_ENV=for-claude", "the task's env is not handed to the other provider's CLI")
	assert.Contains(t, env, "REQUEST_ENV=1", "the request's own env still goes")
}

// A task's agent file that cannot be loaded does not fail the re-launch: the
// session boots without it, as it did before the task was carried.
func TestResumeSession_UnloadableAgentFileDegrades(t *testing.T) {
	_, sess := freshClaudeRelaunch(t,
		func(task *sqlstore.TaskRecord) { task.AgentFile = "/nonexistent/agent.yaml" },
		func(cd *composedDeps, ctx context.Context) (string, error) {
			s, err := cd.Manager.ResumeSession(ctx, "SES-RELAUNCH", agent.ResumeOptions{})
			if err != nil {
				return "", err
			}
			return s.ID, nil
		})
	assertPlantedTaskBundle(t, sess.BootDir)
}

// A re-launched session is not the run's worker: with the task's run still
// running, it carries no run id: not in its env, its meta or its planted
// task.json (only the scheduler's dispatch carries one).
func TestRelaunch_CarriesNoRunID(t *testing.T) {
	for name, relaunch := range map[string]func(*composedDeps, context.Context) (string, error){
		"Manager.Resume": func(cd *composedDeps, ctx context.Context) (string, error) {
			return cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: "SES-RELAUNCH"})
		},
		"ResumeSession": func(cd *composedDeps, ctx context.Context) (string, error) {
			s, err := cd.Manager.ResumeSession(ctx, "SES-RELAUNCH", agent.ResumeOptions{})
			if err != nil {
				return "", err
			}
			return s.ID, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var fake *providertest.Fake
			var sess *agent.Session
			fake, sess = freshClaudeRelaunch(t,
				func(*sqlstore.TaskRecord) {},
				func(cd *composedDeps, ctx context.Context) (string, error) {
					// The task has a run in flight, as it does while its dispatched worker runs.
					_, err := cd.Store.CreateRun(&sqlstore.RunRecord{TaskID: resumeSourceTask, Executor: "cli", Status: sqlstore.RunStatusRunning})
					require.NoError(t, err)
					return relaunch(cd, ctx)
				})
			assert.Contains(t, fake.Call(0).Env, "TORQUE_RUN_ID=0")
			assert.NotContains(t, sess.Meta, "torque.run_id")
			raw, err := os.ReadFile(filepath.Join(sess.BootDir, "tasks", resumeSourceTask, "task.json"))
			require.NoError(t, err)
			var ctx struct {
				RunID int64 `json:"run_id"`
			}
			require.NoError(t, json.Unmarshal(raw, &ctx))
			assert.EqualValues(t, 0, ctx.RunID)
		})
	}
}

// lostStreamingRun is a claude streaming-stdio CLI resumed with an id it does
// not have: it reads the first user turn, reports the lost conversation in a
// result frame and on stderr, and exits 1 (go-providers' captured
// claude/stream_resume_unknown_id, with its recv widened to any first turn,
// since Torque's kickoff is its own text).
func lostStreamingRun(t *testing.T) providertest.Run {
	t.Helper()
	steps := providertest.FixtureSteps(t, "claude/stream_resume_unknown_id")
	require.NotEmpty(t, steps)
	return providertest.Script(append([]providertest.Step{providertest.RecvLine()}, steps[1:]...)...).When("--resume")
}

// A streaming-stdio resume whose provider session is gone (the CLI exits on
// its first turn): with agentkit v0.21.1 the loss is a typed error, which
// reaches the one fresh-boot fallback ResumeSession and Manager.Resume share:
// one fresh boot with the kickoff, Resumed=false, and no loop.
func TestStreamingLostSession_BootsFreshOnce(t *testing.T) {
	const lostID = "00000000-0000-4000-8000-0000000000ff"
	for name, relaunch := range map[string]func(*composedDeps, context.Context) (*agent.Session, error){
		"ResumeSession": func(cd *composedDeps, ctx context.Context) (*agent.Session, error) {
			return cd.Manager.ResumeSession(ctx, "SES-STREAM-LOST", agent.ResumeOptions{})
		},
		"Manager.Resume": func(cd *composedDeps, ctx context.Context) (*agent.Session, error) {
			id, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: "SES-STREAM-LOST"})
			if err != nil {
				return nil, err
			}
			return cd.Manager.Get(id)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := providertest.New(t, runtimes.Claude, lostStreamingRun(t), providertest.Script(providertest.AwaitEOF()))
			fake.ExpectErrors()
			fake.Install()
			cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
			cd.Deps.RuntimeFactory = nil
			cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", PermissionMode: "acceptEdits"}}
			plantResumeCheckpoint(t, cd.Store, "SES-STREAM-LOST", "claude-code", "worker", lostID)
			// ResumeSession reads the id off the session row, Manager.Resume off the checkpoint.
			require.NoError(t, cd.Store.UpdateSessionResumeHint("SES-STREAM-LOST", []byte(lostID)))

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			sess, err := relaunch(cd, ctx)
			require.NoError(t, err, "a lost streaming session boots fresh rather than failing the resume")
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

			var calls []providertest.Call
			require.Eventually(t, func() bool {
				calls = fake.Calls()
				return len(calls) >= 2 && len(calls[0].Args) > 0 && len(calls[1].Args) > 0
			}, 5*time.Second, 20*time.Millisecond, "both launches record their argv")
			got, ok := calls[0].ArgAfter("--resume")
			require.True(t, ok, "the resume was tried first: %v", calls[0].Args)
			assert.Equal(t, lostID, got)
			assert.False(t, calls[1].HasArg("--resume"), "then one fresh boot: %v", calls[1].Args)
			assert.Len(t, fake.Calls(), 2, "once, no loop")
			assert.False(t, sess.Resumed)
			assert.Equal(t, resumeSourceTask, sess.TaskID)
		})
	}
}

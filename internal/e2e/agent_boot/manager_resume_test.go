package agent_boot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
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

// plantResumeCheckpoint stores a session row booted by provider under
// agentProfile and a checkpoint of it holding hint ("" for none).
func plantResumeCheckpoint(t *testing.T, store *sqlstore.Store, sessID, provider, agentProfile, hint string) {
	t.Helper()
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID: sessID, AgentProfile: agentProfile, Provider: provider,
		RuntimeID: "torque-cli/" + provider, RuntimeKind: "cli",
		Workdir: t.TempDir(), State: "done", MetaJSON: "{}",
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

	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)
	got, ok := fake.Call(0).ArgAfter("--resume")
	require.True(t, ok, "claude must be launched with --resume: %v", fake.Call(0).Args)
	assert.Equal(t, hint, got)

	sess, err := cd.Manager.Get(newID)
	require.NoError(t, err)
	assert.True(t, sess.Resumed)
	assert.Equal(t, agent.ModeResume, sess.Mode)

	// A resumed session waits for its next turn rather than taking the
	// kickoff; the operator's turn continues the conversation.
	require.NoError(t, cd.Manager.SendTurn(ctx, sess, "carry on"))
	require.Eventually(t, func() bool { return len(fake.Call(0).Stdin) > 0 }, 5*time.Second, 20*time.Millisecond, "the turn reaches the resumed CLI")
}

// Without a stored id, or when the profile now boots another runtime than
// the one that stored it, the resume boots fresh with the kickoff.
func TestManagerResume_NoIDOrOtherRuntime_FreshBootWithKickoff(t *testing.T) {
	for _, tc := range []struct{ name, recordedBy, hint string }{
		{"no stored id", "claude-code", ""},
		{"the profile now boots claude-code; opencode stored the id", "opencode", "ses_from_opencode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/print_turn1"))
			fake.Install()
			cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
			cd.Deps.RuntimeFactory = nil
			cd.Deps.Profiles = config.ProfileMap{"worker": {
				Executor: "cli", Provider: "claude-code", RuntimeKind: "subprocess", PermissionMode: "acceptEdits",
			}}
			plantResumeCheckpoint(t, cd.Store, "SES-MGR-RESUME-FRESH", tc.recordedBy, "worker", tc.hint)

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			newID, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: "SES-MGR-RESUME-FRESH"})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), newID) })

			require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond,
				"the kickoff runs a first turn")
			assert.False(t, fake.Call(0).HasArg("--resume"), "a fresh boot: %v", fake.Call(0).Args)

			sess, err := cd.Manager.Get(newID)
			require.NoError(t, err)
			assert.False(t, sess.Resumed)
			assert.Equal(t, agent.ModeLongLived, sess.Mode)
		})
	}
}

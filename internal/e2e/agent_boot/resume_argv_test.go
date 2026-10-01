package agent_boot

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/service"
)

// A resume Torque claims reaches the agent CLI (CW-20261001-0174): on the
// production wrapper path, ResumeSession's stored provider session id is
// rendered into the CLI's resume argument by the launch template.

// claude-code (streaming-stdio) is spawned with --resume <id>; the fake
// replays go-providers' captured resumed stream.
func TestResumeSession_ClaudeCode_LaunchesWithResumeArg(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/stream_resume"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", PermissionMode: "acceptEdits"}}

	const providerSessionID = "00000000-0000-4000-8000-000000000039"
	plantSessionForResume(t, cd.Store, "SES-RESUME-ARGV-CLAUDE", "claude-code", providerSessionID, t.TempDir(), "worker")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-ARGV-CLAUDE", agent.ResumeOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)
	got, ok := fake.Call(0).ArgAfter("--resume")
	require.True(t, ok, "claude must be launched with --resume: %v", fake.Call(0).Args)
	assert.Equal(t, providerSessionID, got)
	assertStrictOnce(t, fake.Call(0).Args)
}

// opencode run (subprocess-per-turn) is spawned with --session <id>.
func TestResumeSession_Opencode_LaunchesWithSessionArg(t *testing.T) {
	fake := providertest.New(t, runtimes.OpenCode, providertest.Replay("opencode/run_tool_use"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "opencode")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode", PermissionMode: "acceptEdits"}}

	const providerSessionID = "ses_resume_opencode_1"
	plantSessionForResume(t, cd.Store, "SES-RESUME-ARGV-OPENCODE", "opencode", providerSessionID, t.TempDir(), "worker")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-ARGV-OPENCODE", agent.ResumeOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)
	got, ok := fake.Call(0).ArgAfter("--session")
	require.True(t, ok, "opencode run must be launched with --session: %v", fake.Call(0).Args)
	assert.Equal(t, providerSessionID, got)
}

// A session booted fresh stores the provider session id its first turn
// reports, and ResumeSession hands that id back to the CLI: nothing is
// planted by hand. claude-code and opencode run, both subprocess-per-turn.
func TestResumeSession_FreshBootThenResume_ThreadsTheCapturedID(t *testing.T) {
	for _, tc := range []struct {
		name, provider, turn1, turn2, resumeFlag, capturedID string
		runtime                                              runtimes.ID
	}{
		{"claude-code", "claude-code", "claude/print_turn1", "claude/print_turn2_resume", "--resume", fixtureSessionID(t, "claude/print_turn1"), runtimes.Claude},
		{"opencode run", "opencode", "opencode/run_turn1", "opencode/run_turn2_resume", "--session", fixtureSessionID(t, "opencode/run_turn1"), runtimes.OpenCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := providertest.New(t, tc.runtime,
				providertest.Replay(tc.turn1),
				providertest.Replay(tc.turn2).When(tc.resumeFlag),
			)
			fake.Install()
			cd := composeDeps(t, fakeRuntimeConfig{}, tc.provider)
			cd.Deps.RuntimeFactory = nil
			cd.Deps.Profiles = config.ProfileMap{"worker": {
				Executor: "cli", Provider: tc.provider, RuntimeKind: "subprocess", PermissionMode: "acceptEdits",
			}}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			const firstID = "SES-FRESH-THEN-RESUME"
			first, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID: "CW-FRESH-THEN-RESUME", AgentProfile: "worker", Workdir: t.TempDir(),
				Mode: agent.ModeLongLived, IDFn: func() string { return firstID },
			})
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				rec, err := cd.Store.GetSession(firstID)
				return err == nil && string(rec.ResumeHint) == tc.capturedID
			}, 5*time.Second, 20*time.Millisecond, "the first turn's provider session id is stored")
			require.NoError(t, cd.Manager.Stop(context.Background(), first.ID))
			assert.False(t, fake.Call(0).HasArg(tc.resumeFlag), "the first boot is fresh")

			sess, err := cd.Manager.ResumeSession(ctx, firstID, agent.ResumeOptions{})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
			assert.True(t, sess.Resumed)

			require.Eventually(t, func() bool { return len(fake.Calls()) > 1 }, 5*time.Second, 20*time.Millisecond)
			got, ok := fake.Call(1).ArgAfter(tc.resumeFlag)
			require.True(t, ok, "the resumed launch carries %s: %v", tc.resumeFlag, fake.Call(1).Args)
			assert.Equal(t, tc.capturedID, got)
		})
	}
}

// A resume whose provider no longer has the session fails its first turn
// (claude: "No conversation found with session ID", exit 1, a
// SessionLostError). ResumeSession then boots fresh once, with the
// kickoff, and reports that it did not resume.
func TestResumeSession_LostProviderSession_BootsFreshOnce(t *testing.T) {
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
	plantSessionForResume(t, cd.Store, "SES-RESUME-LOST", "claude-code", lostID, t.TempDir(), "worker")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-LOST", agent.ResumeOptions{})
	require.NoError(t, err, "a lost provider session boots fresh rather than failing the resume")
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	assert.False(t, sess.Resumed)

	require.Eventually(t, func() bool { return len(fake.Calls()) > 1 }, 5*time.Second, 20*time.Millisecond)
	got, ok := fake.Call(0).ArgAfter("--resume")
	require.True(t, ok)
	assert.Equal(t, lostID, got, "the resume was tried first")
	assert.False(t, fake.Call(1).HasArg("--resume"), "then a fresh boot: %v", fake.Call(1).Args)
	assert.Len(t, fake.Calls(), 2, "once")
}

// The HITL response breadcrumb reports what happened: after a lost provider
// session the dispatcher's resume booted fresh, so used_resume is false.
func TestCheckpointDispatch_LostProviderSession_RecordsNoResume(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude,
		providertest.Replay("claude/print_resume_unknown_id").When("--resume"),
		providertest.Replay("claude/print_turn1"),
		providertest.Replay("claude/print_turn2_resume"),
	)
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {
		Executor: "cli", Provider: "claude-code", RuntimeKind: "subprocess", PermissionMode: "acceptEdits",
	}}
	const taskID = "CW-HITL-LOST"
	require.NoError(t, cd.Store.CreateSession(&sqlstore.SessionRecord{
		ID: "SES-HITL-LOST", AgentProfile: "worker", Provider: "claude-code", RuntimeKind: "subprocess",
		Workdir: t.TempDir(), State: "done", TaskID: sql.NullString{String: taskID, Valid: true},
	}))
	require.NoError(t, cd.Store.UpdateSessionResumeHint("SES-HITL-LOST", []byte("00000000-0000-4000-8000-0000000000ff")))

	d := bootstrap.NewCheckpointResponseDispatcher(cd.Store, cd.Manager)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, d.DispatchResponse(ctx, service.CheckpointResponseDispatch{
		TaskID: taskID, CorrelationID: "01HK_LOST", ResponseJSON: `{"answer":"go"}`,
	}))
	t.Cleanup(func() {
		recs, _ := cd.Store.ListSessions(sqlstore.SessionFilter{TaskID: taskID})
		for _, r := range recs {
			_ = cd.Manager.Stop(context.Background(), r.ID)
		}
	})

	events, err := cd.Store.ListRunEvents(sqlstore.RunEventFilter{TaskID: taskID, Types: []string{"checkpoint.response_dispatched"}})
	require.NoError(t, err)
	require.Len(t, events, 1)
	var payload struct {
		UsedResume bool   `json:"used_resume"`
		Error      string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(events[0].Payload), &payload))
	assert.Empty(t, payload.Error)
	assert.False(t, payload.UsedResume, "the provider had lost the session, so the dispatch booted fresh")
}

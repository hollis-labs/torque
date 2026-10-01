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

// Resuming a provider session the CLI has lost, on claude-code's
// streaming-stdio runtime (CW-20261001-0202). Boot used to return as soon as
// the process was up; the kickoff then failed ("No conversation found with
// session ID", exit 1) and the resumed worker was reported Resumed=true and
// ended `failed`. Boot now judges the resume before it returns and fails
// with provider.ErrProviderSessionLost, so ResumeSession boots fresh once.

const streamingKickoff = "Boot @./boot.md"

// streamingProfile is claude-code on its default (streaming-stdio) runtime.
func streamingProfile() config.ProfileMap {
	return config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", PermissionMode: "acceptEdits"}}
}

// freshStreamingBoot is a fresh claude streaming session: it answers its
// first frame (the kickoff) with a result and stays up.
func freshStreamingBoot() providertest.Run {
	return providertest.Script(
		providertest.RecvLine(),
		providertest.Send(`{"type":"result","subtype":"success","is_error":false,"session_id":"00000000-0000-4000-8000-0000000000aa","result":"ok"}`),
		providertest.AwaitEOF(),
	)
}

func TestResumeSession_StreamingLostProviderSession_BootsFreshOnce(t *testing.T) {
	const lostID = "00000000-0000-4000-8000-0000000000ff"
	fake := providertest.New(t, runtimes.Claude,
		providertest.Replay("claude/stream_resume_unknown_id").When("--resume"),
		freshStreamingBoot(),
	)
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = streamingProfile()
	plantSessionForResume(t, cd.Store, "SES-STREAM-LOST", "claude-code", lostID, t.TempDir(), "worker")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := cd.Manager.ResumeSession(ctx, "SES-STREAM-LOST", agent.ResumeOptions{})
	require.NoError(t, err, "a lost provider session boots fresh rather than failing the resume")
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	assert.False(t, sess.Resumed, "the fresh boot did not resume")
	assert.Equal(t, string(agent.RuntimeKindStreamingStdio), sess.RuntimeKind)

	require.Eventually(t, func() bool { return len(fake.Calls()) == 2 && len(fake.Call(1).Stdin) >= 1 }, 5*time.Second, 20*time.Millisecond)
	got, ok := fake.Call(0).ArgAfter("--resume")
	require.True(t, ok, "the resume was tried first: %v", fake.Call(0).Args)
	assert.Equal(t, lostID, got)
	assert.False(t, fake.Call(1).HasArg("--resume"), "then a fresh boot: %v", fake.Call(1).Args)
	assert.Contains(t, fake.Call(1).Stdin[0], streamingKickoff, "the fresh boot delivers the kickoff")
	time.Sleep(300 * time.Millisecond)
	assert.Len(t, fake.Calls(), 2, "once")
}

// The HITL response breadcrumb reports what happened: the dispatcher's
// resume booted fresh, so used_resume is false.
func TestCheckpointDispatch_StreamingLostProviderSession_RecordsNoResume(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude,
		providertest.Replay("claude/stream_resume_unknown_id").When("--resume"),
		freshStreamingBoot(),
	)
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = streamingProfile()
	const taskID = "CW-HITL-STREAM-LOST"
	require.NoError(t, cd.Store.CreateSession(&sqlstore.SessionRecord{
		ID: "SES-HITL-STREAM-LOST", AgentProfile: "worker", Provider: "claude-code", RuntimeKind: "streaming-stdio",
		Workdir: t.TempDir(), State: "done", TaskID: sql.NullString{String: taskID, Valid: true},
	}))
	require.NoError(t, cd.Store.UpdateSessionResumeHint("SES-HITL-STREAM-LOST", []byte("00000000-0000-4000-8000-0000000000ff")))

	d := bootstrap.NewCheckpointResponseDispatcher(cd.Store, cd.Manager)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, d.DispatchResponse(ctx, service.CheckpointResponseDispatch{
		TaskID: taskID, CorrelationID: "01HK_STREAM_LOST", ResponseJSON: `{"answer":"go"}`,
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

// A healthy streaming resume is unchanged: one launch with --resume, the
// session reported as resumed, no second boot.
func TestResumeSession_StreamingHealthyResume_StaysResumed(t *testing.T) {
	const providerSessionID = "00000000-0000-4000-8000-000000000039"
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/stream_resume"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = streamingProfile()
	plantSessionForResume(t, cd.Store, "SES-STREAM-HEALTHY", "claude-code", providerSessionID, t.TempDir(), "worker")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := cd.Manager.ResumeSession(ctx, "SES-STREAM-HEALTHY", agent.ResumeOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	assert.True(t, sess.Resumed)

	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)
	got, ok := fake.Call(0).ArgAfter("--resume")
	require.True(t, ok)
	assert.Equal(t, providerSessionID, got)
	time.Sleep(300 * time.Millisecond)
	assert.Len(t, fake.Calls(), 1, "a healthy resume is not retried")
}

// Only the CLI's own "session lost" is retried. A resume that fails some
// other way (here a rejected login) is reported as before: Boot returns, the
// session is Resumed and ends failed, and nothing boots again.
func TestResumeSession_StreamingOtherFailure_IsNotRetried(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Script(
		providertest.RecvLine(),
		providertest.Send(`{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"00000000-0000-4000-8000-0000000000ff","result":""}`),
		providertest.Stderr("Invalid API key · Please run /login"),
		providertest.Exit(1),
	))
	fake.ExpectErrors()
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = streamingProfile()
	plantSessionForResume(t, cd.Store, "SES-STREAM-OTHER", "claude-code", "00000000-0000-4000-8000-0000000000ff", t.TempDir(), "worker")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := cd.Manager.ResumeSession(ctx, "SES-STREAM-OTHER", agent.ResumeOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	assert.True(t, sess.Resumed)
	time.Sleep(300 * time.Millisecond)
	assert.Len(t, fake.Calls(), 1, "an unrelated failure is not a lost session")
}

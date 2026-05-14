package agent_boot

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// TestBoot_ModeLongLived_AutoFiresFirstTurn validates the long-lived
// lifecycle: AutoFireFirstTurn drives the kickoff (Boot must NOT call
// SendInput separately — that was the gap CW-20260507-0011 patched and
// CW-20260508-0001 generalized via go-agent-sessions v0.6.0's flag).
func TestBoot_ModeLongLived_AutoFiresFirstTurn(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-LL-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// StartOptions assertions.
	require.True(t, cd.Runtime.autoFireFirstTurn.Load(),
		"ModeLongLived must set StartOptions.AutoFireFirstTurn=true")
	payload := cd.Runtime.firstTurnPayload.Load()
	require.NotNil(t, payload, "FirstTurnPayload must be populated for AutoFire")
	assert.NotEmpty(t, *payload, "FirstTurnPayload must be non-empty")
	assert.Contains(t, string(*payload), "Boot @./boot.md",
		"kickoff payload must follow the Boot @./boot.md convention")

	// SessionMeta should carry Mode + BootDir for the long-lived hooks.
	rec, err := cd.Store.GetSession(sess.ID)
	require.NoError(t, err)
	assert.Contains(t, rec.MetaJSON, "long_lived",
		"torque.mode meta must record the long-lived lifecycle")

	// Boot must NOT have called SendInput separately. The lib's runtime is
	// responsible for the AutoFireFirstTurn delivery; Boot's contract is just
	// to set the StartOptions flag.
	fakeSess := cd.Runtime.lastSession()
	require.NotNil(t, fakeSess)
	assert.Equal(t, int32(0), fakeSess.recordedSendInputCount(),
		"Boot must NOT call SendInput separately for ModeLongLived (AutoFireFirstTurn handles it)")

	// Workspace dir was wired through.
	require.NotNil(t, cd.Runtime.workspaceDir.Load(), "WorkspaceDir must be set")
}

// TestBoot_ModeOneShot_SyncTurn validates the scheduler-dispatched
// executor lifecycle: SendInput fires synchronously, the session is stopped
// before Boot returns, and the per-task boot dir is cleaned inline.
func TestBoot_ModeOneShot_SyncTurn(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: false}, "claude")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:        "CW-TEST-OS-001",
		AgentProfile:  "torque-backend",
		Workdir:       t.TempDir(),
		Mode:          agent.ModeOneShot,
		OneShotPrompt: "do the thing",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// AutoFireFirstTurn must stay false on the captured request — OneShot
	// drives SendInput synchronously below.
	assert.False(t, cd.Runtime.autoFireFirstTurn.Load(),
		"ModeOneShot must leave StartOptions.AutoFireFirstTurn=false")

	// SendInput called exactly once with the OneShot prompt.
	fakeSess := cd.Runtime.lastSession()
	require.NotNil(t, fakeSess)
	assert.Equal(t, int32(1), fakeSess.recordedSendInputCount(),
		"OneShot must drive SendInput once with the kickoff body")
	payloads := fakeSess.recordedSendInputs()
	require.Len(t, payloads, 1)
	assert.Equal(t, "do the thing", string(payloads[0]),
		"Boot's OneShot path forwards Options.OneShotPrompt verbatim as the kickoff")

	// Session is no longer running. Boot's OneShot path drives Stop+Wait
	// inline, and WaitSession's return implies the watch goroutine recorded
	// the terminal state via storeStateSink.
	rec, err := cd.Store.GetSession(sess.ID)
	require.NoError(t, err)
	assert.NotEqual(t, "running", rec.State, "OneShot session must not stay running after Boot")
	assert.NotEqual(t, "launching", rec.State, "OneShot session must not stay launching after Boot")

	// Boot dir was cleaned inline (defer in Boot's OneShot branch).
	_, statErr := os.Stat(sess.BootDir)
	assert.True(t, os.IsNotExist(statErr),
		"OneShot must os.RemoveAll the boot dir inline; got stat err %v", statErr)
}

// TestBoot_ModeOneShot_TimeoutFallsThrough validates the new turn-complete
// wait path's ctx.Done() fall-through: when the runtime never emits a
// turn-complete signal (long-lived adapter hung, binary failed to flush its
// final stream-json `done` event, etc), the select must surface a timeout
// instead of waiting indefinitely. The executor wraps ctx in
// context.WithTimeout(profile.TimeoutSeconds) at its callsite — here we
// shortcut by handing Boot a pre-cancelled ctx, which is the limit case.
// Status must be Failed so the executor records the turn as not-done.
func TestBoot_ModeOneShot_TimeoutFallsThrough(t *testing.T) {
	cd := composeDeps(t,
		fakeRuntimeConfig{PTY: false, SuppressTurnDoneOnSendInput: true},
		"claude")

	// 250ms ctx — enough for Boot's setup (workspace + plant + Start +
	// SendInput) to complete, then ctx.Done() fires inside the
	// turn-complete select. No hardcoded 5s grace anymore; the fall-through
	// path is bounded by ctx, not a per-Boot constant.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:        "CW-TEST-OS-TIMEOUT",
		AgentProfile:  "torque-backend",
		Workdir:       t.TempDir(),
		Mode:          agent.ModeOneShot,
		OneShotPrompt: "trigger turn-complete wait timeout",
	})
	require.NoError(t, err, "Boot itself returns success even on timeout — Status carries the failure signal")
	require.NotNil(t, sess)

	assert.Equal(t, agent.StatusFailed, sess.Status,
		"ModeOneShot with no turn-complete and a fired ctx must surface Status=Failed")

	// SendInput still fired (the fake records it before the suppress
	// branch); the no-turn-complete is downstream of the send.
	fakeSess := cd.Runtime.lastSession()
	require.NotNil(t, fakeSess)
	assert.Equal(t, int32(1), fakeSess.recordedSendInputCount(),
		"SendInput must fire even when the runtime never signals turn-complete")
}

// TestBoot_ModeSubagent_StampsParent validates the nested-session lifecycle:
// ParentSessionID is required (Validate rejects empty), and the parent ID
// is stamped on the session row's metadata so Get/List can recover the
// relationship.
func TestBoot_ModeSubagent_StampsParent(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("missing ParentSessionID rejected", func(t *testing.T) {
		_, err := cd.Manager.Boot(ctx, agent.Options{
			TaskID:       "CW-TEST-SA-MISS",
			AgentProfile: "torque-backend",
			Workdir:      t.TempDir(),
			Mode:         agent.ModeSubagent,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, agent.ErrParentSessionRequired)
	})

	t.Run("happy path stamps parent on session row", func(t *testing.T) {
		sess, err := cd.Manager.Boot(ctx, agent.Options{
			TaskID:          "CW-TEST-SA-OK",
			AgentProfile:    "torque-backend",
			Workdir:         t.TempDir(),
			Mode:            agent.ModeSubagent,
			ParentSessionID: "SES-PARENT-X",
		})
		require.NoError(t, err)
		require.NotNil(t, sess)
		assert.Equal(t, "SES-PARENT-X", sess.ParentSessionID,
			"Boot's *Session.ParentSessionID must echo Options.ParentSessionID")

		// Roundtrip via the manager — sessionFromRecord must recover the
		// parent from the substrate-stamped MetaJSON.
		got, err := cd.Manager.Get(sess.ID)
		require.NoError(t, err)
		assert.Equal(t, "SES-PARENT-X", got.ParentSessionID,
			"Manager.Get must recover ParentSessionID from torque.parent_session_id meta")

		// AutoFireFirstTurn fires for ModeSubagent (per boot.go's autoFire matrix).
		assert.True(t, cd.Runtime.autoFireFirstTurn.Load(),
			"ModeSubagent must set AutoFireFirstTurn=true (parity with LongLived)")
	})
}

// TestBoot_ModeBackground_ReturnsImmediately validates that the background
// lifecycle returns once Manager.Start has accepted the request. The
// kickoff fires async via AutoFireFirstTurn; the call should not wait on
// the first turn to complete.
func TestBoot_ModeBackground_ReturnsImmediately(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-BG-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeBackground,
	})
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// 200ms covers slow CI without diluting the "returns immediately" intent
	// — fakeRuntime.Start is synchronous and inert; even with sqlstore writes
	// + bootdir planting on top, the call should land well under this.
	assert.Less(t, elapsed, 200*time.Millisecond,
		"ModeBackground must return immediately (got %v)", elapsed)

	// AutoFireFirstTurn drives the kickoff asynchronously per the
	// per-Mode matrix in boot.go.
	assert.True(t, cd.Runtime.autoFireFirstTurn.Load(),
		"ModeBackground must set AutoFireFirstTurn=true for async kickoff")
}

// TestBoot_ModeResume_LoadsCheckpoint validates the resume lifecycle: a
// previously-persisted checkpoint's ResumeHint feeds StartOptions.SessionIDPreset
// so the adapter can issue --resume <id> on its first turn.
func TestBoot_ModeResume_LoadsCheckpoint(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude")

	const providerSessionID = "claude-session-abc-123"
	cpID := plantCheckpoint(t, cd.Store, "SES-PARENT-RESUME", providerSessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:               "CW-TEST-RES-001",
		AgentProfile:         "torque-backend",
		Workdir:              t.TempDir(),
		Mode:                 agent.ModeResume,
		ResumeFromCheckpoint: cpID,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// SessionIDPreset must thread the checkpoint's ResumeHint bytes verbatim.
	preset := cd.Runtime.sessionIDPreset.Load()
	require.NotNil(t, preset, "Resume must populate StartOptions.SessionIDPreset")
	assert.Equal(t, providerSessionID, *preset,
		"Resume must thread the checkpoint's ResumeHint into StartOptions.SessionIDPreset")

	// Resume's first-turn delivery is driven by the adapter's --resume flag,
	// not AutoFireFirstTurn. Per boot.go's autoFire matrix, ModeResume leaves
	// AutoFireFirstTurn=false.
	assert.False(t, cd.Runtime.autoFireFirstTurn.Load(),
		"ModeResume must leave AutoFireFirstTurn=false (resume path uses SessionIDPreset)")

	// Session row exists in launching state — the row's resume_hint column
	// is NOT populated at Boot time; it gets stamped via OnSessionID once
	// the adapter observes the live provider session_id mid-turn (mediated
	// by Manager.UpdateSessionResumeHint). Boot's contract is to feed
	// SessionIDPreset into StartOptions, asserted above.
	rec, err := cd.Store.GetSession(sess.ID)
	require.NoError(t, err)
	assert.Equal(t, sess.ID, rec.ID)
}

// TestBoot_ModeResume_MissingCheckpointRejected validates the validation
// surface: ModeResume without a ResumeFromCheckpoint is rejected by
// Validate before any side effects.
func TestBoot_ModeResume_MissingCheckpointRejected(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude")

	_, err := cd.Manager.Boot(context.Background(), agent.Options{
		TaskID:       "CW-TEST-RES-MISS",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeResume,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, agent.ErrResumeCheckpointRequired)
}

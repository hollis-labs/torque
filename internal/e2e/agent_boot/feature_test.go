package agent_boot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-sandbox/sandbox"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// TestBoot_PIDReporter validates the agent.Manager.LivePID surface against
// a session driven through its full lifecycle. The fakeSession implements
// PIDReporter directly (LivePID resets to 0 once Stop fires; LastPID is
// retained for log-correlation parity with subprocess-per-turn semantics).
func TestBoot_PIDReporter(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-PID-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// While alive, LivePID > 0.
	livePID := cd.Manager.LivePID(sess.ID)
	assert.Greater(t, livePID, 0, "LivePID must be > 0 while session alive")

	// Drive the session's underlying lifecycle to completion.
	require.NoError(t, cd.Manager.Stop(ctx, sess.ID))
	require.Eventually(t, func() bool {
		return cd.Manager.LivePID(sess.ID) == 0
	}, time.Second, 10*time.Millisecond,
		"LivePID must drop to 0 after Stop fires")

	// LastPID parity — fakeSession retains the spawned pid.
	fakeSess := cd.Runtime.lastSession()
	require.NotNil(t, fakeSess)
	assert.Greater(t, fakeSess.LastPID(), 0,
		"LastPID must retain the most-recently-spawned pid post-Stop")
}

// TestBoot_TypedEventCallback_FiresOnPTYPath validates that Boot wires
// Options.TypedEventCallback through to StartOptions.TypedEventCallback so
// the PTY runtime can fan typed events out to the caller. The fakeRuntime
// captures the callback and the test fires it synthetically.
func TestBoot_TypedEventCallback_FiresOnPTYPath(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hits := make(chan events.Event, 4)
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-TEC-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
		TypedEventCallback: func(ev events.Event) {
			hits <- ev
		},
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Boot must have wired the callback through.
	assert.True(t, cd.Runtime.typedEventCallbackSet.Load(),
		"Boot must forward Options.TypedEventCallback into StartOptions.TypedEventCallback")

	// Fire a synthetic event via the fake — the captured callback should
	// receive it.
	cd.Runtime.simulateTypedEvent(events.Delta{Text: "hi", Phase: "narration"})

	select {
	case got := <-hits:
		delta, ok := got.(events.Delta)
		require.True(t, ok, "got wrong event type %T", got)
		assert.Equal(t, "hi", delta.Text)
	case <-time.After(time.Second):
		t.Fatal("TypedEventCallback was not invoked within 1s of simulateTypedEvent")
	}
}

// TestBoot_SupervisorWiresOnPTYPath validates that Options.Supervisor is
// forwarded into StartOptions.Supervisor on the PTY path with all fields
// preserved. PTY runtime enforces these natively (idle-kill, restart-on-crash,
// watchdog) per go-agent-sessions v0.6.0.
func TestBoot_SupervisorWiresOnPTYPath(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := &agentsessions.SupervisorOptions{
		IdleKill:          30 * time.Second,
		RestartOnCrash:    3,
		MaxRestartBackoff: 30 * time.Second,
	}
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-SUP-PTY-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
		Supervisor:   sup,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	require.True(t, cd.Runtime.supervisorPresent.Load(),
		"PTY-path Boot must forward Supervisor into StartOptions")

	got := cd.Runtime.lastStartOpts.Load()
	require.NotNil(t, got)
	require.NotNil(t, got.Supervisor)
	assert.Equal(t, 30*time.Second, got.Supervisor.IdleKill,
		"Supervisor.IdleKill must round-trip verbatim")
	assert.Equal(t, 3, got.Supervisor.RestartOnCrash,
		"Supervisor.RestartOnCrash must round-trip verbatim")
	assert.Equal(t, 30*time.Second, got.Supervisor.MaxRestartBackoff,
		"Supervisor.MaxRestartBackoff must round-trip verbatim")
}

// TestBoot_SupervisorPassesThroughOnAdapterPath validates the pass-through
// contract on Caps.PTY=false: Boot still forwards Supervisor into
// StartOptions even though the lib's adapter runtime currently silently
// ignores it (pending go-runner v0.3.x). Setting it now means pickup is
// automatic when the lib unblocks; no caller-side changes required.
//
// This is pass-through verification, NOT enforcement — the lib does not
// drive idle-kill / restart-on-crash on the adapter runtime in v0.6.0.
func TestBoot_SupervisorPassesThroughOnAdapterPath(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: false}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sup := &agentsessions.SupervisorOptions{IdleKill: 60 * time.Second, RestartOnCrash: 2}
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:        "CW-TEST-SUP-AD-001",
		AgentProfile:  "torque-backend",
		Workdir:       t.TempDir(),
		Mode:          agent.ModeOneShot,
		OneShotPrompt: "x",
		Supervisor:    sup,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	require.True(t, cd.Runtime.supervisorPresent.Load(),
		"adapter-path Boot must STILL forward Supervisor (pass-through verification)")
	got := cd.Runtime.lastStartOpts.Load()
	require.NotNil(t, got)
	require.NotNil(t, got.Supervisor)
	assert.Equal(t, 60*time.Second, got.Supervisor.IdleKill)
	assert.Equal(t, 2, got.Supervisor.RestartOnCrash)
}

// TestBoot_ExitErrorCausePropagation validates that a structured *ExitError
// returned by Session.Wait propagates through agent.Manager.Wait so consumers
// can classify supervised terminations via errors.As. The exit code reaches
// callers, the session row transitions to state=failed via storeStateSink,
// and the *ExitError surfaces from Manager.Wait carrying .Cause / .Signal /
// .Killed for recovery-broker / post-mortem hook classification.
//
// Per go-agent-sessions v0.7.0: Manager.WaitSession propagates Session.Wait's
// error verbatim instead of discarding it; *ExitError is errors.As-extractable.
func TestBoot_ExitErrorCausePropagation(t *testing.T) {
	xe := &agentsessions.ExitError{
		Code:   137,
		Signal: 9,
		Killed: true,
		Cause:  agentsessions.CauseWatchdogKill,
	}
	cd := composeDeps(t, fakeRuntimeConfig{
		PTY:         true,
		WaitExitErr: xe,
	}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-EXIT-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Drive natural termination (NOT Manager.Stop — that sets killing=true
	// which forces state=done regardless of exit code, defeating the test).
	cd.Runtime.lastSession().simulateExit()
	code, waitErr := cd.Manager.Wait(ctx, sess.ID)

	// v0.7.0: Manager.Wait propagates the structured ExitError. Both the
	// exit code and the structured error reach the caller verbatim.
	assert.Equal(t, 137, code, "ExitError.Code must reach consumers via Manager.Wait")
	require.Error(t, waitErr, "supervised termination must surface a non-nil error")
	var got *agentsessions.ExitError
	require.True(t, errors.As(waitErr, &got),
		"Manager.Wait error must errors.As to *ExitError for supervised terminations")
	assert.Equal(t, agentsessions.CauseWatchdogKill, got.Cause,
		"ExitError.Cause must classify the supervisor-driven termination")
	assert.Equal(t, 9, got.Signal, "ExitError.Signal must round-trip")
	assert.True(t, got.Killed, "ExitError.Killed must round-trip")
	assert.Equal(t, 137, got.Code, "ExitError.Code must round-trip")

	// Code 137 → state=failed via the lib's watch + storeStateSink.
	require.Eventually(t, func() bool {
		rec, err := cd.Store.GetSession(sess.ID)
		return err == nil && rec.State == "failed"
	}, time.Second, 10*time.Millisecond,
		"non-zero exit must transition the session row to state=failed")
}

// TestBoot_SandboxAllowLoopback validates that Boot force-sets
// Profile.AllowLoopback=true on any caller-supplied SandboxProfile. The
// per-task MCP loopback URL lives on 127.0.0.1; without AllowLoopback the
// sandboxed process can't reach it.
func TestBoot_SandboxAllowLoopback(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// AllowLoopback intentionally NOT set on input — Boot must force it true.
	profile := &sandbox.Profile{ID: "test-profile", Description: "test"}
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:         "CW-TEST-SAND-001",
		AgentProfile:   "torque-backend",
		Workdir:        t.TempDir(),
		Mode:           agent.ModeLongLived,
		SandboxProfile: profile,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.True(t, cd.Runtime.allowLoopback.Load(),
		"Boot must force StartOptions.Profile.AllowLoopback=true when SandboxProfile is set")

	got := cd.Runtime.lastStartOpts.Load()
	require.NotNil(t, got)
	assert.Equal(t, "test-profile", got.Profile.ID,
		"non-loopback Profile fields must round-trip from caller's SandboxProfile")
}

package agent_boot

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
)

// TestPidPoller_RecordsLivePIDChanges drives a long-lived adapter session
// through a sequence of synthetic PID transitions (mirrors the
// subprocess-per-turn lib path: pid=N during a turn, 0 between turns) and
// asserts the DB row + Manager.LivePID surface track the live value.
//
// Reproduces CW-20260509-0008 in the failure mode (pid=0 throughout the
// session lifetime) and verifies the per-session poller closes the gap.
func TestPidPoller_RecordsLivePIDChanges(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: false}, "claude")
	cd.Manager.WithPidPollInterval(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-PID-POLLER-001",
		AgentProfile: "clockwork-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	fake := cd.Runtime.lastSession()
	require.NotNil(t, fake)

	// Three synthetic turns: each bumps the live PID, the poller observes,
	// the row's pid catches up. lastPID >= 0 between turns is expected
	// (sqlstore COALESCEs the pid=0 write) — the row keeps the most-recent
	// non-zero pid for sweep liveness.
	turnPIDs := []int{4001, 4002, 4003}
	for _, pid := range turnPIDs {
		fake.setPID(pid)
		require.Eventually(t, func() bool {
			rec, err := cd.Store.GetSession(sess.ID)
			return err == nil && rec.PID == pid
		}, time.Second, 10*time.Millisecond,
			"row.PID must reach %d within poller cadence", pid)
		// Drop to 0 between turns; the row's pid stays at the last live value.
		fake.setPID(0)
		// Manager.LivePID reads Health() — returns 0 between turns.
		require.Eventually(t, func() bool {
			return cd.Manager.LivePID(sess.ID) == 0
		}, time.Second, 10*time.Millisecond,
			"LivePID must drop to 0 between turns")
	}
}

// TestPidPoller_BumpsLastActivity asserts that the poller advances
// last_activity even when the PID has not changed (mid-turn or between
// turns). Without this, dashboards see a frozen last_activity for the
// entire orchestrator run.
func TestPidPoller_BumpsLastActivity(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: false}, "claude")
	cd.Manager.WithPidPollInterval(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-PID-POLLER-002",
		AgentProfile: "clockwork-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)

	rec0, err := cd.Store.GetSession(sess.ID)
	require.NoError(t, err)
	la0 := rec0.LastActivity

	require.Eventually(t, func() bool {
		rec, err := cd.Store.GetSession(sess.ID)
		return err == nil && rec.LastActivity.After(la0)
	}, time.Second, 10*time.Millisecond,
		"poller must bump last_activity even when PID is unchanged")
}

// TestPidPoller_TerminalStateOnAliveFalse asserts that when Health.Alive
// flips false (orchestrator finished, claude exited cleanly with no
// explicit Stop), the poller drives a clean Stop so the watch goroutine
// records done — not crashed-via-next-daemon-restart-sweep.
//
// Direct repro of the cosmetic regression in CW-20260509-0008.
func TestPidPoller_TerminalStateOnAliveFalse(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: false}, "claude")
	cd.Manager.WithPidPollInterval(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-PID-POLLER-003",
		AgentProfile: "clockwork-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)

	fake := cd.Runtime.lastSession()
	require.NotNil(t, fake)
	// simulateExit flips dead=true (so Alive=false on next Health()) and
	// closes done so the watch goroutine can drain.
	fake.simulateExit()

	// Watch goroutine writes state=done (via Manager.Stop which sets
	// killing=true → StateDone regardless of exit code).
	require.Eventually(t, func() bool {
		rec, err := cd.Store.GetSession(sess.ID)
		return err == nil && rec.State == "done"
	}, 2*time.Second, 10*time.Millisecond,
		"Alive=false must drive state=done within poller cadence + Stop drain")
}

// TestPidPoller_NoGoroutineLeak asserts the poller goroutine exits cleanly
// when the session terminates. A leak here would compound across every
// long-lived session a daemon spawns over its lifetime.
func TestPidPoller_NoGoroutineLeak(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: false}, "claude")
	cd.Manager.WithPidPollInterval(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		sess, err := cd.Manager.Boot(ctx, agent.Options{
			TaskID:       "CW-TEST-PID-POLLER-LEAK",
			AgentProfile: "clockwork-backend",
			Workdir:      t.TempDir(),
			Mode:         agent.ModeLongLived,
		})
		require.NoError(t, err)
		require.NoError(t, cd.Manager.Stop(ctx, sess.ID))
	}

	// Allow the poller goroutines a brief grace window to exit + watch
	// goroutines to drain.
	require.Eventually(t, func() bool {
		// Allow up to 2x the boot count in transient stack depth (test
		// substrate threads, etc.) — the test fails only on a clear leak.
		return runtime.NumGoroutine() <= before+5
	}, 2*time.Second, 25*time.Millisecond,
		"poller goroutines must drain after Stop; before=%d after=%d", before, runtime.NumGoroutine())
}

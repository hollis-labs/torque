package agent

import (
	"context"
	"time"

	"github.com/hollis-labs/go-agent-sessions/agentsessions"
)

// defaultPidPollInterval is the per-session poller cadence in production.
// Tunable via Manager.WithPidPollInterval for tests; 5s is a balance between
// "DB row reflects mid-turn PID within bounded latency for sweep + dashboard"
// and "no observable QPS on the sessions table during idle multi-orchestrator
// fleets". CW-20260509-0008.
const defaultPidPollInterval = 5 * time.Second

// pidPoller observes the lib's live Health() and:
//   - writes (state=running, pid) to the store when the live PID changes (so
//     a fresh `running` row records the actual subprocess pid for the orphan
//     sweep + dashboard);
//   - touches last_activity otherwise (so the row reflects liveness even
//     between turns when no subprocess is mid-flight);
//   - exits + drives a clean Stop the moment Health.Alive flips false.
//
// The poller is the only mechanism that bumps last_activity / pid for
// long-lived adapter sessions: the lib's adapterSession sets s.pid inside
// runner.EventProcessStarted but nothing propagates that back to the
// StateSink between turns.
//
// Returns a closer that signals the goroutine to exit and blocks until it
// has returned. Closer is idempotent.
func startPidPoller(mgr *Manager, sessID string, interval time.Duration) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go pollPid(mgr, sessID, interval, stop, done)
	closed := false
	return func() {
		if closed {
			return
		}
		closed = true
		close(stop)
		<-done
	}
}

func pollPid(mgr *Manager, sessID string, interval time.Duration, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastPID int
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}

		snap, ok := mgr.inner.Health(sessID)
		if !ok {
			// Session no longer registered: the lib's watch goroutine has
			// already torn down. teardownSession will fire the closer; just
			// exit.
			return
		}

		if !snap.Health.Alive {
			// Lib reports the session is dead but no terminal state has been
			// recorded (orchestrator finished, claude exited, lib hasn't yet
			// observed Wait). Drive a clean Stop so the watch goroutine
			// records done/failed via StateSink — converts the "row stays
			// running until next daemon-restart sweep marks it crashed"
			// regression from CW-20260509-0008.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = mgr.inner.Stop(ctx, sessID)
			cancel()
			return
		}

		if mgr.deps == nil || mgr.deps.Store == nil {
			continue
		}

		pid := snap.Health.PID
		if pid > 0 && pid != lastPID {
			// New subprocess (turn started). Write running+pid; this is the
			// only path that records the actual claude pid for the orphan
			// sweep's syscall.Kill(pid, 0) liveness check.
			if err := mgr.deps.UpdateSessionState(context.Background(), sessID, string(agentsessions.StateRunning), pid, nil); err == nil {
				lastPID = pid
			}
			continue
		}
		// Same pid (mid-turn) or pid=0 (idle between turns). Bump last_activity
		// so dashboards see the row is alive. UpdateSessionState with pid=0
		// does not overwrite the stored pid (sqlstore COALESCE behavior), so
		// the most-recent live pid is preserved for the sweep liveness check.
		_ = mgr.deps.TouchSession(context.Background(), sessID)
	}
}

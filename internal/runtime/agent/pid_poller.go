package agent

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
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
			// already torn down. teardownSession will fire the closer.
			//
			// CW-20260519-0082 defensive write: the lib's recordState
			// drops StateSink errors on the floor (manager.go:272), so a
			// transient DB lock / write-queue stall during the terminal
			// transition can leave the row stuck at running/launching.
			// Reconcile here before the goroutine exits — if the row
			// still claims to be non-terminal, force it to `failed` so
			// planstart.Redispatch's idempotency check sees an honest
			// state and a fresh /plans/start doesn't 409 against a
			// stranded row.
			reconcileTerminalRow(mgr, sessID)
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
		//
		// touchSessionUnlessFrozen suppresses the heartbeat write when the
		// stream fanout has observed an error / auth-dead frame and no
		// content-bearing event has lifted the gate yet (CW-20260519-0130).
		// Monitors then see last_activity stall to reflect real liveness
		// instead of the bare wrapper-process heartbeat.
		mgr.touchSessionUnlessFrozen(context.Background(), sessID)
	}
}

// reconcileTerminalRow forces a non-terminal session row to `failed` when
// the lib has unregistered the session but the StateSink write was lost.
// Idempotent — a row already in a terminal state is left alone. Defensive
// against the lib's recordState swallowing StateSink errors
// (CW-20260519-0082): without this, a transient write failure during the
// terminal transition strands the row forever, blocking the operator's
// /plans/start retry with 409 and the redispatch hook's idempotency check
// with a phantom "already-orchestrating" verdict.
func reconcileTerminalRow(mgr *Manager, sessID string) {
	if mgr == nil || mgr.deps == nil || mgr.deps.Store == nil {
		return
	}
	rec, err := mgr.deps.Store.GetSession(sessID)
	if err != nil {
		if errors.Is(err, sqlstore.ErrSessionNotFound) {
			return
		}
		return
	}
	if Status(rec.State).Terminal() {
		return
	}
	exit := -1
	_ = mgr.deps.UpdateSessionState(context.Background(), sessID, string(StatusFailed), 0, &exit)
}

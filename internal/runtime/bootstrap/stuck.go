package bootstrap

import (
	"context"
	"fmt"
	"log"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/reactor"
	"github.com/hollis-labs/torque/internal/runtime/stuck"
)

// StuckWatcher wires the stuck-probe trigger (CW-20260518-0043, messaging
// epic A) into the running daemon. The stuck.Probe recovery state machine
// (CW-20260512-0063, sprint α.5) shipped without a caller — its package doc
// called the trigger "currently dead". This is the wireup: a background
// watcher that scans running sessions and fires Probe for any that have
// gone idle past the configured threshold.
//
// It builds the adapters bridging stuck's narrow surfaces to the daemon's
// agent.Manager (session enumeration + the SendInput / ResumeSession /
// Checkpoint primitives), broker.Broker (the WAIT-phase envelope source),
// and the reactor.Dispatcher (the probe routes a received status_update
// through the same V0 dispatch table the reactor Loop uses).
//
// Returns (closer, error). closer is always non-nil — call at shutdown to
// drain the watcher goroutine. When the watcher is disabled by config, or
// agent.Manager is nil (a test composition root with no session runtime),
// the watcher is not started and closer is a no-op.
func StuckWatcher(
	ctx context.Context,
	sessions *agent.Manager,
	brk *broker.Broker,
	dispatcher *reactor.Dispatcher,
	cfg config.StuckConfig,
) (func(), error) {
	if !cfg.WatcherEnabled {
		log.Printf("[stuck] watcher disabled by config — stuck.Probe has no automatic trigger")
		return func() {}, nil
	}
	if sessions == nil || brk == nil || dispatcher == nil {
		// Mirrors bootstrap.Reactor's nil-tolerance: a composition root that
		// did not stand up the session runtime / broker simply runs without
		// the trigger rather than failing the daemon boot.
		log.Printf("[stuck] watcher not wired — sessions/broker/dispatcher unavailable")
		return func() {}, nil
	}

	deps := stuck.WatcherDeps{
		Lister:     &managerSessionLister{mgr: sessions},
		Sender:     sessions, // *agent.Manager satisfies stuck.InputSender directly
		Source:     brk,      // *broker.Broker satisfies stuck.EnvelopeSource directly
		Dispatch:   stuckDispatchAdapter{d: dispatcher},
		Resume:     &managerResumeAdapter{mgr: sessions},
		Checkpoint: &managerCheckpointAdapter{mgr: sessions},
	}
	w, err := stuck.New(deps, stuck.WatcherConfig{
		IdleThreshold: time.Duration(cfg.IdleThresholdSeconds) * time.Second,
		ScanInterval:  time.Duration(cfg.ScanIntervalSeconds) * time.Second,
		WaitTimeout:   time.Duration(cfg.WaitSeconds) * time.Second,
	})
	if err != nil {
		return func() {}, fmt.Errorf("stuck watcher: %w", err)
	}
	w.Start(ctx)
	log.Printf("[stuck] watcher started — idle-threshold=%ds scan-interval=%ds",
		cfg.IdleThresholdSeconds, cfg.ScanIntervalSeconds)
	return w.Close, nil
}

// --- adapters ---------------------------------------------------------

// managerSessionLister bridges stuck.SessionLister to agent.Manager.List.
// It enumerates running sessions and projects each onto stuck.LiveSession.
// Sessions with an empty TaskID are still returned — the Watcher's own
// isStuck check skips them — so the adapter stays a pure projection.
type managerSessionLister struct {
	mgr *agent.Manager
}

func (l *managerSessionLister) RunningSessions() ([]stuck.LiveSession, error) {
	// Limit 0 = no LIMIT clause (sqlstore.ListSessions): every running
	// session is a probe candidate.
	sessions, err := l.mgr.List(agent.StatusRunning, "", "", 0)
	if err != nil {
		return nil, fmt.Errorf("list running sessions: %w", err)
	}
	out := make([]stuck.LiveSession, 0, len(sessions))
	for _, s := range sessions {
		if s == nil {
			continue
		}
		out = append(out, stuck.LiveSession{
			SessionID:    s.ID,
			TaskID:       s.TaskID,
			LastActivity: s.LastActivity,
		})
	}
	return out, nil
}

// stuckDispatchAdapter bridges stuck.EnvelopeDispatcher to the
// reactor.Dispatcher. stuck keeps the return type erased to `any` to avoid
// importing reactor (an import cycle); this adapter — which lives in
// bootstrap, where importing both is fine — re-widens reactor's concrete
// DispatchResult to `any`.
type stuckDispatchAdapter struct {
	d *reactor.Dispatcher
}

func (a stuckDispatchAdapter) Dispatch(ctx context.Context, env gomsg.Envelope) any {
	return a.d.Dispatch(ctx, env)
}

// managerResumeAdapter bridges stuck.ResumeManager to
// agent.Manager.ResumeSession. The probe's silence branch passes the
// rendered diagnostic note; the adapter threads it through ResumeOptions
// and returns the new session id.
type managerResumeAdapter struct {
	mgr *agent.Manager
}

func (a *managerResumeAdapter) ResumeSession(ctx context.Context, sessionID, diagnosticNote string) (string, error) {
	sess, err := a.mgr.ResumeSession(ctx, sessionID, agent.ResumeOptions{DiagnosticNote: diagnosticNote})
	if err != nil {
		return "", err
	}
	return sess.ID, nil
}

// managerCheckpointAdapter bridges stuck.CheckpointMaker to
// agent.Manager.Checkpoint. The probe's silence branch checkpoints the
// session for audit linkage before resume; the adapter returns the new
// checkpoint id.
type managerCheckpointAdapter struct {
	mgr *agent.Manager
}

func (a *managerCheckpointAdapter) Checkpoint(sessionID, payload, note string) (string, error) {
	cp, err := a.mgr.Checkpoint(agent.CheckpointRequest{
		SessionID: sessionID,
		Payload:   payload,
		Note:      note,
	})
	if err != nil {
		return "", err
	}
	return cp.ID, nil
}

package stuck

import (
	"context"
	"log"
	"sync"
	"time"
)

// --- the trigger ------------------------------------------------------
//
// CW-20260518-0043 (messaging epic A). Probe (stuck.go) is the recovery
// state machine; until this file it had NO caller — the package doc called
// the trigger "currently dead" and tracked the wireup as a follow-up. The
// Watcher is that wireup: a periodic scan over running sessions that fires
// Probe for any session that looks stuck.
//
// Signal + its known limitation. The Watcher decides "stuck" from a
// session's LastActivity timestamp: a running session whose LastActivity is
// older than IdleThreshold is probed. This is deliberately the SIMPLEST
// real signal — and it is honest about its weakness:
//
//	The pid_poller (internal/runtime/agent/pid_poller.go) touches
//	last_activity on every ~5s tick for any session the lib still reports
//	alive. So LastActivity is a process-LIVENESS heartbeat, not a turn-
//	PROGRESS signal — it does NOT go stale for an LLM-level hang where the
//	process is alive but the agent is making no progress. What it DOES
//	catch: a session whose row still says `running` but whose pid_poller
//	has stopped touching it (process gone, lib deregistered, teardown did
//	not record a terminal state) — a genuine zombie-session class.
//
// The signal lives entirely behind the SessionLister interface, so a
// future progress-aware signal (a per-turn heartbeat, a run_events recency
// query) is a one-adapter swap with no change to the Watcher mechanism.
// See the implementer report for the follow-up ticket.

// LiveSession is the Watcher's view of one running session — the minimum
// the staleness decision and a subsequent Probe need. The production
// SessionLister projects agent.Session onto this; tests construct it
// directly.
type LiveSession struct {
	// SessionID is the Torque session id — the PROBE-phase SendInput target
	// and the RESUME-phase ResumeSession target.
	SessionID string

	// TaskID is the task the session is executing. Required: Probe
	// correlates incoming status_update envelopes by metadata.task_id, and
	// validateInput rejects an empty TaskID. The Watcher skips any session
	// with an empty TaskID rather than firing a doomed Probe.
	TaskID string

	// LastActivity is the session row's last_activity timestamp. The
	// Watcher probes the session when now-LastActivity exceeds the
	// configured IdleThreshold.
	LastActivity time.Time
}

// SessionLister is the narrow surface the Watcher needs to enumerate the
// sessions currently eligible for a stuck probe. Production wires an
// adapter over agent.Manager.List(StatusRunning, ...); tests pass a fake.
//
// Implementations should return only RUNNING sessions — a terminal session
// cannot be probed. Filtering by state belongs in the adapter so the
// Watcher stays signal-agnostic.
type SessionLister interface {
	RunningSessions() ([]LiveSession, error)
}

// WatcherDeps bundles the surfaces the Watcher threads into every Probe it
// fires. Lister is Watcher-specific; the other five are the session-
// independent Probe primitives (each takes a session id as a call argument,
// so one set serves every probed session). All six are required — New
// returns an error if any is nil, because a partially-wired Watcher would
// silently never recover anything.
type WatcherDeps struct {
	Lister     SessionLister
	Sender     InputSender
	Source     EnvelopeSource
	Dispatch   EnvelopeDispatcher
	Resume     ResumeManager
	Checkpoint CheckpointMaker
}

// WatcherConfig holds the Watcher's timing knobs. Zero-value fields fall
// back to the documented defaults via New.
type WatcherConfig struct {
	// IdleThreshold — a running session whose LastActivity is older than
	// this is considered stuck and gets a Probe. Zero falls back to
	// DefaultIdleThreshold.
	IdleThreshold time.Duration

	// ScanInterval — how often the Watcher scans the running-session set.
	// Zero falls back to DefaultScanInterval.
	ScanInterval time.Duration

	// WaitTimeout is passed through as ProbeInput.WaitTimeout — the PROBE→
	// WAIT silence window. Zero leaves ProbeInput.WaitTimeout zero, so Probe
	// itself falls back to DefaultWaitTimeout.
	WaitTimeout time.Duration
}

// DefaultIdleThreshold / DefaultScanInterval back the zero-value
// WatcherConfig fields. The idle threshold is deliberately conservative —
// the probe costs the agent a turn, so a false positive on a slow-but-
// progressing agent should be rare. 10 minutes is well past any normal
// turn-to-turn gap.
const (
	DefaultIdleThreshold = 10 * time.Minute
	DefaultScanInterval  = 60 * time.Second
)

// Watcher periodically scans running sessions and fires stuck.Probe for any
// that look stuck. One goroutine per Watcher; construct via New, drive with
// Start, drain with Close.
//
// Concurrency. Each scan launches at most one probe goroutine per stuck
// session. An in-flight set keys by session id so a probe that outlives the
// scan interval is never re-triggered. A natural cooldown layers on top:
// Probe's PROBE phase SendInputs the target session, and agent.Manager.
// SendInput touches last_activity — so a just-probed session reads as
// "active" on the next scan and is not immediately re-probed.
type Watcher struct {
	deps WatcherDeps
	cfg  WatcherConfig

	// now + probe are injection seams for tests; production uses time.Now
	// and the package-level Probe.
	now   func() time.Time
	probe func(context.Context, ProbeInput) Result

	mu       sync.Mutex
	inflight map[string]bool
	cancel   context.CancelFunc
	done     chan struct{}
}

// New constructs a Watcher. Returns an error when any WatcherDeps surface
// is nil — a Watcher missing a surface could not complete a single Probe,
// so failing fast at construction beats a silently inert background loop.
func New(deps WatcherDeps, cfg WatcherConfig) (*Watcher, error) {
	switch {
	case deps.Lister == nil:
		return nil, errNilDep("Lister")
	case deps.Sender == nil:
		return nil, errNilDep("Sender")
	case deps.Source == nil:
		return nil, errNilDep("Source")
	case deps.Dispatch == nil:
		return nil, errNilDep("Dispatch")
	case deps.Resume == nil:
		return nil, errNilDep("Resume")
	case deps.Checkpoint == nil:
		return nil, errNilDep("Checkpoint")
	}
	if cfg.IdleThreshold <= 0 {
		cfg.IdleThreshold = DefaultIdleThreshold
	}
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = DefaultScanInterval
	}
	return &Watcher{
		deps:     deps,
		cfg:      cfg,
		now:      time.Now,
		probe:    Probe,
		inflight: make(map[string]bool),
	}, nil
}

// ProbeFunc is the signature of the package-level Probe. WithProbeFunc
// swaps it so tests can drive the Watcher without standing up the real
// probe state machine.
type ProbeFunc func(context.Context, ProbeInput) Result

// WithProbeFunc overrides the probe entrypoint (test seam). Returns the
// receiver for chaining. Production leaves the default (Probe).
func (w *Watcher) WithProbeFunc(fn ProbeFunc) *Watcher {
	if fn != nil {
		w.probe = fn
	}
	return w
}

// WithNowFunc overrides the clock (test seam). Returns the receiver for
// chaining. Production leaves the default (time.Now).
func (w *Watcher) WithNowFunc(fn func() time.Time) *Watcher {
	if fn != nil {
		w.now = fn
	}
	return w
}

// Start launches the scan goroutine. Safe to call once per Watcher;
// subsequent calls without an intervening Close are no-ops. Start never
// errors — the first scan runs on the goroutine, and a SessionLister
// failure is logged, not fatal — so the signature returns nothing.
func (w *Watcher) Start(parent context.Context) {
	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	w.cancel = cancel
	w.done = done
	w.mu.Unlock()

	go w.run(ctx, done)
}

// Close stops the scan goroutine and waits for it to drain. In-flight
// probe goroutines are detached — they observe the parent ctx cancellation
// via their own ProbeInput context and wind down independently. Idempotent.
func (w *Watcher) Close() {
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	w.cancel = nil
	w.done = nil
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (w *Watcher) run(ctx context.Context, done chan struct{}) {
	defer close(done)

	ticker := time.NewTicker(w.cfg.ScanInterval)
	defer ticker.Stop()

	// Scan once immediately so a daemon that boots with an already-stuck
	// session does not wait a full interval before the first probe.
	w.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scan(ctx)
		}
	}
}

// scan runs one pass: list running sessions, probe the stuck ones. A
// SessionLister error aborts only this pass — the loop keeps ticking.
func (w *Watcher) scan(ctx context.Context) {
	sessions, err := w.deps.Lister.RunningSessions()
	if err != nil {
		log.Printf("[stuck] watcher scan: list running sessions: %v", err)
		return
	}
	cutoff := w.now().Add(-w.cfg.IdleThreshold)
	for _, sess := range sessions {
		if !w.isStuck(sess, cutoff) {
			continue
		}
		if !w.claim(sess.SessionID) {
			// A probe for this session is still running — do not stack a
			// second one on top of it.
			continue
		}
		go w.runProbe(ctx, sess)
	}
}

// isStuck reports whether sess should be probed: it must carry a TaskID
// (Probe requires one) and its LastActivity must predate the cutoff.
func (w *Watcher) isStuck(sess LiveSession, cutoff time.Time) bool {
	if sess.SessionID == "" || sess.TaskID == "" {
		return false
	}
	return sess.LastActivity.Before(cutoff)
}

// runProbe fires one Probe and logs its outcome. Always releases the
// in-flight claim so the session is eligible for a future probe once it
// goes stale again.
func (w *Watcher) runProbe(ctx context.Context, sess LiveSession) {
	defer w.release(sess.SessionID)

	log.Printf("[stuck] watcher probing session=%s task=%s (idle since %s)",
		sess.SessionID, sess.TaskID, sess.LastActivity.Format(time.RFC3339))

	res := w.probe(ctx, ProbeInput{
		SessionID:   sess.SessionID,
		TaskID:      sess.TaskID,
		WaitTimeout: w.cfg.WaitTimeout,
		Sender:      w.deps.Sender,
		Source:      w.deps.Source,
		Dispatch:    w.deps.Dispatch,
		Resume:      w.deps.Resume,
		Checkpoint:  w.deps.Checkpoint,
	})

	switch res.Outcome {
	case OutcomeFailed:
		log.Printf("[stuck] watcher probe FAILED session=%s task=%s: %v",
			sess.SessionID, sess.TaskID, res.Err)
	default:
		log.Printf("[stuck] watcher probe done session=%s task=%s outcome=%s resumed=%s",
			sess.SessionID, sess.TaskID, res.Outcome, res.ResumedSessionID)
	}
}

// claim records an in-flight probe for sessionID; it returns false when one
// is already running (the caller then skips this session for the scan).
func (w *Watcher) claim(sessionID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inflight[sessionID] {
		return false
	}
	w.inflight[sessionID] = true
	return true
}

func (w *Watcher) release(sessionID string) {
	w.mu.Lock()
	delete(w.inflight, sessionID)
	w.mu.Unlock()
}

// errNilDep is the construction-time error for a missing WatcherDeps field.
func errNilDep(name string) error {
	return &nilDepError{dep: name}
}

type nilDepError struct{ dep string }

func (e *nilDepError) Error() string {
	return "stuck.NewWatcher: required dependency " + e.dep + " is nil"
}

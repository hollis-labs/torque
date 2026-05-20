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
	// SessionID is the Torque session id — the PROBE-phase SendTurn target
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
	Sender     TurnSender
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

	// PostProbeCooldown — minimum interval between back-to-back probes of
	// the same session, measured from when the previous Probe call returned.
	// Zero falls back to DefaultPostProbeCooldown. Negative disables the
	// cooldown (tests that exercise the legacy "release immediately
	// re-arms" semantics pass -1).
	//
	// Why this exists (CW-20260519-0125). The in-flight claim is dropped
	// the instant Probe returns; without a cooldown, a session whose
	// last_activity was not bumped by the probe is eligible for immediate
	// re-probe on the very next scan tick. Two paths produce that gap:
	//   - SendTurn (PR #76, PROBE-phase delivery for streaming-stdio
	//     workers) calls m.inner.SendInput directly and does NOT invoke
	//     Manager.SendInput's TouchSession side effect.
	//   - The silence branch's ResumeSession creates a NEW session row but
	//     does not mutate the original row's last_activity, so the OLD
	//     row (still state=running until teardown) stays stale.
	// last_activity is updated only via the pid_poller's 5s heartbeat and
	// via content-bearing stream events — neither is guaranteed to fire
	// before the next scan. The cooldown closes that gap explicitly so we
	// don't rely on an incidental side effect.
	PostProbeCooldown time.Duration
}

// DefaultIdleThreshold / DefaultScanInterval back the zero-value
// WatcherConfig fields. The idle threshold is deliberately conservative —
// the probe costs the agent a turn, so a false positive on a slow-but-
// progressing agent should be rare. 10 minutes is well past any normal
// turn-to-turn gap.
//
// DefaultPostProbeCooldown gives a recovered session a meaningful window
// to produce activity (RESPOND-branch) or for the resumed session's pid
// poller / stream events to start bumping last_activity (RESUME-branch)
// before the Watcher reconsiders it. 5 minutes is half the idle threshold:
// long enough to absorb a slow resume boot + first-turn settle, short
// enough that a session that genuinely re-hung after recovery is still
// eligible for another probe within a reasonable window.
const (
	DefaultIdleThreshold     = 10 * time.Minute
	DefaultScanInterval      = 60 * time.Second
	DefaultPostProbeCooldown = 5 * time.Minute
)

// Watcher periodically scans running sessions and fires stuck.Probe for any
// that look stuck. One goroutine per Watcher; construct via New, drive with
// Start, drain with Close.
//
// Concurrency + re-probe guards. Each scan launches at most one probe
// goroutine per stuck session. Two layered guards prevent re-probing the
// same session too aggressively:
//
//  1. The in-flight set: keys by session id, held for the whole Probe call
//     (PROBE → WAIT → RESPOND|RESUME). Blocks stacking a second probe on
//     top of an in-flight one.
//  2. The post-probe cooldown: stamped on each session when the in-flight
//     claim is released. Blocks immediate re-probe on the very next scan
//     tick when last_activity has not yet been bumped by the probed
//     session — see WatcherConfig.PostProbeCooldown for why this matters
//     after CW-20260519-0122 / PR #76 (SendTurn does not call
//     TouchSession the way the old SendInput path did).
type Watcher struct {
	deps WatcherDeps
	cfg  WatcherConfig

	// now + probe are injection seams for tests; production uses time.Now
	// and the package-level Probe.
	now   func() time.Time
	probe func(context.Context, ProbeInput) Result

	mu           sync.Mutex
	inflight     map[string]bool
	lastProbedAt map[string]time.Time
	cancel       context.CancelFunc
	done         chan struct{}
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
	// PostProbeCooldown: zero → default; negative → caller explicitly
	// opted out (preserved for the legacy "release immediately re-arms"
	// test semantic). We don't normalize the negative value to zero here
	// so the check in scan can still distinguish "disabled" from
	// "default applied".
	if cfg.PostProbeCooldown == 0 {
		cfg.PostProbeCooldown = DefaultPostProbeCooldown
	}
	return &Watcher{
		deps:         deps,
		cfg:          cfg,
		now:          time.Now,
		probe:        Probe,
		inflight:     make(map[string]bool),
		lastProbedAt: make(map[string]time.Time),
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
	now := w.now()
	cutoff := now.Add(-w.cfg.IdleThreshold)
	for _, sess := range sessions {
		if !w.isStuck(sess, cutoff) {
			continue
		}
		if w.inCooldown(sess.SessionID, now) {
			// A probe for this session completed recently and we have
			// not yet given the session a meaningful window to make
			// progress (or stay silent). Skip to avoid re-probing on
			// the very next scan tick.
			continue
		}
		if !w.claim(sess.SessionID) {
			// A probe for this session is still running — do not stack a
			// second one on top of it.
			continue
		}
		go w.runProbe(ctx, sess)
	}
	w.pruneCooldown(now)
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

// release clears the in-flight claim AND stamps the per-session
// last-probed-at timestamp that powers the post-probe cooldown. The
// stamp is taken on release (probe-end) rather than claim (probe-start)
// so the cooldown window starts from when the Probe actually returned,
// not from when it was first launched — a long-running probe (e.g. the
// full WaitTimeout silence window) should not have its cooldown
// half-consumed by its own elapsed time.
func (w *Watcher) release(sessionID string) {
	w.mu.Lock()
	delete(w.inflight, sessionID)
	w.lastProbedAt[sessionID] = w.now()
	w.mu.Unlock()
}

// inCooldown reports whether sessionID was probed recently enough that
// the post-probe cooldown still bars another probe. A negative
// PostProbeCooldown disables the gate (used by tests that exercise the
// legacy "release immediately re-arms" semantic).
func (w *Watcher) inCooldown(sessionID string, now time.Time) bool {
	if w.cfg.PostProbeCooldown < 0 {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	t, ok := w.lastProbedAt[sessionID]
	if !ok {
		return false
	}
	return now.Sub(t) < w.cfg.PostProbeCooldown
}

// pruneCooldown drops lastProbedAt entries whose cooldown has already
// elapsed. Bounds the map size against long daemon uptimes — without
// this, a session probed once would sit in the map forever even after
// it's no longer in the running set.
func (w *Watcher) pruneCooldown(now time.Time) {
	if w.cfg.PostProbeCooldown < 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, t := range w.lastProbedAt {
		if now.Sub(t) >= w.cfg.PostProbeCooldown {
			delete(w.lastProbedAt, id)
		}
	}
}

// errNilDep is the construction-time error for a missing WatcherDeps field.
func errNilDep(name string) error {
	return &nilDepError{dep: name}
}

type nilDepError struct{ dep string }

func (e *nilDepError) Error() string {
	return "stuck.NewWatcher: required dependency " + e.dep + " is nil"
}

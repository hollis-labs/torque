package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
)

// stopGraceWindow bounds the per-Stop call so a misbehaving session can't
// block the hook goroutine. 5s matches the daemon's HTTP shutdown grace.
const stopGraceWindow = 5 * time.Second

// planTransitionStopDelay defers Stop briefly after the plan-terminal
// transition fires so the orchestrator can flush a final summary comment
// before SIGTERM. CW-20260509-0028 plan tradeoff: cleaner termination at
// the cost of a brief window where a redundant transition could fire.
const planTransitionStopDelay = 2 * time.Second

// orchestratorAuthorPrefix is the literal author marker the substrate
// matches when scanning comments for the layer-2 session-complete signal.
// Strict prefix to avoid false positives from unrelated authors. The full
// marker contract is documented in the orchestrator template runbook.
const orchestratorAuthorPrefix = "[system/orchestrator/"

// orchestratorCompleteMarker is the literal first-line content prefix the
// orchestrator emits via clockwork_comment_add as its self-stop signal.
// Strict prefix matching: any deviation (extra whitespace before, different
// author prefix, marker on line 2+) is treated as a non-marker comment.
const orchestratorCompleteMarker = "[system/orchestrator/session-complete]"

// SessionLifecycleHook subscribes to scheduler events and stops orchestrator
// sessions when their owning plan reaches a terminal status (layer 1) or
// when the orchestrator emits a session-complete marker comment (layer 2).
// Implements CW-20260509-0028.
//
// Observer methods (ObserveTaskTransition, ObserveComment) are non-blocking:
// they spawn a tracked goroutine and return immediately, so the upstream
// service-layer Add/Transition request path never waits on a session stop.
// Spawned work uses an internal context bound to the hook lifetime; Close
// cancels that context and drains all in-flight goroutines before returning.
type SessionLifecycleHook struct {
	bus      *scheduler.EventBus
	store    *sqlstore.Store
	sessions sessionStopper

	// internalCtx scopes all observer-spawned goroutines to the hook's
	// lifetime. Created in NewSessionLifecycleHook so observers fired before
	// Start (or after Close) still get a non-nil ctx; Close cancels it.
	internalCtx    context.Context
	internalCancel context.CancelFunc
	wg             sync.WaitGroup

	mu     sync.Mutex
	sub    <-chan scheduler.SchedulerEvent
	cancel context.CancelFunc
	done   chan struct{}
	closed bool
}

// sessionStopper is the slice of *Manager the hook needs. Narrow interface
// keeps unit tests free of the full Dependencies + agentsessions wiring.
type sessionStopper interface {
	Get(id string) (*Session, error)
	Stop(ctx context.Context, id string) error
}

// NewSessionLifecycleHook constructs a hook bound to the given collaborators.
// Returns nil if any required collaborator is nil — the daemon path always
// wires all three; nil-tolerance keeps test harnesses that build a partial
// AgentDeps from panicking.
func NewSessionLifecycleHook(bus *scheduler.EventBus, store *sqlstore.Store, sessions sessionStopper) *SessionLifecycleHook {
	if bus == nil || store == nil || sessions == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &SessionLifecycleHook{
		bus:            bus,
		store:          store,
		sessions:       sessions,
		internalCtx:    ctx,
		internalCancel: cancel,
	}
}

// Start subscribes to the bus and launches the dispatcher goroutine. Idempotent
// — calling twice without an intervening Close is a no-op.
func (h *SessionLifecycleHook) Start() {
	h.mu.Lock()
	if h.sub != nil {
		h.mu.Unlock()
		return
	}
	sub := h.bus.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h.sub = sub
	h.cancel = cancel
	h.done = done
	h.mu.Unlock()

	go h.run(ctx, sub, done)
}

// Close unsubscribes from the bus, cancels in-flight observer goroutines,
// and waits for everything to drain. Idempotent; safe to call from a
// process-shutdown path. After Close, observer methods are no-ops (the
// closed flag short-circuits new spawns).
func (h *SessionLifecycleHook) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	sub := h.sub
	cancel := h.cancel
	done := h.done
	h.sub = nil
	h.cancel = nil
	h.done = nil
	h.mu.Unlock()

	// Cancel bus-subscriber ctx + internal ctx so any in-flight goroutines
	// (delayed Stop timers, observer-spawned work) abort their wait.
	if cancel != nil {
		cancel()
	}
	h.internalCancel()

	if sub != nil {
		// Unsubscribe closes the channel — that unblocks the range loop in run().
		h.bus.Unsubscribe(sub)
	}
	if done != nil {
		<-done
	}
	// Drain after the bus subscriber exits so any final dispatch-spawned
	// goroutines have been added to the wg. wg.Wait blocks until all
	// observer + delayed-stop goroutines complete.
	h.wg.Wait()
}

func (h *SessionLifecycleHook) run(ctx context.Context, sub <-chan scheduler.SchedulerEvent, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			h.dispatch(ctx, ev)
		}
	}
}

func (h *SessionLifecycleHook) dispatch(ctx context.Context, ev scheduler.SchedulerEvent) {
	if ev.Type != "task.transitioned" {
		return
	}
	if ev.TaskID == "" {
		return
	}
	to := stringField(ev.Data, "to")
	if !planTerminalStatus(to) {
		return
	}
	h.HandlePlanTransition(ctx, ev.TaskID)
}

// HandlePlanTransition is the layer-1 entry point. Public so tests can
// invoke it without driving the full bus. Looks up the task, validates
// it's a plan with an orchestrator session, and stops that session after
// a brief grace delay.
func (h *SessionLifecycleHook) HandlePlanTransition(ctx context.Context, taskID string) {
	task, err := h.store.GetTask(taskID)
	if err != nil || task == nil {
		return
	}
	if task.Kind != "plan" {
		return
	}
	sessID, ok := orchestratorSessionID(task)
	if !ok {
		return
	}
	h.stopSessionAfterDelay(ctx, sessID, planTransitionStopDelay)
}

// ObserveTaskTransition is the primary layer-1 entry point in production.
// Implements service.TaskTransitionObserver. Plan FSM moves are agent-driven
// via Task.Transition / Task.ForceTransition, which don't ride the
// scheduler.EventBus — the service-layer observer fires from inside that
// call path so the hook reliably sees plan-terminal transitions regardless
// of whether the trigger was MCP, HTTP, or scheduler-internal.
//
// Non-blocking: spawns a tracked goroutine using the hook's internal ctx
// (NOT the caller's ctx, which is typically context.Background() from the
// service layer and would not honor daemon shutdown). The caller-supplied
// ctx is intentionally ignored — the service layer's Add/Transition request
// path must not be coupled to this hook's stop latency.
func (h *SessionLifecycleHook) ObserveTaskTransition(_ context.Context, taskID, _ /* fromStatus */, toStatus string) {
	if !planTerminalStatus(toStatus) {
		return
	}
	if !h.spawnObserver(func(ctx context.Context) {
		h.HandlePlanTransition(ctx, taskID)
	}) {
		return
	}
}

// ObserveComment is the layer-2 entry point invoked by the service-layer
// after a comment is persisted. Filters on the orchestrator author prefix
// + session-complete first-line marker, then stops the linked session.
// Implements service.CommentObserver.
//
// Non-blocking: filter checks on the freely-available c fields run inline so
// the common no-match case has zero goroutine cost. On match, the DB lookup
// + session stop are dispatched to a tracked goroutine bound to the hook's
// internal ctx. The caller-supplied ctx is ignored for the same reason as
// ObserveTaskTransition.
func (h *SessionLifecycleHook) ObserveComment(_ context.Context, c *sqlstore.CommentRecord) {
	if c == nil {
		return
	}
	if c.EntityType != sqlstore.EntityTypeTask || c.EntityID == "" {
		return
	}
	if !strings.HasPrefix(c.Author, orchestratorAuthorPrefix) {
		return
	}
	if !hasSessionCompleteMarker(c.Content) {
		return
	}
	entityID := c.EntityID
	if !h.spawnObserver(func(ctx context.Context) {
		task, err := h.store.GetTask(entityID)
		if err != nil || task == nil {
			return
		}
		sessID, ok := orchestratorSessionID(task)
		if !ok {
			return
		}
		// CW-20260510-0064: belt-and-suspenders against a hallucinated
		// session-complete marker. If the orchestrator emits the marker
		// while one of its children is still progressing (task.status ∈
		// {doing, review}), suppress the stop and log a WARN. The template
		// fix is the primary guard; this is the substrate-side safety net
		// against future LLM behavior drift.
		if h.hasInProgressChild(entityID) {
			log.Printf("[lifecycle] WARN: suppressing layer-2 stop for plan=%s sess=%s — child task still in progress (doing/review). marker likely false-positive.",
				entityID, sessID)
			return
		}
		// Layer 2 fires AFTER the orchestrator wrote its final comment, so no
		// extra grace delay is needed — the orchestrator has already flushed.
		h.stopSession(ctx, sessID)
	}) {
		return
	}
}

// hasInProgressChild returns true when the plan task has at least one
// direct child whose status indicates active work (doing or review). Used
// by the layer-2 marker observer to guard against premature self-stop in
// the face of a hallucinated session-complete signal — see
// CW-20260510-0064 for the incident that motivated the check.
//
// The status set is deliberately narrow: `doing` (executor active) and
// `review` (reviewer end-agent will close it). `todo` is intentionally
// EXCLUDED — a child still at todo means the orchestrator hasn't
// dispatched it yet, and the orchestrator emitting session-complete with
// pending todos is a legitimate "I'm done with this slice" signal (e.g.
// orchestrator self-block / cancelled-by-user paths). The guard fires
// only when work is actively in flight.
func (h *SessionLifecycleHook) hasInProgressChild(planID string) bool {
	if planID == "" {
		return false
	}
	children, err := h.store.ListTasks(sqlstore.TaskFilter{ParentID: planID})
	if err != nil {
		// Conservative on error: don't suppress. The original (pre-CW-0064)
		// behavior is the fallback — better a false-positive stop than a
		// silently-wedged orchestrator.
		return false
	}
	for i := range children {
		switch children[i].Status {
		case "doing", "review":
			return true
		}
	}
	return false
}

// spawnObserver runs fn in a tracked goroutine using the hook's internal ctx.
// Returns false (and does not spawn) when the hook has been Closed; the
// caller treats that as a no-op. The wg increment happens before goroutine
// launch so Close's wg.Wait correctly accounts for in-flight work.
func (h *SessionLifecycleHook) spawnObserver(fn func(context.Context)) bool {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return false
	}
	h.wg.Add(1)
	h.mu.Unlock()

	go func() {
		defer h.wg.Done()
		fn(h.internalCtx)
	}()
	return true
}

func (h *SessionLifecycleHook) stopSessionAfterDelay(ctx context.Context, sessID string, delay time.Duration) {
	if delay <= 0 {
		h.stopSession(ctx, sessID)
		return
	}
	// Track via wg so Close drains the delayed goroutine. Use the hook's
	// internal ctx instead of the caller's ctx so the timer survives the
	// caller-supplied ctx (commonly request-scoped) finishing first; Close
	// cancels the internal ctx to cut the wait short.
	if !h.spawnObserver(func(internalCtx context.Context) {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-internalCtx.Done():
			return
		case <-t.C:
		}
		h.stopSession(internalCtx, sessID)
	}) {
		return
	}
}

func (h *SessionLifecycleHook) stopSession(ctx context.Context, sessID string) {
	sess, err := h.sessions.Get(sessID)
	if err != nil {
		// ErrSessionNotFound here just means the session row vanished
		// (DB rotation in tests, never in production). Silent no-op.
		return
	}
	if sess.Status != StatusRunning && sess.Status != StatusLaunching {
		// Already terminal or never reached running — nothing to stop.
		return
	}
	stopCtx, cancel := context.WithTimeout(ctx, stopGraceWindow)
	defer cancel()
	if err := h.sessions.Stop(stopCtx, sessID); err != nil {
		if errors.Is(err, ErrSessionNotRunning) {
			// Idempotent — pid poller (CW-20260509-0008) may have already
			// driven the clean termination.
			return
		}
		log.Printf("[lifecycle] session stop %s: %v", sessID, err)
	}
}

// planTerminalStatus reports whether the named plan status is a sink for
// the layer-1 hook. Mirrors the four-state set from the plan doc; `crashed`
// is excluded because the orphan sweep generates that, not a transition.
func planTerminalStatus(s string) bool {
	switch s {
	case "review", "done", "failed", "abandoned":
		return true
	}
	return false
}

// hasSessionCompleteMarker reports whether the comment's first line begins
// with the literal session-complete marker. First-line-only by design; a
// later mention in a long comment is not the orchestrator's exit signal.
func hasSessionCompleteMarker(content string) bool {
	if content == "" {
		return false
	}
	first := content
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		first = content[:i]
	}
	return strings.HasPrefix(first, orchestratorCompleteMarker)
}

// orchestratorSessionID extracts metadata.plan.orchestrator_session_id from
// the task's metadata JSON. Returns ("", false) when absent or malformed.
// Mirrors the unexported planstart helper without introducing an import
// cycle.
func orchestratorSessionID(task *sqlstore.TaskRecord) (string, bool) {
	if task == nil || !task.Metadata.Valid || task.Metadata.String == "" {
		return "", false
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(task.Metadata.String), &root); err != nil {
		return "", false
	}
	planNS, _ := root["plan"].(map[string]any)
	v, _ := planNS["orchestrator_session_id"].(string)
	return v, v != ""
}

func stringField(data interface{}, key string) string {
	m, ok := data.(map[string]interface{})
	if !ok {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

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
type SessionLifecycleHook struct {
	bus      *scheduler.EventBus
	store    *sqlstore.Store
	sessions sessionStopper

	mu     sync.Mutex
	sub    <-chan scheduler.SchedulerEvent
	cancel context.CancelFunc
	done   chan struct{}
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
	return &SessionLifecycleHook{
		bus:      bus,
		store:    store,
		sessions: sessions,
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

// Close unsubscribes and waits for the dispatcher goroutine to exit.
// Idempotent. Safe to call from a process-shutdown path.
func (h *SessionLifecycleHook) Close() {
	h.mu.Lock()
	sub := h.sub
	cancel := h.cancel
	done := h.done
	h.sub = nil
	h.cancel = nil
	h.done = nil
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if sub != nil {
		// Unsubscribe closes the channel — that unblocks the range loop in run().
		h.bus.Unsubscribe(sub)
	}
	if done != nil {
		<-done
	}
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

// ObserveComment is the layer-2 entry point invoked by the service-layer
// after a comment is persisted. Filters on the orchestrator author prefix
// + session-complete first-line marker, then stops the linked session.
// Implements service.CommentObserver.
func (h *SessionLifecycleHook) ObserveComment(ctx context.Context, c *sqlstore.CommentRecord) {
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
	task, err := h.store.GetTask(c.EntityID)
	if err != nil || task == nil {
		return
	}
	sessID, ok := orchestratorSessionID(task)
	if !ok {
		return
	}
	// Layer 2 fires AFTER the orchestrator wrote its final comment, so no
	// extra grace delay is needed — the orchestrator has already flushed.
	h.stopSession(ctx, sessID)
}

func (h *SessionLifecycleHook) stopSessionAfterDelay(ctx context.Context, sessID string, delay time.Duration) {
	if delay <= 0 {
		h.stopSession(ctx, sessID)
		return
	}
	go func() {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		h.stopSession(ctx, sessID)
	}()
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

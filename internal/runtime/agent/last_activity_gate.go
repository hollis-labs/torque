package agent

import (
	"context"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// authErrorMarkers tags content-bearing payloads that should be treated as an
// error signal even when the lib doesn't surface them as EventError. claude's
// CLI emits 401 responses as text deltas in some failure modes, so the wrapper
// process stays alive and the PID poller keeps heartbeating with no real
// activity behind it. CW-20260519-0130.
//
// Markers are deliberately specific phrases rather than the bare token "401"
// — model output can mention "401" innocuously (e.g. "401k", "RFC 401",
// version numbers) and a bare-substring match false-positive-freezes healthy
// sessions (Copilot review on PR #78). The four phrases below cover the
// surfaces actually observed in production (Pass-2 / Pass-6 / Pass-11 /
// Pass-20 incidents this arc): "Invalid API key · Fix external API key"
// (OAuth-expiry class), "Failed to authenticate. API Error: 401 Invalid
// authentication credentials" (resume-401 class), and the bare-HTTP form.
var authErrorMarkers = []string{
	"Invalid API key",
	"Invalid authentication",
	"API Error: 401",
	"401 Unauthorized",
}

// observeStreamEvent flips the per-session last_activity freeze gate based on
// stream-event type and content. Called from the stream-fanout drain goroutine
// for every llmtypes.StreamEvent the lib emits.
//
//   - EventError → freeze on. The wrapper process can still be heartbeating,
//     but no real activity is happening behind it; monitors must see
//     last_activity stop reflecting that.
//   - EventDelta containing an auth-error marker → freeze on (claude's CLI
//     surfaces 401 / Invalid API key responses as text deltas in some
//     failure modes).
//   - Content-bearing event (EventToolUse / EventDelta with non-error
//     content / EventThinking) → freeze off. Real model output means the
//     session is making progress again.
//   - Other events (EventUsage, EventSessionID, EventDone) leave the gate
//     as-is; they're metadata and don't change the liveness signal.
func (m *Manager) observeStreamEvent(sessID string, ev llmtypes.StreamEvent) {
	if m == nil || sessID == "" {
		return
	}
	if isErrorStreamEvent(ev) {
		m.markActivityFrozen(sessID)
		return
	}
	if isContentBearingStreamEvent(ev) {
		m.clearActivityFrozen(sessID)
	}
}

func isErrorStreamEvent(ev llmtypes.StreamEvent) bool {
	if ev.Type == llmtypes.EventError {
		return true
	}
	if ev.Type == llmtypes.EventDelta && containsAuthErrorMarker(ev.Content) {
		return true
	}
	return false
}

func isContentBearingStreamEvent(ev llmtypes.StreamEvent) bool {
	switch ev.Type {
	case llmtypes.EventToolUse, llmtypes.EventThinking:
		return true
	case llmtypes.EventDelta:
		return ev.Content != "" && !containsAuthErrorMarker(ev.Content)
	}
	return false
}

func containsAuthErrorMarker(s string) bool {
	if s == "" {
		return false
	}
	for _, marker := range authErrorMarkers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func (m *Manager) markActivityFrozen(sessID string) {
	m.mu.Lock()
	if m.activityFrozen == nil {
		m.activityFrozen = make(map[string]struct{})
	}
	m.activityFrozen[sessID] = struct{}{}
	m.mu.Unlock()
}

func (m *Manager) clearActivityFrozen(sessID string) {
	m.mu.Lock()
	delete(m.activityFrozen, sessID)
	m.mu.Unlock()
}

func (m *Manager) activityFrozenState(sessID string) bool {
	m.mu.RLock()
	_, ok := m.activityFrozen[sessID]
	m.mu.RUnlock()
	return ok
}

// touchSessionUnlessFrozen issues a heartbeat TouchSession only when the
// per-session freeze gate is off. Returns true when the touch was suppressed
// — the caller is the PID poller and uses this to keep last_activity honest
// when the wrapper process is heartbeating without real activity behind it.
func (m *Manager) touchSessionUnlessFrozen(ctx context.Context, sessID string) (suppressed bool) {
	if m.activityFrozenState(sessID) {
		return true
	}
	if m.deps == nil || m.deps.Store == nil {
		return false
	}
	_ = m.deps.TouchSession(ctx, sessID)
	return false
}

package agent

import (
	"context"
	"database/sql"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
)

// TestObserveStreamEvent_ErrorFrameFreezesActivity covers the freeze trigger:
// a typed EventError flips the per-session gate so subsequent heartbeats are
// suppressed. CW-20260519-0130.
func TestObserveStreamEvent_ErrorFrameFreezesActivity(t *testing.T) {
	mgr := &Manager{activityFrozen: make(map[string]struct{})}

	mgr.observeStreamEvent("SES-A", llmtypes.StreamEvent{Type: llmtypes.EventError, Error: "boom"})

	assert.True(t, mgr.activityFrozenState("SES-A"),
		"EventError must mark the session as activity-frozen so the PID poller stops bumping last_activity")
}

// TestObserveStreamEvent_AuthErrorDeltaFreezesActivity covers the
// defense-in-depth branch: some claude-CLI failure modes surface a 401 /
// Invalid API key response as a text delta rather than a typed error frame.
// Without this branch, the wrapper keeps heartbeating an auth-dead session
// (the original Pass-18 / Pass-1 trap).
func TestObserveStreamEvent_AuthErrorDeltaFreezesActivity(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"raw 401", "request returned status 401 Unauthorized"},
		{"invalid api key", "Error: Invalid API key. Please check your credentials."},
		{"invalid authentication", "Invalid authentication credentials"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &Manager{activityFrozen: make(map[string]struct{})}

			mgr.observeStreamEvent("SES-X", llmtypes.StreamEvent{
				Type:    llmtypes.EventDelta,
				Content: tc.content,
			})

			assert.True(t, mgr.activityFrozenState("SES-X"),
				"delta containing %q must freeze the session", tc.content)
		})
	}
}

// TestObserveStreamEvent_ContentBearingEventLiftsFreeze covers the recovery
// path: after a freeze, a real content-bearing frame (tool_use / non-error
// delta / thinking) lifts the gate so last_activity resumes reflecting real
// liveness.
func TestObserveStreamEvent_ContentBearingEventLiftsFreeze(t *testing.T) {
	cases := []struct {
		name string
		ev   llmtypes.StreamEvent
	}{
		{"tool_use", llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{Name: "Read"}}},
		{"non-error delta", llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "ok, here is the answer"}},
		{"thinking", llmtypes.StreamEvent{Type: llmtypes.EventThinking, ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: "reasoning..."}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &Manager{activityFrozen: make(map[string]struct{})}
			mgr.markActivityFrozen("SES-Y")
			require.True(t, mgr.activityFrozenState("SES-Y"))

			mgr.observeStreamEvent("SES-Y", tc.ev)

			assert.False(t, mgr.activityFrozenState("SES-Y"),
				"%s event must clear the freeze so the heartbeat resumes bumping last_activity", tc.name)
		})
	}
}

// TestObserveStreamEvent_MetadataEventsLeaveGateUnchanged covers the
// neutrality rule: usage / session_id / done are metadata and must not
// toggle the gate either way. A spurious usage frame between an error and
// the recovery must not lift the freeze prematurely.
func TestObserveStreamEvent_MetadataEventsLeaveGateUnchanged(t *testing.T) {
	cases := []llmtypes.StreamEvent{
		{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 10, OutputTokens: 20}},
		{Type: llmtypes.EventSessionID, SessionID: "claude-session-123"},
		{Type: llmtypes.EventDone},
	}
	for _, ev := range cases {
		t.Run(string(ev.Type), func(t *testing.T) {
			mgr := &Manager{activityFrozen: make(map[string]struct{})}
			mgr.markActivityFrozen("SES-Z")
			require.True(t, mgr.activityFrozenState("SES-Z"))

			mgr.observeStreamEvent("SES-Z", ev)

			assert.True(t, mgr.activityFrozenState("SES-Z"),
				"%s is metadata and must not lift the freeze", ev.Type)
		})
	}

	t.Run("empty delta does not lift", func(t *testing.T) {
		mgr := &Manager{activityFrozen: make(map[string]struct{})}
		mgr.markActivityFrozen("SES-W")

		mgr.observeStreamEvent("SES-W", llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: ""})

		assert.True(t, mgr.activityFrozenState("SES-W"),
			"empty delta carries no content and must not be treated as recovery")
	})
}

// TestTouchSessionUnlessFrozen_HonorsGate is the integration shape the
// CW-20260519-0130 acceptance criterion calls for: write an error frame,
// advance time, verify last_activity does NOT bump on subsequent heartbeats;
// then write a successful frame and verify last_activity resumes.
//
// Uses an in-memory sqlite store so the assertion reads the actual
// last_activity column the dashboard + monitors observe.
func TestTouchSessionUnlessFrozen_HonorsGate(t *testing.T) {
	store := newGateTestStore(t)
	deps := &Dependencies{Store: store}
	mgr := NewManager(deps)

	const sessID = "SES-FREEZE-1"
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID:           sessID,
		AgentProfile: "implementer",
		Provider:     "claude",
		RuntimeID:    "torque-cli/claude",
		RuntimeKind:  "adapter",
		Workdir:      "/tmp/x",
	}))

	// Baseline heartbeat — no freeze, last_activity advances.
	require.False(t, mgr.touchSessionUnlessFrozen(context.Background(), sessID),
		"baseline touch must not be suppressed (gate is clear)")
	frozenStart := readLastActivity(t, store, sessID)

	// Observe an error frame — gate flips on.
	mgr.observeStreamEvent(sessID, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: "401 Unauthorized"})
	require.True(t, mgr.activityFrozenState(sessID))

	// Subsequent heartbeats must be suppressed. We sleep between calls to
	// guarantee the wall-clock advances past sqlite's timestamp precision;
	// if the write actually happened the read-back would show a bump.
	for i := 0; i < 3; i++ {
		time.Sleep(2 * time.Millisecond)
		assert.True(t, mgr.touchSessionUnlessFrozen(context.Background(), sessID),
			"heartbeat #%d under freeze must report suppressed=true", i)
	}
	frozenEnd := readLastActivity(t, store, sessID)
	assert.True(t, frozenEnd.Equal(frozenStart),
		"last_activity must NOT advance while the gate is frozen (start=%s end=%s)", frozenStart, frozenEnd)

	// A content-bearing event clears the gate; the next heartbeat resumes
	// bumping last_activity. The Pass-18 / Pass-1 trap was that this never
	// happened — monitors saw a stale-but-recent timestamp on auth-dead
	// sessions because the wrapper kept heartbeating.
	mgr.observeStreamEvent(sessID, llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{Name: "Read"}})
	require.False(t, mgr.activityFrozenState(sessID))

	time.Sleep(2 * time.Millisecond)
	assert.False(t, mgr.touchSessionUnlessFrozen(context.Background(), sessID),
		"post-recovery heartbeat must NOT be suppressed")
	resumed := readLastActivity(t, store, sessID)
	assert.True(t, resumed.After(frozenEnd),
		"last_activity must advance after the gate is lifted (frozen=%s resumed=%s)", frozenEnd, resumed)
}

// TestTeardownSession_ClearsActivityGate locks the cleanup contract: when
// a session terminates, its gate entry must be dropped so the map does not
// accumulate over the daemon's lifetime.
func TestTeardownSession_ClearsActivityGate(t *testing.T) {
	mgr := NewManager(&Dependencies{})

	const sessID = "SES-TEARDOWN-1"
	mgr.markActivityFrozen(sessID)
	require.True(t, mgr.activityFrozenState(sessID))

	mgr.teardownSession(sessID)

	assert.False(t, mgr.activityFrozenState(sessID),
		"teardownSession must drop the activity-gate entry alongside the other per-session maps")
}

func newGateTestStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func readLastActivity(t *testing.T, store *sqlstore.Store, sessID string) time.Time {
	t.Helper()
	rec, err := store.GetSession(sessID)
	require.NoError(t, err)
	return rec.LastActivity
}

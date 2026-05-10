package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSession_MarshalJSON_LiveSessionOmitsExitAndEnded covers the
// CW-20260510-0064 fix: a still-running session must NOT emit
// "ExitCode": null or "EndedAt": null on the wire. The orchestrator LLM
// has been observed reading "ExitCode": null + "PID": 0 as a crash signal
// and hallucinating exit_code=-1; absence is harder to misread than null.
func TestSession_MarshalJSON_LiveSessionOmitsExitAndEnded(t *testing.T) {
	sess := Session{
		ID:       "SES-LIVE",
		Status:   StatusRunning,
		PID:      0, // adapter-mode (claude) — PID stays 0 for the session lifetime
		ExitCode: nil,
		EndedAt:  nil,
	}

	b, err := json.Marshal(sess)
	require.NoError(t, err)

	out := string(b)

	// Critical: no exit_code or ended_at on the wire when nil.
	assert.NotContains(t, out, `"ExitCode"`,
		"ExitCode must be omitted (omitempty) when nil so an LLM can't read null as -1/0")
	assert.NotContains(t, out, `"EndedAt"`,
		"EndedAt must be omitted (omitempty) when nil so an LLM can't read null as a stop time")

	// Status should still be present and == "running".
	assert.Contains(t, out, `"Status":"running"`)

	// Terminal must be present and false — gives the orchestrator an
	// unambiguous signal without having to reason about ExitCode+Status.
	assert.Contains(t, out, `"Terminal":false`,
		"derived Terminal field must be present and false for a live session")
}

// TestSession_MarshalJSON_TerminalSessionEmitsExitAndEnded covers the
// other half: a real terminal session DOES surface ExitCode + EndedAt so
// operators (and the orchestrator's task-FSM-anchored escalation path)
// can read the recorded outcome.
func TestSession_MarshalJSON_TerminalSessionEmitsExitAndEnded(t *testing.T) {
	exit := 0
	ended := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	sess := Session{
		ID:       "SES-DONE",
		Status:   StatusDone,
		PID:      12345,
		ExitCode: &exit,
		EndedAt:  &ended,
	}

	b, err := json.Marshal(sess)
	require.NoError(t, err)
	out := string(b)

	assert.Contains(t, out, `"ExitCode":0`,
		"ExitCode must be present on a terminal session")
	assert.Contains(t, out, `"EndedAt"`,
		"EndedAt must be present on a terminal session")
	assert.Contains(t, out, `"Status":"done"`)
	assert.Contains(t, out, `"Terminal":true`,
		"derived Terminal must be true when Status is terminal AND ExitCode is recorded")
}

// TestSession_MarshalJSON_TerminalRequiresExitCode locks the AND-rule for
// the derived Terminal flag: Status alone is not enough. A session whose
// Status flipped to `done` but whose ExitCode hasn't been persisted yet
// (a brief shutdown window in manager.Stop) reads as Terminal=false so
// the orchestrator doesn't pre-declare success on incomplete data.
func TestSession_MarshalJSON_TerminalRequiresExitCode(t *testing.T) {
	sess := Session{
		ID:       "SES-PARTIAL",
		Status:   StatusDone,
		ExitCode: nil, // partial shutdown — exit not persisted yet
	}

	b, err := json.Marshal(sess)
	require.NoError(t, err)
	out := string(b)

	assert.Contains(t, out, `"Terminal":false`,
		"Terminal must be false when ExitCode is nil even if Status looks terminal")
	assert.NotContains(t, out, `"ExitCode"`,
		"ExitCode must remain omitted while nil")
}

// TestSession_MarshalJSON_RoundtripPreservesPayload verifies the new
// MarshalJSON doesn't lose any of the fields the operator-facing
// MCP surface depends on. We marshal, then re-parse into a generic map
// (since the alias trick means UnmarshalJSON wouldn't roundtrip Terminal
// back as a struct field), and assert all the persistent fields are
// present and shaped correctly.
func TestSession_MarshalJSON_RoundtripPreservesPayload(t *testing.T) {
	exit := 0
	ended := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	created := time.Date(2026, 5, 10, 11, 0, 0, 0, time.UTC)
	sess := Session{
		ID:           "SES-ROUNDTRIP",
		Mode:         ModeLongLived,
		AgentProfile: "orchestrator",
		Provider:     "claude",
		RuntimeID:    "rt-1",
		RuntimeKind:  "claude",
		Workdir:      "/tmp/work",
		BootDir:      "/tmp/boot",
		WorkspaceDir: "/tmp/ws",
		ProjectID:    "proj-1",
		TaskID:       "CW-PLAN-1",
		Status:       StatusDone,
		PID:          1234,
		ExitCode:     &exit,
		Meta:         map[string]string{"role": "orchestrator"},
		CreatedAt:    created,
		UpdatedAt:    created,
		LastActivity: created,
		EndedAt:      &ended,
	}

	b, err := json.Marshal(sess)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	// Sanity: every persistent field present.
	for _, key := range []string{
		"ID", "Mode", "AgentProfile", "Provider", "RuntimeID", "RuntimeKind",
		"Workdir", "BootDir", "WorkspaceDir", "ProjectID", "TaskID",
		"Status", "PID", "ExitCode", "Meta", "CreatedAt", "UpdatedAt",
		"LastActivity", "EndedAt", "Terminal",
	} {
		_, ok := got[key]
		assert.Truef(t, ok, "field %q must round-trip", key)
	}

	assert.Equal(t, "SES-ROUNDTRIP", got["ID"])
	assert.Equal(t, "done", got["Status"])
	assert.Equal(t, true, got["Terminal"])
}

// TestSession_MarshalJSON_AdapterModeLiveSessionIsTheCW0064Shape pins down
// exactly the wire payload that triggered the CW-20260510-0064
// false-negative. Live adapter-mode session: Status=running, PID=0,
// ExitCode/EndedAt nil. The post-fix shape must NOT contain any
// substring an LLM could reasonably misread as a crash signal.
func TestSession_MarshalJSON_AdapterModeLiveSessionIsTheCW0064Shape(t *testing.T) {
	sess := Session{
		ID:           "SES-01KR852C01NQ61D7CAWCJ3WJ99",
		Status:       StatusRunning,
		PID:          0,
		LastActivity: time.Date(2026, 5, 10, 5, 19, 25, 0, time.UTC),
	}

	b, err := json.Marshal(sess)
	require.NoError(t, err)
	out := string(b)

	// The orchestrator must see Terminal=false explicitly + no exit_code or
	// ended_at fields at all. PID=0 stays (adapter-mode reality) but the
	// template-level deny-list keeps the orchestrator from polling sessions
	// in the first place; this test is the substrate's defense in depth.
	assert.Contains(t, out, `"Terminal":false`)
	assert.False(t, strings.Contains(out, `"ExitCode":-1`),
		"the substrate must NEVER emit ExitCode=-1 for a live session — that was the hallucinated value that aborted plan CW-20260510-0019")
	assert.False(t, strings.Contains(out, `"ExitCode":null`),
		"omitempty must drop ExitCode entirely; null is what was getting misread")
	assert.False(t, strings.Contains(out, `"EndedAt":null`),
		"omitempty must drop EndedAt entirely")
}

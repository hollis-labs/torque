package aar_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/aar"
)

func TestRender_AllFieldsPresent_RoundTripsThroughParse(t *testing.T) {
	id := aar.Identity{
		TaskID:       "CW-20260520-0007",
		RunID:        42,
		SessionID:    "sess-abc",
		AgentProfile: "implementer-long",
		Provider:     "claude-code",
		Mode:         "long_lived",
		ProjectID:    "PRJ-20260416-0001",
		SprintID:     "SP-20260519-0002",
		Title:        "Build the AAR system",
		StartedAt:    time.Date(2026, 5, 20, 20, 0, 0, 0, time.UTC),
		EndedAt:      time.Date(2026, 5, 20, 22, 15, 0, 0, time.UTC),
	}
	ref := aar.Reflection{
		Summary:            "Stood up AAR system end-to-end.",
		Outcome:            aar.OutcomeSuccess,
		Clunky:             "Couldn't find the docs/torque-agent-guide referenced in the boot.",
		Automatable:        "Run identity + boot-template hookup.",
		ManualShouldBeAuto: "Auto-fill run_id from the active run record.",
		SharpEdges:         "Loopback doesn't know TORQUE_WORK_ROOT.",
		Suggestions:        "Thread work_root into NewLoopback for file-path artifacts.",
		Errors:             []string{"flaky test in pkg foo", "go build failed first try"},
	}

	out := aar.Render(id, ref)
	require.NotEmpty(t, out)
	assert.Contains(t, out, "schema: aar/v1")
	assert.Contains(t, out, "task_id: CW-20260520-0007")
	assert.Contains(t, out, "run_id: 42")
	assert.Contains(t, out, "agent_profile: implementer-long")
	assert.Contains(t, out, "outcome: success")
	assert.Contains(t, out, "## Summary")
	assert.Contains(t, out, "Stood up AAR system end-to-end.")
	assert.Contains(t, out, "- flaky test in pkg foo")

	gotID, gotRef, schema, err := aar.Parse(out)
	require.NoError(t, err)
	assert.Equal(t, "aar/v1", schema)
	assert.Equal(t, id.TaskID, gotID.TaskID)
	assert.Equal(t, id.RunID, gotID.RunID)
	assert.Equal(t, id.SessionID, gotID.SessionID)
	assert.Equal(t, id.AgentProfile, gotID.AgentProfile)
	assert.Equal(t, id.Provider, gotID.Provider)
	assert.Equal(t, id.Mode, gotID.Mode)
	assert.Equal(t, id.ProjectID, gotID.ProjectID)
	assert.Equal(t, id.SprintID, gotID.SprintID)
	assert.True(t, id.StartedAt.Equal(gotID.StartedAt), "started_at round-trip; got %v want %v", gotID.StartedAt, id.StartedAt)
	assert.True(t, id.EndedAt.Equal(gotID.EndedAt), "ended_at round-trip")
	assert.Equal(t, ref.Summary, gotRef.Summary)
	assert.Equal(t, ref.Clunky, gotRef.Clunky)
	assert.Equal(t, ref.Automatable, gotRef.Automatable)
	assert.Equal(t, ref.ManualShouldBeAuto, gotRef.ManualShouldBeAuto)
	assert.Equal(t, ref.SharpEdges, gotRef.SharpEdges)
	assert.Equal(t, ref.Suggestions, gotRef.Suggestions)
	assert.Equal(t, aar.OutcomeSuccess, gotRef.Outcome)
	assert.Equal(t, ref.Errors, gotRef.Errors)
}

func TestRender_EmptyReflection_StillStructured(t *testing.T) {
	out := aar.Render(aar.Identity{TaskID: "CW-X"}, aar.Reflection{})
	assert.Contains(t, out, "schema: aar/v1")
	assert.Contains(t, out, "task_id: CW-X")
	for _, heading := range []string{
		"## Summary",
		"## What was clunky, confusing, or surprising?",
		"## What could be automated or converted to a harness step?",
		"## What manual step should the system have done?",
		"## Sharp edges hit and workarounds",
		"## Suggestions for the next run",
		"## Errors / issues encountered",
	} {
		assert.Contains(t, out, heading, "empty AAR must preserve every section heading")
	}
	// Empty sections render as the (none) placeholder.
	assert.Contains(t, out, "_(none)_")
}

func TestRender_YAMLEscaping(t *testing.T) {
	out := aar.Render(aar.Identity{
		TaskID: "CW-X",
		Title:  `Has: a colon and "quotes"`,
	}, aar.Reflection{})
	assert.Contains(t, out, `title: "Has: a colon and \"quotes\""`)

	_, _, _, err := aar.Parse(out)
	require.NoError(t, err)
}

func TestParse_MissingFrontmatter(t *testing.T) {
	_, _, _, err := aar.Parse("# After-Action Report\n\nbody")
	require.Error(t, err)
}

func TestParse_MissingClosingDelimiter(t *testing.T) {
	_, _, _, err := aar.Parse("---\nschema: aar/v1\n")
	require.Error(t, err)
}

func TestParse_RejectsEmpty(t *testing.T) {
	_, _, _, err := aar.Parse("")
	require.Error(t, err)
}

func TestValidateOutcome(t *testing.T) {
	require.NoError(t, aar.ValidateOutcome(""))
	require.NoError(t, aar.ValidateOutcome(aar.OutcomeSuccess))
	require.NoError(t, aar.ValidateOutcome(aar.OutcomePartial))
	require.NoError(t, aar.ValidateOutcome(aar.OutcomeBlocked))
	require.NoError(t, aar.ValidateOutcome(aar.OutcomeFailed))
	require.Error(t, aar.ValidateOutcome(aar.Outcome("bogus")))
}

func TestReflectionKeys_StableOrder(t *testing.T) {
	keys := aar.ReflectionKeys()
	assert.Equal(t, []string{
		"summary",
		"clunky",
		"automatable",
		"manual_should_be_auto",
		"sharp_edges",
		"suggestions",
		"errors",
	}, keys)
}

func TestRender_Deterministic(t *testing.T) {
	id := aar.Identity{TaskID: "CW-X", RunID: 1, AgentProfile: "x"}
	r := aar.Reflection{Outcome: aar.OutcomePartial, Summary: "summary"}
	a := aar.Render(id, r)
	b := aar.Render(id, r)
	assert.Equal(t, a, b)
}

func TestRender_OmitsZeroIdentityFields(t *testing.T) {
	out := aar.Render(aar.Identity{TaskID: "CW-X"}, aar.Reflection{})
	// No run_id should appear when zero.
	assert.NotContains(t, out, "run_id:")
	// No session_id key when empty.
	assert.NotContains(t, out, "session_id:")
}

func TestParse_IgnoresUnknownFrontmatterKeys(t *testing.T) {
	doc := strings.Join([]string{
		"---",
		"schema: aar/v1",
		"task_id: CW-X",
		"unknown_key: whatever",
		"---",
		"",
		"# After-Action Report",
		"",
		"## Summary",
		"",
		"hello",
		"",
	}, "\n")
	id, _, schema, err := aar.Parse(doc)
	require.NoError(t, err)
	assert.Equal(t, "aar/v1", schema)
	assert.Equal(t, "CW-X", id.TaskID)
}

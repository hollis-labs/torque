package mcpadapter_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/aar"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// canonicalReflection returns a non-trivial reflection_json blob used by
// the happy-path tests. Keeps the test bodies focused on assertions rather
// than the payload shape.
func canonicalReflection(extras ...func(map[string]any)) map[string]any {
	r := map[string]any{
		"summary":               "Built the AAR system end-to-end and verified locally.",
		"outcome":               "success",
		"clunky":                "Could not find docs/torque-agent-guide referenced in the boot.",
		"automatable":           "Booting an interim AAR template via a make target.",
		"manual_should_be_auto": "RunID lookup at submission time.",
		"sharp_edges":           "Loopback handler does not get TORQUE_WORK_ROOT.",
		"suggestions":           "Thread the work_root into the loopback so file paths can be authoritative.",
		"errors":                []any{"flaky test in pkg foo", "transient gh rate limit"},
	}
	for _, f := range extras {
		f(r)
	}
	return r
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestLoopback_AARSubmit_PersistsArtifactAndStampsMetadata(t *testing.T) {
	fix := setupLoopback(t)
	fix.promote(t, "doing")

	text, isErr := callTool(t, fix.loopback, "torque_aar_submit", map[string]interface{}{
		"reflection_json": mustJSON(t, canonicalReflection()),
	})
	require.False(t, isErr, "aar submit: %s", text)

	var rec map[string]interface{}
	parseData(t, text, &rec)

	assert.Equal(t, fix.taskID, rec["TaskID"])
	assert.Equal(t, aar.ArtifactType, rec["Type"])
	body, _ := rec["Content"].(string)
	require.NotEmpty(t, body, "AAR body must be persisted in Content")
	assert.Contains(t, body, "schema: aar/v1")
	assert.Contains(t, body, "task_id: "+fix.taskID)
	assert.Contains(t, body, "outcome: success")
	assert.Contains(t, body, "Built the AAR system end-to-end")

	// Metadata is a JSON-encoded string on the artifact record (PascalCase
	// field per the schema).
	metaWrap, ok := rec["Metadata"].(map[string]interface{})
	require.True(t, ok, "Metadata should marshal as sql.NullString-shaped map; got %T", rec["Metadata"])
	require.True(t, metaWrap["Valid"].(bool), "Metadata.Valid should be true")
	rawMeta, _ := metaWrap["String"].(string)
	require.NotEmpty(t, rawMeta)
	var meta map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(rawMeta), &meta))
	assert.Equal(t, "aar/v1", meta["schema"])
	assert.Equal(t, "success", meta["outcome"])
	assert.EqualValues(t, 7, int(meta["reflection_populated"].(float64)),
		"7 sections populated: summary + 5 reflection questions + errors (errors counted when non-empty)")
	assert.EqualValues(t, 2, int(meta["errors_count"].(float64)))
}

func TestLoopback_AARSubmit_RejectsMissingPayload(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_aar_submit", map[string]interface{}{})
	require.True(t, isErr)
	code, _, field := parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "reflection_json", field)
}

func TestLoopback_AARSubmit_RejectsMalformedJSON(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_aar_submit", map[string]interface{}{
		"reflection_json": "this is not JSON",
	})
	require.True(t, isErr)
	code, _, field := parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "reflection_json", field)
}

func TestLoopback_AARSubmit_RejectsInvalidOutcome(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_aar_submit", map[string]interface{}{
		"reflection_json": mustJSON(t, canonicalReflection(func(m map[string]any) {
			m["outcome"] = "weird-state"
		})),
	})
	require.True(t, isErr)
	code, _, field := parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "outcome", field)
}

func TestLoopback_AARSubmit_EmptyOutcomeDefaultsToSuccess(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_aar_submit", map[string]interface{}{
		"reflection_json": mustJSON(t, canonicalReflection(func(m map[string]any) {
			m["outcome"] = ""
		})),
	})
	require.False(t, isErr, "aar submit: %s", text)

	var rec map[string]interface{}
	parseData(t, text, &rec)
	body, _ := rec["Content"].(string)
	assert.Contains(t, body, "outcome: success")
}

func TestLoopback_AARSubmit_EmptyReflectionStillFiles(t *testing.T) {
	// A vacuous AAR (no friction surfaced) is still a queryable signal.
	fix := setupLoopback(t)

	empty := map[string]any{
		"summary":               "",
		"outcome":               "success",
		"clunky":                "",
		"automatable":           "",
		"manual_should_be_auto": "",
		"sharp_edges":           "",
		"suggestions":           "",
	}
	text, isErr := callTool(t, fix.loopback, "torque_aar_submit", map[string]interface{}{
		"reflection_json": mustJSON(t, empty),
	})
	require.False(t, isErr, "empty reflection should still file: %s", text)

	var rec map[string]interface{}
	parseData(t, text, &rec)
	body, _ := rec["Content"].(string)
	// Every section heading must still be present so the document shape
	// is uniform across AARs even when the agent surfaced nothing.
	for _, h := range []string{
		"## Summary",
		"## What was clunky, confusing, or surprising?",
		"## What could be automated or converted to a harness step?",
	} {
		assert.Contains(t, body, h)
	}
	// Metadata reports zero populated sections (every reflection field empty).
	metaWrap, _ := rec["Metadata"].(map[string]interface{})
	rawMeta, _ := metaWrap["String"].(string)
	var meta map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(rawMeta), &meta))
	assert.EqualValues(t, 0, int(meta["reflection_populated"].(float64)))
}

func TestLoopback_AARSubmit_ParseRoundTripPreservesIdentity(t *testing.T) {
	fix := setupLoopback(t)
	fix.promote(t, "doing")

	text, isErr := callTool(t, fix.loopback, "torque_aar_submit", map[string]interface{}{
		"reflection_json": mustJSON(t, canonicalReflection()),
	})
	require.False(t, isErr, "aar submit: %s", text)

	var rec map[string]interface{}
	parseData(t, text, &rec)
	body, _ := rec["Content"].(string)

	id, reflection, schema, err := aar.Parse(body)
	require.NoError(t, err)
	assert.Equal(t, aar.Schema, schema)
	assert.Equal(t, fix.taskID, id.TaskID)
	assert.Equal(t, "Built the AAR system end-to-end and verified locally.", reflection.Summary)
	assert.Equal(t, aar.OutcomeSuccess, reflection.Outcome)
	assert.Equal(t, []string{"flaky test in pkg foo", "transient gh rate limit"}, reflection.Errors)
}

func TestLoopback_AARSubmit_AttachesRunID(t *testing.T) {
	// Seed a run directly so the latest-run lookup has something to find.
	// This mirrors the production path where the scheduler creates a run
	// before the worker boots; the loopback handler picks up the most-
	// recently-started run on AAR submission.
	fix := setupLoopback(t)
	runID, err := fix.store().CreateRun(&sqlstore.RunRecord{
		TaskID:   fix.taskID,
		Executor: "cli",
	})
	require.NoError(t, err)
	require.NotZero(t, runID)

	text, isErr := callTool(t, fix.loopback, "torque_aar_submit", map[string]interface{}{
		"reflection_json": mustJSON(t, canonicalReflection()),
	})
	require.False(t, isErr, "aar submit: %s", text)

	var rec map[string]interface{}
	parseData(t, text, &rec)

	// ArtifactRecord.RunID marshals as a sql.NullInt64 shape.
	runIDField, ok := rec["RunID"].(map[string]interface{})
	require.True(t, ok, "RunID should marshal as NullInt64 map; got %T", rec["RunID"])
	require.True(t, runIDField["Valid"].(bool), "RunID.Valid should be true after run lookup")
	assert.EqualValues(t, runID, int64(runIDField["Int64"].(float64)))

	body, _ := rec["Content"].(string)
	assert.Contains(t, body, "run_id:")
}

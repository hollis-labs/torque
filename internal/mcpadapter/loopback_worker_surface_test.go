package mcpadapter_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// setupLoopbackForWorker creates a loopback-mode adapter bound to a
// freshly-created kind=agent task in "doing" state. Returns the adapter,
// the bound task ID, and the service handle so tests can exercise the
// worker self-task surface AND verify side-effects via the cross-task
// service API (the loopback's tool catalog deliberately excludes the
// checkpoint_get / checkpoint_list discovery tools — they would let a
// worker read another task's checkpoint history).
func setupLoopbackForWorker(t *testing.T) (*mcpadapter.Adapter, string, *service.Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc := service.New(store)

	const taskID = "CW-TEST-WORKER-0001"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "worker loopback test task",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "implementer",
	}))
	return mcpadapter.NewLoopback(svc, taskID), taskID, svc
}

// TestLoopback_TaskGet_ReturnsBoundTask covers the canonical shape: the
// worker calls torque_task_get with no id (or with its own id) and gets
// its full task record back, including metadata. The task body is what
// the existing checkpoint-redispatch protocol expects to read for
// metadata.checkpoint_responses each turn — before Phase 2 this tool was
// missing from the loopback, leaving the protocol undeliverable.
func TestLoopback_TaskGet_ReturnsBoundTask(t *testing.T) {
	a, taskID, _ := setupLoopbackForWorker(t)

	t.Run("no id argument returns bound task", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{})
		require.False(t, isErr, text)
		var rec map[string]interface{}
		parseData(t, text, &rec)
		assert.Equal(t, taskID, rec["ID"])
	})

	t.Run("matching id argument returns bound task", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{"id": taskID})
		require.False(t, isErr, text)
		var rec map[string]interface{}
		parseData(t, text, &rec)
		assert.Equal(t, taskID, rec["ID"])
	})
}

// TestLoopback_TaskGet_RejectsForeignTaskID covers the security boundary
// that keeps the loopback's self-task contract honest: an explicit id arg
// that doesn't match the bound task is rejected. Without this check the
// worker could read any other task's body — silently widening the
// loopback's read surface past the documented "agents can only act on
// their own task" contract.
func TestLoopback_TaskGet_RejectsForeignTaskID(t *testing.T) {
	a, _, _ := setupLoopbackForWorker(t)
	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{"id": "CW-SOMEONE-ELSES-0001"})
	require.True(t, isErr, "expected error response for foreign task id, got: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "id", field)
}

// TestLoopback_CheckpointEmit_PinsToOwnTask covers the help-asking
// primitive's happy path: the worker emits a typed approval checkpoint;
// the checkpoint row materializes against the worker's own task without
// the worker having to pass task_id. The emitter source type is force-
// stamped to "agent" so dashboards see the right provenance — workers
// can't accidentally mislabel themselves as "system".
func TestLoopback_CheckpointEmit_PinsToOwnTask(t *testing.T) {
	a, taskID, svc := setupLoopbackForWorker(t)

	text, isErr := callTool(t, a, "torque_task_checkpoint_emit", map[string]interface{}{
		"type":         "approval",
		"payload_json": `{"title":"Need DB creds","prompt":"block or skip?"}`,
	})
	require.False(t, isErr, text)

	var emitted map[string]interface{}
	parseData(t, text, &emitted)
	corr, _ := emitted["CorrelationID"].(string)
	require.NotEmpty(t, corr)
	assert.Equal(t, "pending", emitted["Status"])

	// Verify the checkpoint actually landed against the worker's task and
	// the source type came across as "agent" — not "system" (which would
	// be the cross-task tool's default for an omitted emitter_source_type).
	// torque_task_checkpoint_get is deliberately absent from the loopback
	// surface (discovery tools are excluded; an agent should only read
	// what was redispatched to it via task.metadata.checkpoint_responses),
	// so verify via the service layer directly — the same shape an
	// operator's dashboard would see.
	got, err := svc.Checkpoint.Get(corr)
	require.NoError(t, err)
	assert.Equal(t, taskID, got.TaskID)
	assert.Equal(t, "approval", got.Type)
	assert.Equal(t, "agent", got.EmitterSourceType)
}

// TestLoopback_CheckpointRespond_PinsToOwnTask covers the dual case to
// emit — a worker can respond to its OWN pending checkpoint (after the
// substrate has redispatched it with a response). The responder source
// type is force-stamped to "agent" for the same provenance reason as
// emit.
func TestLoopback_CheckpointRespond_PinsToOwnTask(t *testing.T) {
	a, _, _ := setupLoopbackForWorker(t)

	// Emit first so we have a correlation_id to respond to.
	emitText, _ := callTool(t, a, "torque_task_checkpoint_emit", map[string]interface{}{
		"type":         "approval",
		"payload_json": `{"title":"x","prompt":"y"}`,
	})
	var emitted map[string]interface{}
	parseData(t, emitText, &emitted)
	corr := emitted["CorrelationID"].(string)

	// Respond.
	respText, isErr := callTool(t, a, "torque_task_checkpoint_respond", map[string]interface{}{
		"correlation_id": corr,
		"response_json":  `{"decision":"approved","comment":"ack"}`,
	})
	require.False(t, isErr, respText)
	var responded map[string]interface{}
	parseData(t, respText, &responded)
	assert.Equal(t, "responded", responded["Status"])
	// ResponderSourceType is serialized as a sql.NullString — Go's stdlib
	// json marshals that as {String, Valid}. The contract we care about is
	// that the loopback force-stamped "agent" regardless of what the
	// worker passed (or didn't pass).
	rst, _ := responded["ResponderSourceType"].(map[string]interface{})
	require.NotNil(t, rst, "ResponderSourceType missing or unexpected shape: %v", responded["ResponderSourceType"])
	assert.Equal(t, true, rst["Valid"])
	assert.Equal(t, "agent", rst["String"])
}

// TestLoopback_CheckpointRespond_RejectsForeignCorrelation is the security
// guard: a worker must not be able to respond to checkpoints belonging to
// other tasks. The handler resolves the checkpoint's task_id from the
// store and rejects the call when it doesn't match the loopback's bound
// task. Without this, a worker accidentally re-using a correlation_id
// could silently consume another task's pending question to the user.
func TestLoopback_CheckpointRespond_RejectsForeignCorrelation(t *testing.T) {
	// Set up TWO tasks, get a checkpoint on the second, then try to
	// respond from a loopback bound to the first.
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc := service.New(store)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-WORKER-A", Title: "a", Status: "doing", Executor: "cli", Kind: "agent", AgentProfile: "implementer",
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-WORKER-B", Title: "b", Status: "doing", Executor: "cli", Kind: "agent", AgentProfile: "implementer",
	}))

	// B emits a checkpoint via the cross-task service surface (simulates a
	// different worker / surface emitting it).
	emitOut, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:            "CW-WORKER-B",
		Type:              "approval",
		PayloadJSON:       `{}`,
		EmitterSourceType: "agent",
	})
	require.NoError(t, err)

	// A's loopback now tries to respond to B's correlation_id.
	aLoopback := mcpadapter.NewLoopback(svc, "CW-WORKER-A")
	text, isErr := callTool(t, aLoopback, "torque_task_checkpoint_respond", map[string]interface{}{
		"correlation_id": emitOut.CorrelationID,
		"response_json":  `{}`,
	})
	require.True(t, isErr, "expected error for cross-task respond, got: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "correlation_id", field)
}

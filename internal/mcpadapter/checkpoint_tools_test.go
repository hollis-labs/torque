package mcpadapter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCP_Checkpoint_EmitRespondGetList(t *testing.T) {
	a := setupAdapter(t)

	// Create a decision task and move it to doing.
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":           "decision task",
		"description":     "x",
		"kind":            "decision",
		"manual":          true,
		"checkpoint_mode": "blocking",
	})
	require.False(t, isErr, text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID := created["ID"].(string)

	_, terr := callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id": taskID, "status": "doing",
	})
	require.False(t, terr)

	// Emit
	emitText, isErr := callTool(t, a, "torque_task_checkpoint_emit", map[string]interface{}{
		"task_id":             taskID,
		"type":                "collect_data",
		"payload_json":        `{"q":"?"}`,
		"emitter_source_type": "system",
	})
	require.False(t, isErr, emitText)
	var emitted map[string]interface{}
	parseData(t, emitText, &emitted)
	corr := emitted["CorrelationID"].(string)
	assert.Equal(t, "pending", emitted["Status"])
	assert.NotEmpty(t, corr)

	// Respond
	respText, isErr := callTool(t, a, "torque_task_checkpoint_respond", map[string]interface{}{
		"correlation_id":        corr,
		"response_json":         `{"a":1}`,
		"responder_source_type": "user",
		"responder_source_ref":  "chrispian",
	})
	require.False(t, isErr, respText)
	var responded map[string]interface{}
	parseData(t, respText, &responded)
	assert.Equal(t, "responded", responded["Status"])

	// Get
	getText, isErr := callTool(t, a, "torque_task_checkpoint_get", map[string]interface{}{
		"correlation_id": corr,
	})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, getText, &got)
	assert.Equal(t, "responded", got["Status"])

	// List for task — new {items, meta} envelope with brief default shape
	listText, isErr := callTool(t, a, "torque_task_checkpoint_list", map[string]interface{}{
		"task_id": taskID,
	})
	require.False(t, isErr)
	var listEnv struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, listText, &listEnv)
	require.Len(t, listEnv.Items, 1)
	// Brief shape uses snake_case correlation_id.
	assert.Equal(t, corr, listEnv.Items[0]["correlation_id"])
}

func TestMCP_Checkpoint_Cancel(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":           "decision cancel",
		"description":     "x",
		"kind":            "decision",
		"manual":          true,
		"checkpoint_mode": "blocking",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID := created["ID"].(string)

	_, _ = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id": taskID, "status": "doing",
	})

	emitText, _ := callTool(t, a, "torque_task_checkpoint_emit", map[string]interface{}{
		"task_id":             taskID,
		"type":                "collect_data",
		"payload_json":        `{}`,
		"emitter_source_type": "system",
	})
	var emitted map[string]interface{}
	parseData(t, emitText, &emitted)
	corr := emitted["CorrelationID"].(string)

	cancelText, isErr := callTool(t, a, "torque_task_checkpoint_cancel", map[string]interface{}{
		"correlation_id":       corr,
		"reason":               "no longer relevant",
		"canceler_source_type": "user",
		"canceler_source_ref":  "chrispian",
	})
	require.False(t, isErr, cancelText)
	var canceled map[string]interface{}
	parseData(t, cancelText, &canceled)
	assert.Equal(t, "canceled", canceled["Status"])
}

func TestMCP_Checkpoint_Pending(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":           "decision pending",
		"description":     "x",
		"kind":            "decision",
		"manual":          true,
		"checkpoint_mode": "blocking",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID := created["ID"].(string)
	_, _ = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id": taskID, "status": "doing",
	})

	_, _ = callTool(t, a, "torque_task_checkpoint_emit", map[string]interface{}{
		"task_id":             taskID,
		"type":                "collect_data",
		"payload_json":        `{}`,
		"emitter_source_type": "system",
	})

	pendingText, isErr := callTool(t, a, "torque_task_checkpoints_pending", map[string]interface{}{})
	require.False(t, isErr)
	var pendingEnv struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, pendingText, &pendingEnv)
	require.Len(t, pendingEnv.Items, 1)
	// Brief shape uses lowercase "status".
	assert.Equal(t, "pending", pendingEnv.Items[0]["status"])
}

func TestMCP_Checkpoint_EmitTimeoutAtSet(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":           "decision to",
		"description":     "x",
		"kind":            "decision",
		"manual":          true,
		"checkpoint_mode": "blocking",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID := created["ID"].(string)
	_, _ = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id": taskID, "status": "doing",
	})

	deadline := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	emitText, isErr := callTool(t, a, "torque_task_checkpoint_emit", map[string]interface{}{
		"task_id":             taskID,
		"type":                "collect_data",
		"payload_json":        `{}`,
		"emitter_source_type": "system",
		"timeout_at":          deadline,
	})
	require.False(t, isErr, emitText)
	var emitted map[string]interface{}
	parseData(t, emitText, &emitted)
	corr := emitted["CorrelationID"].(string)

	getText, _ := callTool(t, a, "torque_task_checkpoint_get", map[string]interface{}{
		"correlation_id": corr,
	})
	var got map[string]interface{}
	parseData(t, getText, &got)
	timeoutAt := got["TimeoutAt"].(map[string]interface{})
	assert.Equal(t, true, timeoutAt["Valid"])
}

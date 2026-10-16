package mcpadapter_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPSelfReport(t *testing.T) {
	a := setupAdapter(t)
	text, failed := callTool(t, a, "torque_task_create", map[string]any{"title": "report", "progress_detail": "step"})
	require.False(t, failed, text)
	var task map[string]any
	parseData(t, text, &task)
	in := map[string]any{"task_id": task["ID"], "label": "report", "idempotency_key": "p"}
	text, failed = callTool(t, a, "torque_task_progress", in)
	require.False(t, failed, text)
	var first map[string]any
	parseData(t, text, &first)
	require.Equal(t, true, first["emitted"])
	text, failed = callTool(t, a, "torque_task_progress", in)
	require.False(t, failed, text)
	var duplicate map[string]any
	parseData(t, text, &duplicate)
	require.Equal(t, "duplicate", duplicate["suppressed_reason"])
	require.Equal(t, first["event_id"], duplicate["event_id"])
	text, failed = callTool(t, a, "torque_task_data_emit", map[string]any{"task_id": task["ID"], "schema": "x@1", "data": map[string]any{}, "idempotency_key": "p"})
	require.True(t, failed, text)
	require.Contains(t, text, "idempotency_conflict")
}

func TestMCPStdioSelfReportProxy(t *testing.T) {
	var requests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		require.Equal(t, "/api/v1/tasks/T-REMOTE/progress", r.URL.Path)
		var in map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, "remote", in["label"])
		requests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"emitted": true, "event_id": "evt_remote", "effective_level": "step", "detail_source": "task"})
	}))
	defer ts.Close()
	a := setupAdapter(t).WithSelfReportProxy(ts.URL, "test-token")
	// No local task exists: success proves the call reached the daemon.
	text, failed := callTool(t, a, "torque_task_progress", map[string]any{"task_id": "T-REMOTE", "label": "remote"})
	require.False(t, failed, text)
	require.Contains(t, text, "evt_remote")
	require.Equal(t, 1, requests)
	ts.Close()
	text, failed = callTool(t, a, "torque_task_progress", map[string]any{"task_id": "T-REMOTE", "label": "remote"})
	require.True(t, failed, text)
	require.Contains(t, text, "unavailable")
}

func TestLoopbackSelfReport(t *testing.T) {
	fix := setupLoopback(t)
	text, failed := callTool(t, fix.loopback, "torque_task_progress", map[string]any{"label": "phase", "level": "milestone"})
	require.False(t, failed, text)
	var out map[string]any
	parseData(t, text, &out)
	require.Equal(t, true, out["emitted"])
	text, failed = callTool(t, fix.loopback, "torque_task_progress", map[string]any{"task_id": "another", "label": "phase", "level": "milestone"})
	require.True(t, failed, text)
	text, failed = callTool(t, fix.loopback, "torque_task_data_emit", map[string]any{"schema": "x@1", "data": map[string]any{}})
	require.False(t, failed, text)
}

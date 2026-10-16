package mcpadapter_test

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMCPProgressDetailOriginAndClear(t *testing.T) {
	a := setupAdapter(t)
	text, failed := callTool(t, a, "torque_template_create", map[string]any{"id": "detail", "name": "Detail", "progress_detail": "step"})
	require.False(t, failed, text)
	text, failed = callTool(t, a, "torque_task_create_from_template", map[string]any{"template_id": "detail", "title": "child"})
	require.False(t, failed, text)
	var task map[string]any
	parseData(t, text, &task)
	id := task["ID"].(string)
	text, failed = callTool(t, a, "torque_task_get", map[string]any{"id": id, "format": "typed"})
	require.False(t, failed, text)
	parseData(t, text, &task)
	require.Equal(t, "step", task["progress_detail"])
	require.Equal(t, "template", task["progress_detail_source"])
	for _, level := range []string{"step", ""} {
		text, failed = callTool(t, a, "torque_task_update", map[string]any{"id": id, "progress_detail": level})
		require.False(t, failed, text)
		text, failed = callTool(t, a, "torque_task_get", map[string]any{"id": id, "format": "typed"})
		require.False(t, failed, text)
		parseData(t, text, &task)
		if level == "" {
			require.Nil(t, task["progress_detail"])
			require.Nil(t, task["progress_detail_source"])
		} else {
			require.Equal(t, level, task["progress_detail"])
			require.Equal(t, "task", task["progress_detail_source"])
		}
	}
}

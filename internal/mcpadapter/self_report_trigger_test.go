package mcpadapter

import (
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestSelfReportTriggerThroughMCPMiddleware(t *testing.T) {
	for _, loopback := range []bool{false, true} {
		name := "remote"
		if loopback {
			name = "worker_loopback"
		}
		t.Run(name, func(t *testing.T) {
			store, svc := newTriggerStore(t)
			const taskID = "CW-REPORT-TRIGGER"
			require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: taskID, Title: "report", Priority: 2}))
			a := New(svc, nil)
			if loopback {
				a = NewLoopback(svc, taskID)
			}
			for _, report := range []struct {
				tool, kind string
				input      map[string]any
			}{
				{"torque_task_progress", catalog.KindProgressCheckpoint, map[string]any{"label": "ready", "level": "milestone"}},
				{"torque_task_data_emit", catalog.KindDataEmitted, map[string]any{"schema": "example@1", "data": map[string]any{"ready": true}}},
			} {
				if !loopback {
					report.input["task_id"] = taskID
				}
				callOverProtocol(t, a, report.tool, report.input)
				var trigger, kind string
				require.NoError(t, store.DB().QueryRow(`SELECT trigger_kind, kind FROM event_outbox WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, taskID).Scan(&trigger, &kind))
				require.Equal(t, sqlstore.TriggerAgent, trigger)
				require.Equal(t, report.kind, kind)
			}
		})
	}
}

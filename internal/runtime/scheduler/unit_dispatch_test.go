package scheduler_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// A picked task with an execution plan goes to the unit dispatcher, not an
// executor; a plain task still takes the executor path.
func TestTickRoutesUnitPlanTasksToUnitDispatcher(t *testing.T) {
	sched, store, mock := setupScheduler(t)

	var mu sync.Mutex
	var got []string
	sched.SetUnitDispatcher(func(_ context.Context, task sqlstore.TaskRecord) (bool, error) {
		if !service.HasUnitPlan(task) {
			return false, nil
		}
		mu.Lock()
		got = append(got, task.ID)
		mu.Unlock()
		return true, nil
	})

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-UNIT-1", Title: "unit", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock",
		Metadata: sql.NullString{Valid: true, String: `{"unit_plan":{"workstream_id":"ws","goal":"g","done_when":["d"]}}`},
	}))

	require.NoError(t, sched.Tick(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"CW-UNIT-1"}, got)
	assert.Zero(t, mock.RunCount(), "a unit task must not reach an executor")
}

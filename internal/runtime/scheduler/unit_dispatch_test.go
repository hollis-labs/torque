package scheduler_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
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

type noTeams struct{}

func (noTeams) FormTeam(context.Context, string, tetherclient.TeamForm) (*tetherclient.TeamResult, error) {
	return nil, errors.New("must not be called")
}
func (noTeams) DelegateTeam(context.Context, string, string, string, string) (*tetherclient.TeamResult, error) {
	return nil, errors.New("must not be called")
}
func (noTeams) DissolveTeam(context.Context, string, string) error { return nil }
func (noTeams) TeamRun(context.Context, string) (*tetherclient.TeamRunInfo, error) {
	return nil, tetherclient.ErrRunNotFound
}

// A plan the dispatcher refuses (three workers, no pool identities) is parked,
// so the second tick does not pick it again.
func TestRefusedUnitIsPickedOnce(t *testing.T) {
	sched, store, _ := setupScheduler(t)
	svc := service.New(store)
	svc.Unit.Configure(noTeams{}, service.UnitConfig{})
	t.Cleanup(svc.Unit.Stop)

	var mu sync.Mutex
	picks := 0
	sched.SetUnitDispatcher(func(ctx context.Context, task sqlstore.TaskRecord) (bool, error) {
		mu.Lock()
		picks++
		mu.Unlock()
		return svc.Unit.DispatchTask(ctx, task)
	})
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-UNIT-2", Title: "unit", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock",
		Metadata: sql.NullString{Valid: true, String: `{"unit_plan":{"workstream_id":"ws","goal":"g","done_when":["d"],"workers":3}}`},
	}))

	require.NoError(t, sched.Tick(context.Background()))
	require.NoError(t, sched.Tick(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, picks)
	rec, err := store.GetTask("CW-UNIT-2")
	require.NoError(t, err)
	assert.True(t, rec.Manual)
	assert.Contains(t, rec.BlockedReason, "pool identities")
}

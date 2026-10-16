package scheduler_test

import (
	"context"
	"database/sql"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestLaunchProgressDetailDispatch(t *testing.T) {
	for _, detail := range []string{"", "verbose"} {
		t.Run(detail, func(t *testing.T) {
			sched, store, exec := setupScheduler(t)
			require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "launch-detail", Title: "launch", Status: "todo", Executor: "mock", AgentProfile: "mock", ProgressDetail: sql.NullString{String: "off", Valid: true}}))
			require.NoError(t, sched.Tick(scheduler.WithLaunchProgressDetail(context.Background(), detail)))
			require.Eventually(t, func() bool { return exec.RunCount() > 0 }, time.Second, 10*time.Millisecond)
			jobs := exec.RecordedJobs()
			require.Equal(t, detail, jobs[0].ProgressDetail)
			runs, err := store.ListRuns("launch-detail")
			require.NoError(t, err)
			require.Len(t, runs, 1)
			require.Equal(t, detail, runs[0].ProgressDetail.String)
			require.Equal(t, detail != "", runs[0].ProgressDetail.Valid)
		})
	}
}

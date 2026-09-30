package scheduler_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/queue"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/require"
)

// usageExecutor emits one usage delta, signals that it did, then ends the
// run the way `end` says: succeed, fail, or block until cancelled.
type usageExecutor struct {
	end     string
	emitted chan struct{}
}

func (u *usageExecutor) Name() string { return "usage" }
func (u *usageExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{}
}
func (u *usageExecutor) Validate(*executor.ExecutionJob) error { return nil }
func (u *usageExecutor) Run(ctx context.Context, _ *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	cb(executor.TokenEvent(10, 5, 0))
	close(u.emitted)
	switch u.end {
	case "fail":
		return nil, errors.New("boom")
	case "cancel":
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &executor.ExecutionResult{Status: "done"}, nil
}

// TestTokenTotalsReleasedOnEveryRunEnd guards against the per-run running
// total leaking on a long-lived scheduler: whether the run succeeds, fails
// or is cancelled mid-flight, its entry is gone once the worker exits.
func TestTokenTotalsReleasedOnEveryRunEnd(t *testing.T) {
	for _, end := range []string{"done", "fail", "cancel"} {
		t.Run(end, func(t *testing.T) {
			store := sqlitetest.OpenStore(t)
			q, err := queue.Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
			require.NoError(t, err)
			ex := &usageExecutor{end: end, emitted: make(chan struct{})}
			registry := executor.NewRegistry()
			registry.Register(ex)
			sched := scheduler.New(store, q, registry, nil, &config.SchedulerConfig{
				Workers: 1, IntervalSeconds: 1, RetryBudget: 0, Enabled: true, StaleSeconds: 300,
			})
			t.Cleanup(func() {
				sched.Stop(context.Background())
				q.Close()
				store.Close()
			})

			id := "CW-TOK-" + end
			require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
				ID: id, Title: "T", Status: "todo", Priority: 1,
				Executor: "usage", AgentProfile: "usage", OnDone: "close",
			}))
			require.NoError(t, sched.Tick(context.Background()))

			select {
			case <-ex.emitted:
			case <-time.After(3 * time.Second):
				t.Fatal("executor never ran")
			}
			if end == "cancel" {
				require.Equal(t, 1, sched.TokenTotalsTracked(), "live run holds its total")
				require.NoError(t, store.TransitionTask(id, "review"))
			}
			require.Eventually(t, func() bool { return sched.TokenTotalsTracked() == 0 },
				3*time.Second, 10*time.Millisecond, "run total must be released when the run ends (%s)", end)
			sched.DrainResults()
		})
	}
}

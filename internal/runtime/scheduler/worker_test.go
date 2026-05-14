package scheduler_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerPoolConcurrencyLimit(t *testing.T) {
	pool := scheduler.NewWorkerPool(2)
	defer pool.Shutdown(context.Background())

	var maxConcurrent int64
	var current int64
	var mu sync.Mutex
	done := make(chan struct{}, 5)

	for i := 0; i < 5; i++ {
		taskID := "task-" + string(rune('A'+i))
		pool.Submit(taskID, 0, func(ctx context.Context) (*executor.ExecutionResult, error) {
			c := atomic.AddInt64(&current, 1)
			mu.Lock()
			if c > maxConcurrent {
				maxConcurrent = c
			}
			mu.Unlock()

			time.Sleep(50 * time.Millisecond)
			atomic.AddInt64(&current, -1)
			done <- struct{}{}
			return &executor.ExecutionResult{Status: "done"}, nil
		})
	}

	for i := 0; i < 5; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for workers")
		}
	}

	assert.LessOrEqual(t, maxConcurrent, int64(2), "should never exceed max workers")
}

func TestWorkerPoolGracefulShutdown(t *testing.T) {
	pool := scheduler.NewWorkerPool(2)

	var completed int64
	for i := 0; i < 3; i++ {
		taskID := "task-" + string(rune('A'+i))
		pool.Submit(taskID, 0, func(ctx context.Context) (*executor.ExecutionResult, error) {
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt64(&completed, 1)
			return &executor.ExecutionResult{Status: "done"}, nil
		})
	}

	// Give workers time to start
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := pool.Shutdown(ctx)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, atomic.LoadInt64(&completed), int64(2), "should complete in-flight workers")
}

func TestWorkerPoolActiveCount(t *testing.T) {
	pool := scheduler.NewWorkerPool(3)
	defer pool.Shutdown(context.Background())

	assert.Equal(t, 0, pool.ActiveCount())

	started := make(chan struct{})
	blocked := make(chan struct{})

	pool.Submit("task-A", 0, func(ctx context.Context) (*executor.ExecutionResult, error) {
		started <- struct{}{}
		<-blocked
		return &executor.ExecutionResult{Status: "done"}, nil
	})

	<-started
	assert.Equal(t, 1, pool.ActiveCount())

	close(blocked)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 0, pool.ActiveCount())
}

func TestWorkerPoolAvailableSlots(t *testing.T) {
	pool := scheduler.NewWorkerPool(3)
	defer pool.Shutdown(context.Background())

	assert.Equal(t, 3, pool.AvailableSlots())

	started := make(chan struct{})
	blocked := make(chan struct{})

	pool.Submit("task-A", 0, func(ctx context.Context) (*executor.ExecutionResult, error) {
		started <- struct{}{}
		<-blocked
		return &executor.ExecutionResult{Status: "done"}, nil
	})

	<-started
	assert.Equal(t, 2, pool.AvailableSlots())

	close(blocked)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 3, pool.AvailableSlots())
}

func TestWorkerPoolResultCallback(t *testing.T) {
	pool := scheduler.NewWorkerPool(2)
	defer pool.Shutdown(context.Background())

	results := make(chan scheduler.WorkerResult, 1)
	pool.OnResult(func(r scheduler.WorkerResult) {
		results <- r
	})

	pool.Submit("CW-0001", 0, func(ctx context.Context) (*executor.ExecutionResult, error) {
		return &executor.ExecutionResult{Status: "done", Cost: 0.05}, nil
	})

	select {
	case r := <-results:
		assert.Equal(t, "CW-0001", r.TaskID)
		assert.Equal(t, "done", r.Result.Status)
		assert.NoError(t, r.Err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for result")
	}
}

func TestWorkerPoolErrorCallback(t *testing.T) {
	pool := scheduler.NewWorkerPool(2)
	defer pool.Shutdown(context.Background())

	results := make(chan scheduler.WorkerResult, 1)
	pool.OnResult(func(r scheduler.WorkerResult) {
		results <- r
	})

	pool.Submit("CW-0001", 0, func(ctx context.Context) (*executor.ExecutionResult, error) {
		return nil, assert.AnError
	})

	select {
	case r := <-results:
		assert.Equal(t, "CW-0001", r.TaskID)
		assert.Nil(t, r.Result)
		assert.Error(t, r.Err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for result")
	}
}

func TestWorkerPoolPassesRunID(t *testing.T) {
	pool := scheduler.NewWorkerPool(1)
	defer pool.Shutdown(context.Background())

	results := make(chan scheduler.WorkerResult, 1)
	pool.OnResult(func(r scheduler.WorkerResult) {
		results <- r
	})

	pool.Submit("CW-0001", 42, func(ctx context.Context) (*executor.ExecutionResult, error) {
		return &executor.ExecutionResult{Status: "done"}, nil
	})

	select {
	case r := <-results:
		assert.Equal(t, "CW-0001", r.TaskID)
		assert.Equal(t, int64(42), r.RunID, "pool should propagate runID to WorkerResult")
		assert.NoError(t, r.Err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for result")
	}
}

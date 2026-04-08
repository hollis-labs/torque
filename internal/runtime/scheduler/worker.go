package scheduler

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// WorkFunc is the function a worker executes. It receives a context that is
// cancelled on pool shutdown.
type WorkFunc func(ctx context.Context) (*executor.ExecutionResult, error)

// WorkerResult is delivered to the OnResult callback when a worker finishes.
type WorkerResult struct {
	TaskID string
	Result *executor.ExecutionResult
	Err    error
}

// WorkerPool manages a fixed set of concurrent workers using a semaphore.
type WorkerPool struct {
	maxWorkers int
	semaphore  chan struct{}
	active     int64
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc

	mu       sync.Mutex
	onResult func(WorkerResult)
}

// NewWorkerPool creates a worker pool with the given concurrency limit.
func NewWorkerPool(maxWorkers int) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		maxWorkers: maxWorkers,
		semaphore:  make(chan struct{}, maxWorkers),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// OnResult registers a callback invoked when any worker completes.
func (p *WorkerPool) OnResult(fn func(WorkerResult)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onResult = fn
}

// Submit enqueues a task for execution. It blocks until a worker slot is
// available (semaphore acquire).
func (p *WorkerPool) Submit(taskID string, fn WorkFunc) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		// Acquire semaphore slot
		select {
		case p.semaphore <- struct{}{}:
		case <-p.ctx.Done():
			p.deliverResult(WorkerResult{TaskID: taskID, Err: p.ctx.Err()})
			return
		}

		atomic.AddInt64(&p.active, 1)
		defer func() {
			atomic.AddInt64(&p.active, -1)
			<-p.semaphore // Release semaphore slot
		}()

		result, err := fn(p.ctx)
		p.deliverResult(WorkerResult{TaskID: taskID, Result: result, Err: err})
	}()
}

func (p *WorkerPool) deliverResult(r WorkerResult) {
	p.mu.Lock()
	fn := p.onResult
	p.mu.Unlock()

	if fn != nil {
		fn(r)
	}
}

// ActiveCount returns the number of currently running workers.
func (p *WorkerPool) ActiveCount() int {
	return int(atomic.LoadInt64(&p.active))
}

// AvailableSlots returns the number of idle worker slots.
func (p *WorkerPool) AvailableSlots() int {
	return p.maxWorkers - p.ActiveCount()
}

// Shutdown cancels the pool context and waits for all active workers to finish.
func (p *WorkerPool) Shutdown(ctx context.Context) error {
	p.cancel()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

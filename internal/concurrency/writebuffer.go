package concurrency

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	queue "github.com/hollis-labs/go-queue"
)

// writeBuffer implements WriteBuffer using go-queue as the hot-tier
// storage (queue.db) and a drain goroutine that batches events into
// the main database via the write serializer.
type writeBuffer struct {
	queue    queue.Queue
	ws       WriteSerializer
	cfg      DrainConfig
	stats    drainStatsInternal
	stopped  chan struct{}
	stopOnce sync.Once
}

type drainStatsInternal struct {
	batchesDrained atomic.Int64
	eventsDrained  atomic.Int64
	lastDrainAt    atomic.Value // time.Time
	lastBatchSize  atomic.Int64
	drainErrors    atomic.Int64
}

const hotEventJobType = "hot_event"

// NewWriteBuffer creates a write buffer backed by a go-queue Queue
// (typically the SQLite driver pointing at queue.db). The drain goroutine
// batches events and writes them to the main database through the
// given WriteSerializer.
func NewWriteBuffer(q queue.Queue, ws WriteSerializer, cfg DrainConfig) WriteBuffer {
	if cfg.QueueName == "" {
		cfg.QueueName = "hot_events"
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.DrainInterval <= 0 {
		cfg.DrainInterval = time.Second
	}
	return &writeBuffer{
		queue:   q,
		ws:      ws,
		cfg:     cfg,
		stopped: make(chan struct{}),
	}
}

// Push enqueues a BufferedEvent to the hot tier (queue.db).
// This is a fast, append-only write to a separate SQLite file.
func (wb *writeBuffer) Push(ctx context.Context, event BufferedEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal buffered event: %w", err)
	}
	return wb.queue.Push(ctx, hotEventJobType, payload, queue.OnQueue(wb.cfg.QueueName))
}

// StartDrain runs the drain loop. It pops events from the hot tier
// in batches and writes them to the main database through the write
// serializer. Blocks until ctx is cancelled or Stop is called.
func (wb *writeBuffer) StartDrain(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			wb.drainAll(context.Background())
			return ctx.Err()
		case <-wb.stopped:
			wb.drainAll(context.Background())
			return nil
		case <-time.After(wb.cfg.DrainInterval):
			wb.drainBatch(ctx)
		}
	}
}

// Stats returns current drain statistics.
func (wb *writeBuffer) Stats() DrainStats {
	var lastDrain time.Time
	if v := wb.stats.lastDrainAt.Load(); v != nil {
		lastDrain = v.(time.Time)
	}

	depth, _ := wb.queue.Size(context.Background(), wb.cfg.QueueName)

	return DrainStats{
		BatchesDrained: wb.stats.batchesDrained.Load(),
		EventsDrained:  wb.stats.eventsDrained.Load(),
		LastDrainAt:    lastDrain,
		LastBatchSize:  int(wb.stats.lastBatchSize.Load()),
		DrainErrors:    wb.stats.drainErrors.Load(),
		QueueDepth:     depth,
	}
}

// Stop signals the drain to finish and shut down.
func (wb *writeBuffer) Stop() {
	wb.stopOnce.Do(func() { close(wb.stopped) })
}

// drainBatch pops up to BatchSize events and writes them in one transaction.
func (wb *writeBuffer) drainBatch(ctx context.Context) {
	events, jobIDs := wb.popBatch(ctx)
	if len(events) == 0 {
		return
	}

	err := wb.ws.Submit(ctx, func(db *sql.DB) error {
		return wb.insertEventBatch(db, events)
	})

	if err != nil {
		wb.stats.drainErrors.Add(1)
		// Release jobs back to queue on failure
		for _, id := range jobIDs {
			_ = wb.queue.Release(ctx, id, 0)
		}
		return
	}

	// Delete successfully drained jobs from the queue
	for _, id := range jobIDs {
		_ = wb.queue.Delete(ctx, id)
	}

	wb.stats.batchesDrained.Add(1)
	wb.stats.eventsDrained.Add(int64(len(events)))
	wb.stats.lastBatchSize.Store(int64(len(events)))
	wb.stats.lastDrainAt.Store(time.Now())
}

// drainAll drains everything remaining in the queue.
func (wb *writeBuffer) drainAll(ctx context.Context) {
	for {
		events, jobIDs := wb.popBatch(ctx)
		if len(events) == 0 {
			return
		}

		err := wb.ws.Submit(ctx, func(db *sql.DB) error {
			return wb.insertEventBatch(db, events)
		})

		if err != nil {
			wb.stats.drainErrors.Add(1)
			for _, id := range jobIDs {
				_ = wb.queue.Release(ctx, id, 0)
			}
			return
		}

		for _, id := range jobIDs {
			_ = wb.queue.Delete(ctx, id)
		}

		wb.stats.batchesDrained.Add(1)
		wb.stats.eventsDrained.Add(int64(len(events)))
		wb.stats.lastBatchSize.Store(int64(len(events)))
		wb.stats.lastDrainAt.Store(time.Now())
	}
}

// popBatch pops up to BatchSize events from the queue.
func (wb *writeBuffer) popBatch(ctx context.Context) ([]BufferedEvent, []string) {
	var events []BufferedEvent
	var jobIDs []string

	for i := 0; i < wb.cfg.BatchSize; i++ {
		job, err := wb.queue.Pop(ctx, wb.cfg.QueueName)
		if err != nil || job == nil {
			break
		}

		var event BufferedEvent
		if err := json.Unmarshal(job.Payload, &event); err != nil {
			// Bad payload — send to failed queue
			_ = wb.queue.Failed(ctx, job, fmt.Sprintf("unmarshal: %v", err))
			continue
		}

		events = append(events, event)
		jobIDs = append(jobIDs, job.ID)
	}

	return events, jobIDs
}

// insertEventBatch inserts a slice of events in a single transaction.
func (wb *writeBuffer) insertEventBatch(db *sql.DB, events []BufferedEvent) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin batch insert: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Build a multi-row INSERT for efficiency.
	var placeholders []string
	var args []interface{}

	for _, e := range events {
		placeholders = append(placeholders, "(?, ?, ?, ?, ?)")
		args = append(args, e.RunID, e.TaskID, e.Type, e.Payload, e.CreatedAt)
	}

	query := fmt.Sprintf(
		"INSERT INTO run_events (run_id, task_id, type, payload, created_at) VALUES %s",
		strings.Join(placeholders, ", "),
	)

	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("batch insert events: %w", err)
	}

	return tx.Commit()
}

package writeq

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

const (
	defaultQueueSize   = 256
	defaultBatchSize   = 32
	defaultBatchWindow = 2 * time.Millisecond
)

// Stats surfaces queue throughput and batching metrics.
type Stats struct {
	Submitted     int64
	Completed     int64
	Failed        int64
	Batches       int64
	OpsInBatches  int64
	LastBatchSize int64
}

// Writer serializes state writes through a single transaction owner.
type Writer interface {
	Submit(ctx context.Context, name string, fn func(*sqlstore.WriteTx) error) error
	Stats() Stats
}

// Direct executes each op immediately in its own write transaction.
type Direct struct {
	store *sqlstore.Store
	stats counters
}

// Options configures the queued writer.
type Options struct {
	QueueSize   int
	MaxBatch    int
	BatchWindow time.Duration
}

// Queue batches state writes onto one long-lived worker goroutine.
type Queue struct {
	store       *sqlstore.Store
	ops         chan op
	maxBatch    int
	batchWindow time.Duration
	stats       counters

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

type op struct {
	name string
	fn   func(*sqlstore.WriteTx) error
	res  chan error
}

type counters struct {
	submitted     atomic.Int64
	completed     atomic.Int64
	failed        atomic.Int64
	batches       atomic.Int64
	opsInBatches  atomic.Int64
	lastBatchSize atomic.Int64
}

// NewDirect returns the non-batching fallback used by tests and narrow callsites.
func NewDirect(store *sqlstore.Store) *Direct {
	return &Direct{store: store}
}

// Submit executes fn synchronously in a single write transaction.
func (d *Direct) Submit(ctx context.Context, _ string, fn func(*sqlstore.WriteTx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d.stats.submitted.Add(1)
	tx, err := d.store.BeginWriteTx(ctx)
	if err != nil {
		d.stats.failed.Add(1)
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		d.stats.failed.Add(1)
		return err
	}
	if err := tx.Commit(); err != nil {
		d.stats.failed.Add(1)
		return err
	}
	d.stats.completed.Add(1)
	d.stats.batches.Add(1)
	d.stats.opsInBatches.Add(1)
	d.stats.lastBatchSize.Store(1)
	return nil
}

// Stats returns cumulative counters for the direct writer.
func (d *Direct) Stats() Stats { return d.stats.snapshot() }

// New constructs a batching writer. Call Run in a goroutine to start it.
func New(store *sqlstore.Store, opts Options) *Queue {
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultQueueSize
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = defaultBatchSize
	}
	if opts.BatchWindow <= 0 {
		opts.BatchWindow = defaultBatchWindow
	}
	return &Queue{
		store:       store,
		ops:         make(chan op, opts.QueueSize),
		maxBatch:    opts.MaxBatch,
		batchWindow: opts.BatchWindow,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
}

// Run drains queued write ops until ctx is cancelled or Stop is called.
func (q *Queue) Run(ctx context.Context) error {
	defer close(q.doneCh)
	for {
		select {
		case <-ctx.Done():
			q.drain(context.Background())
			return ctx.Err()
		case <-q.stopCh:
			q.drain(context.Background())
			return nil
		case first := <-q.ops:
			batch := q.collect(first)
			q.executeBatch(ctx, batch)
		}
	}
}

// Stop asks the queue to drain and exit. Safe to call multiple times.
func (q *Queue) Stop() {
	q.stopOnce.Do(func() { close(q.stopCh) })
}

// Wait blocks until Run exits.
func (q *Queue) Wait() {
	<-q.doneCh
}

// Submit enqueues an op and blocks until its completion ack arrives.
func (q *Queue) Submit(ctx context.Context, name string, fn func(*sqlstore.WriteTx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	item := op{
		name: name,
		fn:   fn,
		res:  make(chan error, 1),
	}
	q.stats.submitted.Add(1)
	select {
	case q.ops <- item:
	case <-q.stopCh:
		q.stats.failed.Add(1)
		return fmt.Errorf("state write queue stopped")
	case <-ctx.Done():
		q.stats.failed.Add(1)
		return ctx.Err()
	}
	select {
	case err := <-item.res:
		if err != nil {
			q.stats.failed.Add(1)
		} else {
			q.stats.completed.Add(1)
		}
		return err
	case <-ctx.Done():
		q.stats.failed.Add(1)
		return ctx.Err()
	}
}

// Stats returns cumulative queue counters.
func (q *Queue) Stats() Stats { return q.stats.snapshot() }

func (q *Queue) collect(first op) []op {
	batch := []op{first}
	if q.maxBatch == 1 {
		return batch
	}

	timer := time.NewTimer(q.batchWindow)
	defer timer.Stop()

	for len(batch) < q.maxBatch {
		select {
		case next := <-q.ops:
			batch = append(batch, next)
		case <-timer.C:
			return batch
		default:
			select {
			case next := <-q.ops:
				batch = append(batch, next)
			case <-timer.C:
				return batch
			}
		}
	}
	return batch
}

func (q *Queue) executeBatch(ctx context.Context, batch []op) {
	if len(batch) == 0 {
		return
	}
	q.stats.batches.Add(1)
	q.stats.opsInBatches.Add(int64(len(batch)))
	q.stats.lastBatchSize.Store(int64(len(batch)))

	tx, err := q.store.BeginWriteTx(ctx)
	if err != nil {
		for _, item := range batch {
			item.res <- err
		}
		return
	}
	defer tx.Rollback()

	results := make([]error, len(batch))
	for i, item := range batch {
		savepoint := fmt.Sprintf("cw_writeq_%d_%s", i, sanitizeSavepoint(item.name))
		if _, err := tx.Exec("SAVEPOINT " + savepoint); err != nil {
			results[i] = err
			continue
		}
		mark := tx.MarkAfterCommit()
		if err := item.fn(tx); err != nil {
			tx.RewindAfterCommit(mark)
			_, _ = tx.Exec("ROLLBACK TO SAVEPOINT " + savepoint)
			_, _ = tx.Exec("RELEASE SAVEPOINT " + savepoint)
			results[i] = err
			continue
		}
		if _, err := tx.Exec("RELEASE SAVEPOINT " + savepoint); err != nil {
			tx.RewindAfterCommit(mark)
			results[i] = err
			continue
		}
	}
	if err := tx.Commit(); err != nil {
		for i := range results {
			if results[i] == nil {
				results[i] = err
			}
		}
	}
	for i, item := range batch {
		item.res <- results[i]
	}
}

func (q *Queue) drain(ctx context.Context) {
	for {
		select {
		case first := <-q.ops:
			batch := q.collect(first)
			q.executeBatch(ctx, batch)
		default:
			return
		}
	}
}

func sanitizeSavepoint(name string) string {
	if name == "" {
		return "op"
	}
	replacer := strings.NewReplacer(
		" ", "_",
		"-", "_",
		".", "_",
		"/", "_",
		"\\", "_",
		":", "_",
	)
	clean := replacer.Replace(strings.ToLower(name))
	clean = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_':
			return r
		default:
			return '_'
		}
	}, clean)
	clean = strings.Trim(clean, "_")
	if clean == "" {
		return "op"
	}
	return clean
}

func (c *counters) snapshot() Stats {
	return Stats{
		Submitted:     c.submitted.Load(),
		Completed:     c.completed.Load(),
		Failed:        c.failed.Load(),
		Batches:       c.batches.Load(),
		OpsInBatches:  c.opsInBatches.Load(),
		LastBatchSize: c.lastBatchSize.Load(),
	}
}

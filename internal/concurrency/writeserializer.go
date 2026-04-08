package concurrency

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// writeSerializer implements WriteSerializer using a single goroutine
// that drains a channel of WriteOp values. This guarantees all writes
// to the main database are serialized — no concurrent SQLite writers.
type writeSerializer struct {
	db      *sql.DB
	ops     chan WriteOp
	stopped chan struct{}
	once    sync.Once
}

// NewWriteSerializer creates a write serializer bound to the given database.
// channelSize controls the buffered channel capacity — 0 means unbuffered
// (each Submit blocks until the writer goroutine picks it up).
func NewWriteSerializer(db *sql.DB, channelSize int) WriteSerializer {
	if channelSize < 0 {
		channelSize = 0
	}
	return &writeSerializer{
		db:      db,
		ops:     make(chan WriteOp, channelSize),
		stopped: make(chan struct{}),
	}
}

// Start runs the write serializer loop. It processes WriteOp values
// from the channel one at a time, executing each Fn against the database.
// Blocks until ctx is cancelled or Stop is called. After the context is
// cancelled, it drains any remaining ops in the channel before returning.
func (ws *writeSerializer) Start(ctx context.Context) error {
	defer ws.once.Do(func() { close(ws.stopped) })

	for {
		select {
		case op, ok := <-ws.ops:
			if !ok {
				return nil
			}
			ws.executeOp(op)

		case <-ctx.Done():
			// Drain remaining ops before returning.
			ws.drainRemaining()
			return ctx.Err()

		case <-ws.stopped:
			ws.drainRemaining()
			return nil
		}
	}
}

// Submit sends a write operation to the serializer and blocks until the
// operation completes (or the context expires). Returns the error from
// the write function, or a context error if the submit itself times out.
func (ws *writeSerializer) Submit(ctx context.Context, fn func(db *sql.DB) error) error {
	op := WriteOp{
		Fn:     fn,
		Result: make(chan error, 1),
	}

	// Try to send the op to the channel.
	select {
	case ws.ops <- op:
	case <-ws.stopped:
		return fmt.Errorf("write serializer stopped")
	case <-ctx.Done():
		return ctx.Err()
	}

	// Wait for the result.
	select {
	case err := <-op.Result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop signals the serializer to finish processing and shut down.
// Safe to call multiple times.
func (ws *writeSerializer) Stop() {
	ws.once.Do(func() { close(ws.stopped) })
}

func (ws *writeSerializer) executeOp(op WriteOp) {
	err := op.Fn(ws.db)
	op.Result <- err
}

func (ws *writeSerializer) drainRemaining() {
	for {
		select {
		case op, ok := <-ws.ops:
			if !ok {
				return
			}
			ws.executeOp(op)
		default:
			return
		}
	}
}

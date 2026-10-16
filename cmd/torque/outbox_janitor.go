package main

import (
	"context"
	"log"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// outboxRetention is how long state events stay in event_outbox: the R-state
// class of the stream contract (180 days). Rows leave only from the head of
// the log, and a reader behind the new head is sent a gap frame.
const outboxRetention = 180 * 24 * time.Hour

// runOutboxJanitor trims expired events hourly until ctx ends.
func runOutboxJanitor(ctx context.Context, store *sqlstore.Store) {
	trim := func() {
		n, err := store.OutboxTrimBefore(ctx, time.Now().Add(-outboxRetention))
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[outbox] trim: %v", err)
			}
			return
		}
		if n > 0 {
			log.Printf("[outbox] trimmed %d expired events", n)
		}
	}
	trim()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			trim()
		}
	}
}

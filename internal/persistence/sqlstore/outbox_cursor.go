package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// OutboxCursorLoad returns the last sequence number a named consumer settled,
// or 0 when it has none. The table is event_outbox_cursors (migration 042).
func (s *Store) OutboxCursorLoad(ctx context.Context, consumer string) (int64, error) {
	var last int64
	err := s.ReadDB().QueryRowContext(ctx,
		`SELECT last_seq FROM event_outbox_cursors WHERE consumer = ?`, consumer).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("outbox cursor %q: %w", consumer, err)
	}
	return last, nil
}

// OutboxCursorSave records that a named consumer has settled everything up to
// and including seq. It never moves a cursor backwards.
func (s *Store) OutboxCursorSave(ctx context.Context, consumer string, seq int64) error {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = w.Rollback() }()
	if _, err := w.tx.ExecContext(ctx, `
		INSERT INTO event_outbox_cursors (consumer, last_seq, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(consumer) DO UPDATE SET last_seq = excluded.last_seq, updated_at = excluded.updated_at
		WHERE excluded.last_seq > event_outbox_cursors.last_seq`,
		consumer, seq, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("save outbox cursor %q: %w", consumer, err)
	}
	return w.Commit()
}

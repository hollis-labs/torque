package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
)

// Read side of event_outbox (migration 042, CW-20261016-0133). The writer is
// state_events.go. Readers address rows by seq, Torque's local commit-order
// cursor, and tell a restore or reset apart by generation. Delivery is
// at-least-once; consumers dedupe on the event id.

// OutboxMeta is the single event_outbox_meta row.
type OutboxMeta struct {
	// Generation is the producer restart counter. It changes by hand on a
	// restore or reset, so a reader resuming across one is given a gap.
	Generation int64
	// PrunedThrough is the highest seq that has been trimmed from the head.
	PrunedThrough int64
}

// OutboxEvent is one stored event with its log-assigned fields filled in.
type OutboxEvent struct {
	Seq        int64
	Generation int64
	Event      runtimeevents.Event
}

// OutboxSupported reports whether the outbox may be read by seq on this
// store. On Postgres concurrent transactions can commit out of seq order, so a
// reader could skip a row; the reader stays off until seq is allocated from a
// commit-ordered counter.
func (s *Store) OutboxSupported() bool {
	_, ok := s.dialect.(sqliteDialect)
	return ok
}

// OutboxMeta returns the generation and trim point.
func (s *Store) OutboxMeta(ctx context.Context) (OutboxMeta, error) {
	var m OutboxMeta
	err := s.ReadDB().QueryRowContext(ctx,
		`SELECT generation, pruned_through FROM event_outbox_meta WHERE id = 1`).Scan(&m.Generation, &m.PrunedThrough)
	if err != nil {
		return OutboxMeta{}, fmt.Errorf("outbox meta: %w", err)
	}
	return m, nil
}

// OutboxHead returns the highest seq ever appended (0 when empty). It is read
// from sqlite_sequence-independent data: max(seq) over the live rows, or the
// trim point when everything has been trimmed.
func (s *Store) OutboxHead(ctx context.Context) (int64, error) {
	var head sql.NullInt64
	if err := s.ReadDB().QueryRowContext(ctx, `SELECT MAX(seq) FROM event_outbox`).Scan(&head); err != nil {
		return 0, fmt.Errorf("outbox head: %w", err)
	}
	if head.Valid {
		return head.Int64, nil
	}
	m, err := s.OutboxMeta(ctx)
	if err != nil {
		return 0, err
	}
	return m.PrunedThrough, nil
}

// OutboxAfter returns up to limit events with seq > after, oldest first, with
// Sequence (= seq) and Generation filled in.
func (s *Store) OutboxAfter(ctx context.Context, after int64, limit int) ([]OutboxEvent, error) {
	if limit <= 0 {
		limit = 500
	}
	meta, err := s.OutboxMeta(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.ReadDB().QueryContext(ctx,
		`SELECT seq, occurred_at, envelope FROM event_outbox WHERE seq > ? ORDER BY seq LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox after %d: %w", after, err)
	}
	defer rows.Close()

	var out []OutboxEvent
	for rows.Next() {
		var (
			seq      int64
			occurred string
			envelope string
		)
		if err := rows.Scan(&seq, &occurred, &envelope); err != nil {
			return nil, err
		}
		var ev runtimeevents.Event
		if err := json.Unmarshal([]byte(envelope), &ev); err != nil {
			return nil, fmt.Errorf("outbox seq %d: bad envelope: %w", seq, err)
		}
		ev.Sequence = uint64(seq)
		ev.Generation = uint64(meta.Generation)
		if t, err := time.Parse(time.RFC3339Nano, occurred); err == nil {
			ev.RecordedAt = &t
		}
		out = append(out, OutboxEvent{Seq: seq, Generation: meta.Generation, Event: ev})
	}
	return out, rows.Err()
}

// OutboxTrimBefore removes events older than cutoff from the head of the log
// and advances pruned_through, so a reader behind the new head is told about
// the gap. Rows are only ever removed from the head: occurred_at follows seq,
// so everything older than cutoff is a contiguous prefix. Returns the number
// of rows removed.
func (s *Store) OutboxTrimBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return 0, err
	}
	defer w.Rollback()

	var maxSeq sql.NullInt64
	if err := w.tx.QueryRow(`SELECT MAX(seq) FROM event_outbox WHERE occurred_at < ?`,
		cutoff.UTC().Format(time.RFC3339Nano)).Scan(&maxSeq); err != nil {
		return 0, err
	}
	if !maxSeq.Valid {
		return 0, nil
	}
	res, err := w.tx.Exec(`DELETE FROM event_outbox WHERE seq <= ?`, maxSeq.Int64)
	if err != nil {
		return 0, err
	}
	if _, err := w.tx.Exec(`UPDATE event_outbox_meta SET pruned_through = MAX(pruned_through, ?) WHERE id = 1`, maxSeq.Int64); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, w.Commit()
}

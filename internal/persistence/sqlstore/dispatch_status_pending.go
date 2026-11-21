package sqlstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
)

// StageDispatchStatuses atomically records a page for independent delivery and
// advances only the scan cursor. The delivery cursor continues to pin retention.
func (s *Store) StageDispatchStatuses(ctx context.Context, scanConsumer string, events []OutboxEvent) error {
	if len(events) == 0 {
		return nil
	}
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()
	for _, ev := range events {
		if _, err = w.tx.Exec(`INSERT OR IGNORE INTO dispatch_status_pending(seq) VALUES (?)`, ev.Seq); err != nil {
			return err
		}
	}
	if _, err = w.tx.Exec(`INSERT INTO event_outbox_cursors(consumer,last_seq,updated_at) VALUES (?,?,?) ON CONFLICT(consumer) DO UPDATE SET last_seq=MAX(last_seq,excluded.last_seq),updated_at=excluded.updated_at`, scanConsumer, events[len(events)-1].Seq, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return w.Commit()
}

// PendingDispatchStatuses rotates bounded retry pages by attempt count, so a
// full page of unresolved events cannot monopolize every tick. Claiming a page
// persists its turn before network I/O, including across process restarts.
func (s *Store) PendingDispatchStatuses(ctx context.Context, limit int) ([]OutboxEvent, error) {
	meta, err := s.OutboxMeta(ctx)
	if err != nil {
		return nil, err
	}
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Rollback()
	rows, err := w.tx.Query(`SELECT e.seq,e.envelope FROM dispatch_status_pending p JOIN event_outbox e ON e.seq=p.seq ORDER BY p.attempts,p.seq LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var events []OutboxEvent
	for rows.Next() {
		var seq int64
		var raw string
		if err = rows.Scan(&seq, &raw); err != nil {
			break
		}
		var ev runtimeevents.Event
		if err = json.Unmarshal([]byte(raw), &ev); err != nil {
			break
		}
		ev.Sequence, ev.Generation = uint64(seq), uint64(meta.Generation)
		events = append(events, OutboxEvent{Seq: seq, Generation: meta.Generation, Event: ev})
	}
	rowErr := rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	for _, ev := range events {
		if _, err = w.tx.Exec(`UPDATE dispatch_status_pending SET attempts=attempts+1 WHERE seq=?`, ev.Seq); err != nil {
			return nil, err
		}
	}
	return events, w.Commit()
}

// AcknowledgeDispatchStatus removes only this settled event. The low watermark
// advances up to the oldest pending event, never past an unresolved delivery.
func (s *Store) AcknowledgeDispatchStatus(ctx context.Context, consumer, scanConsumer string, seq int64) error {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()
	if _, err = w.tx.Exec(`DELETE FROM dispatch_status_pending WHERE seq=?`, seq); err != nil {
		return err
	}
	if _, err = w.tx.Exec(`UPDATE event_outbox_cursors SET last_seq=COALESCE((SELECT MIN(seq)-1 FROM dispatch_status_pending),(SELECT last_seq FROM event_outbox_cursors WHERE consumer=?)),updated_at=? WHERE consumer=?`, scanConsumer, time.Now().UTC().Format(time.RFC3339Nano), consumer); err != nil {
		return err
	}
	return w.Commit()
}

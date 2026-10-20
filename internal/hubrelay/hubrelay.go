// Package hubrelay ships the state-event outbox (event_outbox) to the event hub
// with the relay in mesh/eventbus. The outbox is already the durable,
// transactional record of every state change; this package only reads it in
// order, hands each event to the hub client and remembers how far the hub has
// settled, in the same database (event_outbox_cursors).
package hubrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/libs/mesh/eventbus"
	"github.com/hollis-labs/libs/mesh/eventbus/relay"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Store is the part of the sqlstore the relay uses.
type Store interface {
	OutboxMeta(ctx context.Context) (sqlstore.OutboxMeta, error)
	OutboxHead(ctx context.Context) (int64, error)
	OutboxAfter(ctx context.Context, after int64, limit int) ([]sqlstore.OutboxEvent, error)
	OutboxCursorLoad(ctx context.Context, consumer string) (int64, error)
	OutboxCursorSave(ctx context.Context, consumer string, seq int64) error
}

// Item turns a stored event into the hub's ingest body. The hub owns the actor,
// the record time and the cursor, so a producer never sends them. The outbox row
// is not changed.
func Item(ev sqlstore.OutboxEvent) (eventbus.Item, error) {
	e := ev.Event
	e.Actor = nil
	e.RecordedAt = nil
	e.Cursor = ""
	body, err := json.Marshal(e)
	if err != nil {
		return eventbus.Item{}, fmt.Errorf("hubrelay: outbox seq %d: %w", ev.Seq, err)
	}
	// Every outbox event is a state change: protected. Deliver bypasses the
	// client's queue, so this only labels it.
	return eventbus.Item{ID: e.ID, Priority: eventbus.PriorityHigh, Body: body}, nil
}

// Source reads the outbox for a relay. If the log was trimmed past the cursor
// (the relay was down for longer than the retention), it resumes from the trim
// point and reports the gap through OnGap instead of failing for ever: the rows
// are gone and cannot be sent.
type Source struct {
	Store Store
	// OnGap is called with the first and last sequence numbers that are gone.
	OnGap func(from, to int64)
}

var (
	_ relay.Source     = Source{}
	_ relay.HeadSource = Source{}
)

// After implements relay.Source.
func (s Source) After(ctx context.Context, after int64, limit int) ([]relay.Entry, error) {
	events, err := s.Store.OutboxAfter(ctx, after, limit)
	var pruned *sqlstore.OutboxPrunedError
	if errors.As(err, &pruned) {
		if s.OnGap != nil {
			s.OnGap(after+1, pruned.PrunedThrough)
		}
		events, err = s.Store.OutboxAfter(ctx, pruned.PrunedThrough, limit)
	}
	if err != nil {
		return nil, err
	}
	out := make([]relay.Entry, 0, len(events))
	for _, ev := range events {
		item, err := Item(ev)
		if err != nil {
			return nil, err
		}
		out = append(out, relay.Entry{Seq: ev.Seq, Item: item})
	}
	return out, nil
}

// Head implements relay.HeadSource.
func (s Source) Head(ctx context.Context) (int64, error) { return s.Store.OutboxHead(ctx) }

// Consumer names the cursor for an outbox generation. A generation changes when
// the log is restored or reset, and then the old sequence numbers mean nothing,
// so the relay starts that generation from its beginning.
func Consumer(generation int64) string { return fmt.Sprintf("hub-relay:g%d", generation) }

// Cursor keeps the relay's position in event_outbox_cursors.
type Cursor struct {
	Store    Store
	Consumer string
}

var _ relay.Cursor = Cursor{}

// Load implements relay.Cursor.
func (c Cursor) Load(ctx context.Context) (int64, error) {
	return c.Store.OutboxCursorLoad(ctx, c.Consumer)
}

// Save implements relay.Cursor.
func (c Cursor) Save(ctx context.Context, seq int64) error {
	return c.Store.OutboxCursorSave(ctx, c.Consumer, seq)
}

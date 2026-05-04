// Package messaging implements the github.com/hollis-labs/go-messaging
// Store contract over Clockwork's SQLite substrate. It is the durable
// layer the S1.3 broker sits on; typed envelope semantics (request/reply
// shapes, broker policy) live above this package.
//
// The contract is generic by design: payloads are opaque json.RawMessage,
// addresses are URN-form messaging.Address values, and the Store never
// interprets Channel or Metadata.
package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/go-messaging"
)

// Compile-time assertion: *Store satisfies messaging.Store.
var _ messaging.Store = (*Store)(nil)

// Store is a SQLite-backed messaging.Store. Subscribe is implemented as an
// in-process fan-out registry over the same DB — historical state is
// served from `messages`; live deliveries are pushed to subscribers when
// Send commits.
type Store struct {
	db *sql.DB

	mu          sync.Mutex
	subscribers []*subscription
}

type subscription struct {
	to     messaging.Address
	toURN  string
	ch     chan messaging.Envelope
	filter messaging.Filter
	ctx    context.Context
}

// NewStore constructs a Store backed by the supplied *sql.DB. The DB is
// assumed to have migration 022_messages.sql applied; no schema work is
// performed here.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Send persists an envelope. Store assigns ID (fresh UUIDv7) and CreatedAt;
// caller-set values for those fields are overwritten. DeliveredAt and
// ConsumedAt MUST be nil — otherwise ErrPresetLifecycle is returned.
func (s *Store) Send(ctx context.Context, env messaging.Envelope) (messaging.Envelope, error) {
	if env.DeliveredAt != nil || env.ConsumedAt != nil {
		return messaging.Envelope{}, messaging.ErrPresetLifecycle
	}
	id, err := uuid.NewV7()
	if err != nil {
		return messaging.Envelope{}, fmt.Errorf("uuid v7: %w", err)
	}
	env.ID = id.String()
	env.CreatedAt = time.Now().UTC()
	env.DeliveredAt = nil
	env.ConsumedAt = nil

	metaJSON, err := encodeMetadata(env.Metadata)
	if err != nil {
		return messaging.Envelope{}, fmt.Errorf("encode metadata: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
        INSERT INTO messages (
            id, kind, channel, thread_id, in_reply_to,
            from_kind, from_authority, from_id, from_subid, from_urn,
            to_kind,   to_authority,   to_id,   to_subid,   to_urn,
            payload, content_type, metadata_json, created_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		env.ID, string(env.Kind), string(env.Channel), env.ThreadID, env.InReplyTo,
		string(env.From.Kind), env.From.Authority, env.From.ID, env.From.SubID, env.From.URN(),
		string(env.To.Kind), env.To.Authority, env.To.ID, env.To.SubID, env.To.URN(),
		[]byte(env.Payload), env.ContentType, metaJSON, env.CreatedAt,
	)
	if err != nil {
		return messaging.Envelope{}, fmt.Errorf("insert message: %w", err)
	}

	s.fanOut(env)
	return env, nil
}

// Get retrieves a single envelope by ID. Returns ErrNotFound if absent.
func (s *Store) Get(ctx context.Context, id string) (messaging.Envelope, error) {
	row := s.db.QueryRowContext(ctx, baseSelect+` WHERE m.id = ?`, id)
	env, err := scanEnvelope(row)
	if errors.Is(err, sql.ErrNoRows) {
		return messaging.Envelope{}, messaging.ErrNotFound
	}
	if err != nil {
		return messaging.Envelope{}, err
	}
	return env, nil
}

// Inbox returns undelivered envelopes for `to`, chronologically. Atomically
// marks the returned envelopes as DeliveredAt=now for `to`.
//
// Implementation note: we serialize Inbox calls behind a write transaction
// pinned to a single connection (via sql.Conn) and explicit BEGIN IMMEDIATE.
// SQLite's default DEFERRED tx upgrades on the first write; under
// concurrent readers the upgrade fails with SQLITE_BUSY (517) and the
// busy_timeout PRAGMA does not retry mid-tx upgrades. IMMEDIATE acquires
// the RESERVED lock up front, so concurrent Inbox calls queue on
// busy_timeout rather than racing on the upgrade.
func (s *Store) Inbox(ctx context.Context, to messaging.Address, f messaging.Filter) ([]messaging.Envelope, error) {
	toURN := to.URN()
	now := time.Now().UTC()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	q := baseSelectFromTx + `
        WHERE m.to_urn = ?
          AND m.id NOT IN (SELECT message_id FROM message_deliveries WHERE recipient_urn = ?)
          AND m.canceled_at IS NULL`
	args := []any{toURN, toURN}
	q, args = applyFilter(q, args, f)
	q += ` ORDER BY m.created_at ASC, m.id ASC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}

	rows, err := conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query inbox: %w", err)
	}

	var envs []messaging.Envelope
	for rows.Next() {
		env, err := scanEnvelope(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan inbox row: %w", err)
		}
		envs = append(envs, env)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate inbox: %w", rowsErr)
	}

	for i := range envs {
		_, err := conn.ExecContext(ctx,
			`INSERT INTO message_deliveries (message_id, recipient_urn, delivered_at) VALUES (?, ?, ?)`,
			envs[i].ID, toURN, now)
		if err != nil {
			return nil, fmt.Errorf("mark delivered %s: %w", envs[i].ID, err)
		}
		t := now
		envs[i].DeliveredAt = &t
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, fmt.Errorf("commit inbox tx: %w", err)
	}
	committed = true
	return envs, nil
}

// Thread returns envelopes sharing a ThreadID, chronological. Read-only.
func (s *Store) Thread(ctx context.Context, threadID string, f messaging.Filter) ([]messaging.Envelope, error) {
	q := baseSelect + ` WHERE m.thread_id = ?`
	args := []any{threadID}
	q, args = applyFilter(q, args, f)
	q += ` ORDER BY m.created_at ASC, m.id ASC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query thread: %w", err)
	}
	defer rows.Close()

	var envs []messaging.Envelope
	for rows.Next() {
		env, err := scanEnvelope(rows)
		if err != nil {
			return nil, fmt.Errorf("scan thread row: %w", err)
		}
		envs = append(envs, env)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thread: %w", err)
	}
	return envs, nil
}

// Consume advances ConsumedAt for (envelope, recipient). Idempotent.
// If the envelope has not yet been delivered to `recipient`, Consume creates
// the delivery row with delivered_at = now. This keeps Consume usable as a
// stand-alone acknowledgement when callers don't first call Inbox.
func (s *Store) Consume(ctx context.Context, id string, recipient messaging.Address) error {
	rURN := recipient.URN()
	now := time.Now().UTC()

	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return messaging.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check message: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `
        UPDATE message_deliveries
           SET consumed_at = COALESCE(consumed_at, ?)
         WHERE message_id = ? AND recipient_urn = ?`, now, id, rURN)
	if err != nil {
		return fmt.Errorf("update consumed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		_, err = s.db.ExecContext(ctx, `
            INSERT INTO message_deliveries (message_id, recipient_urn, delivered_at, consumed_at)
            VALUES (?, ?, ?, ?)`, id, rURN, now, now)
		if err != nil {
			return fmt.Errorf("insert consumed delivery: %w", err)
		}
	}
	return nil
}

// Cancel marks an envelope as dead. Idempotent. Returns ErrNotFound only
// when the envelope ID has never existed.
func (s *Store) Cancel(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE messages SET canceled_at = COALESCE(canceled_at, ?) WHERE id = ?`,
		now, id)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return messaging.ErrNotFound
	}
	return nil
}

// Subscribe streams newly-created envelopes for `to` matching the filter.
// Returns a channel that closes when ctx is canceled. Live-only; no
// historical replay (callers use Inbox/Thread for that).
func (s *Store) Subscribe(ctx context.Context, to messaging.Address, f messaging.Filter) (<-chan messaging.Envelope, error) {
	sub := &subscription{
		to:     to,
		toURN:  to.URN(),
		ch:     make(chan messaging.Envelope, 16),
		filter: f,
		ctx:    ctx,
	}
	s.mu.Lock()
	s.subscribers = append(s.subscribers, sub)
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		for i, sv := range s.subscribers {
			if sv == sub {
				s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		close(sub.ch)
	}()

	return sub.ch, nil
}

func (s *Store) fanOut(env messaging.Envelope) {
	s.mu.Lock()
	subs := make([]*subscription, len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.Unlock()

	envURN := env.To.URN()
	for _, sub := range subs {
		if sub.toURN != "" && sub.toURN != envURN {
			continue
		}
		if !sub.filter.Matches(env) {
			continue
		}
		select {
		case sub.ch <- env:
		case <-sub.ctx.Done():
		default:
			// buffer full; drop. Inbox is the durable path.
		}
	}
}

// baseSelect carries the most-recent per-recipient delivered_at /
// consumed_at via a correlated sub-select. v0.2 of the lib treats
// envelopes as single-recipient, so the MAX semantics are equivalent
// to "this envelope's lifecycle"; multi-recipient impls in later
// versions will surface per-recipient state via dedicated calls.
const baseSelect = `
    SELECT m.id, m.kind, m.channel, m.thread_id, m.in_reply_to,
           m.from_kind, m.from_authority, m.from_id, m.from_subid,
           m.to_kind,   m.to_authority,   m.to_id,   m.to_subid,
           m.payload, m.content_type, m.metadata_json, m.created_at,
           m.canceled_at,
           (SELECT MAX(d.delivered_at) FROM message_deliveries d WHERE d.message_id = m.id),
           (SELECT MAX(d.consumed_at)  FROM message_deliveries d WHERE d.message_id = m.id)
      FROM messages m`

const baseSelectFromTx = `
    SELECT m.id, m.kind, m.channel, m.thread_id, m.in_reply_to,
           m.from_kind, m.from_authority, m.from_id, m.from_subid,
           m.to_kind,   m.to_authority,   m.to_id,   m.to_subid,
           m.payload, m.content_type, m.metadata_json, m.created_at,
           m.canceled_at,
           NULL, NULL
      FROM messages m`

func applyFilter(q string, args []any, f messaging.Filter) (string, []any) {
	if len(f.Kind) > 0 {
		q += ` AND m.kind IN (` + placeholders(len(f.Kind)) + `)`
		for _, k := range f.Kind {
			args = append(args, string(k))
		}
	}
	if len(f.Channel) > 0 {
		q += ` AND m.channel IN (` + placeholders(len(f.Channel)) + `)`
		for _, c := range f.Channel {
			args = append(args, string(c))
		}
	}
	if f.ThreadID != "" {
		q += ` AND m.thread_id = ?`
		args = append(args, f.ThreadID)
	}
	return q, args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, 2*n-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '?')
	}
	return string(out)
}

// scanRow is the minimal scanner contract we need. *sql.Row and *sql.Rows
// both satisfy it.
type scanRow interface {
	Scan(dest ...any) error
}

// scanEnvelope reads a row produced by baseSelect or baseSelectFromTx
// and reconstructs the Envelope. The two trailing nullable times carry
// the most-recent delivered_at / consumed_at across the envelope's
// recipients (NULL when either no delivery exists or the caller used
// the FromTx variant inside an Inbox transaction).
func scanEnvelope(row scanRow) (messaging.Envelope, error) {
	var (
		env           messaging.Envelope
		kind, channel string
		threadID      string
		inReplyTo     string
		fromKind      string
		fromAuth      string
		fromID        string
		fromSubID     string
		toKind        string
		toAuth        string
		toID          string
		toSubID       string
		payload       []byte
		contentType   string
		metadataJSON  string
		createdAt     time.Time
		canceledAt    sql.NullTime
		// MAX() over an aggregate returns driver.Value=string under
		// modernc.org/sqlite even when the underlying column is TIME, so
		// we scan as NullString and parse manually below.
		deliveredAtRaw sql.NullString
		consumedAtRaw  sql.NullString
	)
	if err := row.Scan(
		&env.ID, &kind, &channel, &threadID, &inReplyTo,
		&fromKind, &fromAuth, &fromID, &fromSubID,
		&toKind, &toAuth, &toID, &toSubID,
		&payload, &contentType, &metadataJSON, &createdAt,
		&canceledAt,
		&deliveredAtRaw, &consumedAtRaw,
	); err != nil {
		return messaging.Envelope{}, err
	}
	env.Kind = messaging.Kind(kind)
	env.Channel = messaging.Channel(channel)
	env.ThreadID = threadID
	env.InReplyTo = inReplyTo
	env.From = messaging.Address{
		Kind:      messaging.AddressKind(fromKind),
		Authority: fromAuth,
		ID:        fromID,
		SubID:     fromSubID,
	}
	env.To = messaging.Address{
		Kind:      messaging.AddressKind(toKind),
		Authority: toAuth,
		ID:        toID,
		SubID:     toSubID,
	}
	if len(payload) > 0 {
		env.Payload = json.RawMessage(payload)
	}
	env.ContentType = contentType
	if metadataJSON != "" && metadataJSON != "{}" {
		md, err := decodeMetadata(metadataJSON)
		if err != nil {
			return messaging.Envelope{}, fmt.Errorf("decode metadata: %w", err)
		}
		env.Metadata = md
	}
	env.CreatedAt = createdAt.UTC()
	if t, ok := parseSQLiteTime(deliveredAtRaw); ok {
		env.DeliveredAt = &t
	}
	if t, ok := parseSQLiteTime(consumedAtRaw); ok {
		env.ConsumedAt = &t
	}
	_ = canceledAt // tracked for Inbox filtering; not surfaced on Envelope
	return env, nil
}

// parseSQLiteTime parses the few timestamp formats modernc.org/sqlite may
// return when an aggregate (MAX/MIN) wraps a TIME column. Empty / invalid
// strings yield (zero, false).
func parseSQLiteTime(s sql.NullString) (time.Time, bool) {
	if !s.Valid || s.String == "" {
		return time.Time{}, false
	}
	layouts := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 -0700 -0700",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s.String); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func encodeMetadata(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeMetadata(s string) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

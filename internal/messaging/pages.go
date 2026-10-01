package messaging

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-sqlite/txutil"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

// PageQuery is a validated message read query. Inbox drain batches do not use
// cursors or offsets: every returned row leaves the undelivered cohort.
type PageQuery struct {
	Limit        int
	SortDir      string
	AfterTime    time.Time
	AfterID      string
	Offset       *int
	IncludeTotal bool
}
type MessagePage struct {
	Items            []gomsg.Envelope
	Total            *int
	HasMore          bool
	TotalUnavailable string
}
type PageStore interface {
	ThreadPage(context.Context, string, gomsg.Filter, PageQuery) (MessagePage, error)
	DrainInboxPage(context.Context, gomsg.Address, gomsg.Filter, PageQuery) (MessagePage, error)
}

func validatePage(q PageQuery) error {
	if q.Limit < 1 || q.Limit > pagination.MaxLimit {
		return fmt.Errorf("invalid message page size")
	}
	if q.SortDir != "asc" && q.SortDir != "desc" {
		return fmt.Errorf("invalid message order")
	}
	return nil
}
func inboxSQL(to gomsg.Address, f gomsg.Filter) (string, []any) {
	q := ` FROM messages m WHERE m.to_urn = ? AND m.id NOT IN (SELECT message_id FROM message_deliveries WHERE recipient_urn = ?) AND m.canceled_at IS NULL`
	args := []any{to.URN(), to.URN()}
	return applyFilter(q, args, f)
}

// DrainInboxPage preserves Inbox's delivery transaction, with an exact bound.
// COUNT happens before delivery; EXISTS afterwards. Neither probe mutates.
func (s *Store) DrainInboxPage(ctx context.Context, to gomsg.Address, f gomsg.Filter, q PageQuery) (MessagePage, error) {
	if err := validatePage(q); err != nil {
		return MessagePage{}, err
	}
	if q.AfterID != "" || q.Offset != nil || q.SortDir != "asc" {
		return MessagePage{}, fmt.Errorf("inbox drain supports chronological batches only")
	}
	page := MessagePage{Items: make([]gomsg.Envelope, 0)}
	where, args := inboxSQL(to, f)
	err := txutil.WithImmediate(ctx, s.db, func(tx *sql.Tx) error {
		if q.IncludeTotal {
			n := 0
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&n); err != nil {
				return err
			}
			page.Total = &n
		}
		selectQuery := baseSelectFromTx + strings.TrimPrefix(where, " FROM messages m") + " ORDER BY m.created_at ASC, m.id ASC LIMIT ?"
		boundArgs := append(append([]any{}, args...), q.Limit)
		rows, err := tx.QueryContext(ctx, selectQuery, boundArgs...)
		if err != nil {
			return err
		}
		for rows.Next() {
			env, e := scanEnvelope(rows)
			if e != nil {
				rows.Close()
				return e
			}
			page.Items = append(page.Items, env)
		}
		e := rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		for i := range page.Items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO message_deliveries (message_id,recipient_urn,delivered_at) VALUES (?,?,?)`, page.Items[i].ID, to.URN(), now); err != nil {
				return err
			}
			page.Items[i].DeliveredAt = &now
		}
		return tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1"+where+")", args...).Scan(&page.HasMore)
	})
	return page, err
}

func (s *Store) ThreadPage(ctx context.Context, thread string, f gomsg.Filter, q PageQuery) (MessagePage, error) {
	if err := validatePage(q); err != nil {
		return MessagePage{}, err
	}
	where, args := applyFilter(" FROM messages m WHERE m.thread_id = ?", []any{thread}, f)
	page := MessagePage{Items: make([]gomsg.Envelope, 0)}
	if q.IncludeTotal {
		n := 0
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&n); err != nil {
			return page, err
		}
		page.Total = &n
	}
	if q.AfterID != "" {
		op := ">"
		if q.SortDir == "desc" {
			op = "<"
		}
		where += " AND (m.created_at " + op + " ? OR (m.created_at = ? AND m.id > ?))"
		args = append(args, q.AfterTime, q.AfterTime, q.AfterID)
	}
	query := baseSelect + strings.TrimPrefix(where, " FROM messages m") + " ORDER BY m.created_at " + q.SortDir + ", m.id ASC LIMIT ?"
	args = append(args, q.Limit+1)
	if q.Offset != nil {
		query += " OFFSET ?"
		args = append(args, *q.Offset)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		env, e := scanEnvelope(rows)
		if e != nil {
			return page, e
		}
		page.Items = append(page.Items, env)
	}
	page.HasMore = len(page.Items) > q.Limit
	return page, rows.Err()
}

func (r *Router) DrainInboxPage(ctx context.Context, to gomsg.Address, f gomsg.Filter, q PageQuery) (MessagePage, error) {
	store, err := r.route(to.Authority)
	if err != nil {
		return MessagePage{}, err
	}
	paged, ok := store.(PageStore)
	if !ok {
		return MessagePage{}, fmt.Errorf("inbox store does not support bounded delivery pages")
	}
	return paged.DrainInboxPage(ctx, to, f, q)
}

func (r *Router) ThreadPage(ctx context.Context, thread string, f gomsg.Filter, q PageQuery) (MessagePage, error) {
	stores := []gomsg.Store{r.local}
	for _, store := range r.foreign {
		stores = append(stores, store)
	}
	if len(stores) > 1 && q.Offset != nil && *q.Offset > 0 {
		return MessagePage{}, &PageQueryError{Field: "offset", Message: "positive offset is unavailable on federated threads; use cursor"}
	}
	page := MessagePage{Items: make([]gomsg.Envelope, 0)}
	if len(stores) > 1 && q.IncludeTotal {
		page.TotalUnavailable = "federated"
	}
	seen := map[string]bool{}
	for _, store := range stores {
		paged, ok := store.(PageStore)
		if !ok {
			return page, fmt.Errorf("thread store does not support bounded cursor pages")
		}
		partQuery := q
		if len(stores) > 1 {
			partQuery.IncludeTotal = false
			partQuery.Offset = nil
		}
		part, err := paged.ThreadPage(ctx, thread, f, partQuery)
		if err != nil {
			return page, err
		}
		if len(part.Items) > q.Limit+1 {
			return page, fmt.Errorf("thread peer exceeded page bound")
		}
		for _, env := range part.Items {
			if !seen[env.ID] {
				seen[env.ID] = true
				page.Items = append(page.Items, env)
			}
		}
		page.HasMore = page.HasMore || part.HasMore
		if len(stores) == 1 {
			page.Total = part.Total
			page.TotalUnavailable = part.TotalUnavailable
		}
	}
	sort.Slice(page.Items, func(i, j int) bool {
		a, b := page.Items[i], page.Items[j]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		if q.SortDir == "desc" {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	if len(page.Items) > q.Limit+1 {
		page.Items = page.Items[:q.Limit+1]
	}
	page.HasMore = page.HasMore || len(page.Items) > q.Limit
	return page, nil
}

// ThreadParty authorizes a whole thread without enumerating its history.
func (s *Store) ThreadParty(ctx context.Context, thread string, authorities []string) (exists, party bool, err error) {
	if err = s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM messages WHERE thread_id = ?)", thread).Scan(&exists); err != nil {
		return
	}
	if len(authorities) == 0 {
		return
	}
	args := []any{thread}
	for _, a := range authorities {
		args = append(args, a)
	}
	for _, a := range authorities {
		args = append(args, a)
	}
	err = s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM messages WHERE thread_id = ? AND (from_authority IN ("+placeholders(len(authorities))+") OR to_authority IN ("+placeholders(len(authorities))+")))", args...).Scan(&party)
	return
}

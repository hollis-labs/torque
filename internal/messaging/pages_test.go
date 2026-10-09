package messaging_test

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/substrate/mesh/messaging"
	"github.com/hollis-labs/torque/internal/messaging"
	"github.com/stretchr/testify/require"
)

func TestDrainInboxPageExactDeliveryAndPreDrainTotal(t *testing.T) {
	db := newDB(t)
	store := messaging.NewStore(db)
	ctx := context.Background()
	to := addr("test", "reader")
	for i := 0; i < 206; i++ {
		kind := gomsg.MsgKindNotice
		if i == 205 {
			kind = gomsg.MsgKindResponse
		}
		_, err := store.Send(ctx, gomsg.Envelope{Kind: kind, From: addr("test", "sender"), To: to})
		require.NoError(t, err)
	}
	f := gomsg.Filter{Kind: []gomsg.Kind{gomsg.MsgKindNotice}}
	q := messaging.PageQuery{Limit: 50, SortDir: "asc", IncludeTotal: true}
	seen := map[string]bool{}
	for i, want := range []int{205, 155, 105, 55, 5} {
		page, err := store.DrainInboxPage(ctx, to, f, q)
		require.NoError(t, err)
		require.Equal(t, want, *page.Total)
		require.Len(t, page.Items, min(50, want))
		require.Equal(t, i < 4, page.HasMore)
		for _, env := range page.Items {
			require.False(t, seen[env.ID])
			seen[env.ID] = true
			require.NotNil(t, env.DeliveredAt)
		}
		var delivered int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM message_deliveries").Scan(&delivered))
		require.Equal(t, len(seen), delivered)
		wire := messaging.MessageEnvelope(page, q, true)
		require.Nil(t, wire.Meta.NextCursor)
	}
	require.Len(t, seen, 205)
	q.IncludeTotal = false
	page, err := store.DrainInboxPage(ctx, to, f, q)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.Nil(t, page.Total)
	require.False(t, page.HasMore)
	// The excluded kind remained undelivered; paging never delivered a probe row.
	all, err := store.Inbox(ctx, to, gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, all, 1)
}

type staticPagedStore struct {
	gomsg.Store
	items      []gomsg.Envelope
	seenBounds []int
}

func (s *staticPagedStore) DrainInboxPage(context.Context, gomsg.Address, gomsg.Filter, messaging.PageQuery) (messaging.MessagePage, error) {
	panic("drain must not be used by thread reads")
}
func (s *staticPagedStore) ThreadPage(_ context.Context, _ string, _ gomsg.Filter, q messaging.PageQuery) (messaging.MessagePage, error) {
	s.seenBounds = append(s.seenBounds, q.Limit+1)
	rows := append([]gomsg.Envelope{}, s.items...)
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		if q.SortDir == "desc" {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	var total *int
	if q.IncludeTotal {
		v := len(rows)
		total = &v
	}
	out := []gomsg.Envelope{}
	for _, v := range rows {
		if q.AfterID != "" {
			after := v.CreatedAt.After(q.AfterTime)
			if q.SortDir == "desc" {
				after = v.CreatedAt.Before(q.AfterTime)
			}
			if !after && !(v.CreatedAt.Equal(q.AfterTime) && v.ID > q.AfterID) {
				continue
			}
		}
		out = append(out, v)
	}
	if q.Offset != nil {
		if *q.Offset >= len(out) {
			out = out[:0]
		} else {
			out = out[*q.Offset:]
		}
	}
	more := len(out) > q.Limit
	if len(out) > q.Limit+1 {
		out = out[:q.Limit+1]
	}
	return messaging.MessagePage{Items: out, Total: total, HasMore: more}, nil
}

func TestFederatedThreadPageBoundedMergeDedupeAndCountAvailability(t *testing.T) {
	stamp := time.Date(2026, 10, 1, 0, 0, 0, 123456789, time.UTC)
	all := []gomsg.Envelope{}
	for i := 0; i < 11; i++ {
		all = append(all, gomsg.Envelope{ID: fmt.Sprintf("msg-%02d", i), CreatedAt: stamp.Add(time.Duration(i/3) * time.Nanosecond)})
	}
	local := &staticPagedStore{items: all[:8]}
	peer := &staticPagedStore{items: all[3:]}
	r, err := messaging.NewRouter(messaging.RouterConfig{Local: local, ForeignRoutes: map[string]gomsg.Store{"peer": peer}})
	require.NoError(t, err)
	for _, dir := range []string{"asc", "desc"} {
		q := messaging.PageQuery{Limit: 3, SortDir: dir, IncludeTotal: true}
		seen := map[string]bool{}
		for i := 0; i < 5; i++ {
			page, e := r.ThreadPage(context.Background(), "thread", gomsg.Filter{}, q)
			require.NoError(t, e)
			require.Nil(t, page.Total)
			require.Equal(t, "federated", page.TotalUnavailable)
			require.LessOrEqual(t, len(page.Items), 4)
			wire := messaging.MessageEnvelope(page, q, false)
			for _, v := range wire.Items {
				require.False(t, seen[v.ID], "duplicate at page boundary")
				seen[v.ID] = true
			}
			if !wire.Meta.HasMore {
				require.Nil(t, wire.Meta.NextCursor)
				break
			}
			require.NotNil(t, wire.Meta.NextCursor)
			next, e := messaging.ParsePageQuery(url.Values{"limit": {"3"}, "sort_dir": {dir}, "cursor": {*wire.Meta.NextCursor}, "include_total": {"true"}}, false)
			require.NoError(t, e)
			q = next
		}
		require.Len(t, seen, 11)
	}
	for _, bound := range append(local.seenBounds, peer.seenBounds...) {
		require.Equal(t, 4, bound)
	}
	offset := 1
	_, err = r.ThreadPage(context.Background(), "thread", gomsg.Filter{}, messaging.PageQuery{Limit: 3, SortDir: "asc", Offset: &offset})
	var query *messaging.PageQueryError
	require.ErrorAs(t, err, &query)
	require.Equal(t, "offset", query.Field)
	require.Contains(t, query.Message, "use cursor")
	single, err := messaging.NewRouter(messaging.RouterConfig{Local: local})
	require.NoError(t, err)
	page, err := single.ThreadPage(context.Background(), "thread", gomsg.Filter{}, messaging.PageQuery{Limit: 3, SortDir: "asc", IncludeTotal: true, Offset: &offset})
	require.NoError(t, err)
	require.Equal(t, 8, *page.Total)
	require.Empty(t, page.TotalUnavailable)
}

func TestSQLThreadPageFractionalCursorAndFilteredTotal(t *testing.T) {
	db := newDB(t)
	store := messaging.NewStore(db)
	ctx := context.Background()
	ids := []string{}
	for i := 0; i < 7; i++ {
		kind := gomsg.MsgKindNotice
		if i == 6 {
			kind = gomsg.MsgKindResponse
		}
		env, e := store.Send(ctx, gomsg.Envelope{Kind: kind, From: addr("test", "sender"), To: addr("test", "reader"), ThreadID: "thread"})
		require.NoError(t, e)
		ids = append(ids, env.ID)
		_, e = db.Exec("UPDATE messages SET created_at = ? WHERE id = ?", time.Date(2026, 10, 1, 0, 0, 0, i/2, time.UTC), env.ID)
		require.NoError(t, e)
	}
	for _, dir := range []string{"asc", "desc"} {
		q := messaging.PageQuery{Limit: 2, SortDir: dir, IncludeTotal: true}
		seen := map[string]bool{}
		for i := 0; i < 4; i++ {
			page, e := store.ThreadPage(ctx, "thread", gomsg.Filter{Kind: []gomsg.Kind{gomsg.MsgKindNotice}}, q)
			require.NoError(t, e)
			require.Equal(t, 6, *page.Total)
			wire := messaging.MessageEnvelope(page, q, false)
			for _, v := range wire.Items {
				require.False(t, seen[v.ID])
				seen[v.ID] = true
			}
			if !wire.Meta.HasMore {
				break
			}
			q, e = messaging.ParsePageQuery(url.Values{"limit": {"2"}, "sort_dir": {dir}, "cursor": {*wire.Meta.NextCursor}, "include_total": {"true"}}, false)
			require.NoError(t, e)
		}
		require.Len(t, seen, 6)
	}
	var delivered int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM message_deliveries").Scan(&delivered))
	require.Zero(t, delivered)
}

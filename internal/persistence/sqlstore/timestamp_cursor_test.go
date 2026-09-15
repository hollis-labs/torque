package sqlstore_test

import (
	"fmt"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

// Literal fixtures retain legacy text that current write paths no longer emit.
func seedTimestampTasks(t *testing.T, store *sqlstore.Store) {
	t.Helper()
	for _, f := range []struct{ id, ts string }{
		{"a", "2026-08-19 21:31:49.277010000 +0000 UTC"},
		{"b", "2026-08-19 21:31:49.100000002 +0000 UTC"},
		{"c", "2026-08-19 21:31:49.100000001 +0000 UTC"},
		{"d", "2026-08-19 21:31:49.27701"},
		{"e", "2026-08-19 21:31:49 +0000 UTC"},
		{"f", "2026-08-19 21:31:49"},
		{"g", "2026-08-19 21:31:49.000000000 +0000 UTC"},
		{"h", "2026-08-19 21:31:50 +0000 UTC"},
		{"i", "2026-08-19 21:31:00"},
	} {
		require.NoError(t, store.CreateTask(sampleTask(f.id)))
		_, err := store.DB().Exec(`UPDATE tasks SET created_at=?, updated_at=? WHERE id=?`, f.ts, f.ts, f.id)
		require.NoError(t, err)
	}
}

func walkTimestampTasks(t *testing.T, store *sqlstore.Store, f sqlstore.TaskFilter) []string {
	t.Helper()
	var ids []string
	for page := 0; ; page++ {
		require.Less(t, page, 12, "pagination must advance")
		rows, err := store.ListTasks(f)
		require.NoError(t, err)
		if len(rows) == 0 {
			return ids
		}
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		last := rows[len(rows)-1]
		instant := last.UpdatedAt
		if f.SortBy == "created_at" {
			instant = last.CreatedAt
		}
		f.AfterSortValue = instant.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
		f.AfterID = last.ID
	}
}

func TestTimestampCursor_PrecisionAndEquivalentShapes(t *testing.T) {
	store := setupTestStore(t)
	seedTimestampTasks(t, store)
	for _, col := range []string{"created_at", "updated_at"} {
		for _, dir := range []string{"asc", "desc"} {
			for _, limit := range []int{1, 2, 4} {
				t.Run(fmt.Sprintf("%s/%s/%d", col, dir, limit), func(t *testing.T) {
					want := []string{"i", "e", "f", "g", "c", "b", "a", "d", "h"}
					if dir == "desc" {
						want = []string{"h", "a", "d", "b", "c", "e", "f", "g", "i"}
					}
					require.Equal(t, want, walkTimestampTasks(t, store, sqlstore.TaskFilter{SortBy: col, SortDir: dir, Limit: limit}))
				})
			}
		}
	}
}

func TestTimestampCursor_PreciseRangeMembership(t *testing.T) {
	store := setupTestStore(t)
	seedTimestampTasks(t, store)
	for _, col := range []string{"created_at", "updated_at"} {
		for _, tc := range []struct {
			name, after, before string
			want                []string
		}{
			{"whole_upper", "", "2026-08-19 21:31:49", []string{"i", "e", "f", "g"}},
			{"nano_lower", "2026-08-19 21:31:49.100000002", "", []string{"b", "a", "d", "h"}},
			{"equal_fraction", "2026-08-19 21:31:49.27701", "2026-08-19 21:31:49.277010000", []string{"a", "d"}},
			{"whole_equal", "2026-08-19 21:31:49", "2026-08-19 21:31:49", []string{"e", "f", "g"}},
		} {
			t.Run(col+"/"+tc.name, func(t *testing.T) {
				f := sqlstore.TaskFilter{SortBy: col, SortDir: "asc", Limit: 1, UpdatedAfter: tc.after, UpdatedBefore: tc.before}
				if col == "created_at" {
					f.CreatedAfter, f.CreatedBefore = tc.after, tc.before
					f.UpdatedAfter, f.UpdatedBefore = "", ""
				}
				require.Equal(t, tc.want, walkTimestampTasks(t, store, f))
			})
		}
	}
	// A pre-existing whole-second cursor still ties equivalent textual instants.
	rows, err := store.ListTasks(sqlstore.TaskFilter{SortBy: "updated_at", AfterID: "e", AfterSortValue: "2026-08-19 21:31:49", Limit: 1})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "f", rows[0].ID)
}

func TestTimestampCursor_EpicPrecision(t *testing.T) {
	store := setupTestStore(t)
	for _, f := range []struct{ id, ts string }{{"a", "2026-08-19 21:31:49.27701 +0000 UTC"}, {"b", "2026-08-19 21:31:49.100000001 +0000 UTC"}} {
		require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: f.id, Name: f.id}))
		_, err := store.DB().Exec(`UPDATE epics SET updated_at=? WHERE id=?`, f.ts, f.id)
		require.NoError(t, err)
	}
	f := sqlstore.EpicFilter{SortBy: "updated_at", Limit: 1}
	var ids []string
	for page := 0; ; page++ {
		require.Less(t, page, 4)
		rows, err := store.ListEpics(f)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		ids = append(ids, rows[0].ID)
		f.AfterID = rows[0].ID
		f.AfterSortValue = rows[0].UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	}
	require.Equal(t, []string{"b", "a"}, ids)
}

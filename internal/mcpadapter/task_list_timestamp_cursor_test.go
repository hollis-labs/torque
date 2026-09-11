package mcpadapter_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

// setupAdapterWithDB mirrors setupAdapter but hands back the raw handle too,
// so a test can plant timestamp text that no current write path produces.
// See TestFullStack_TaskList_LegacyTimestampShapes for why that is necessary.
func setupAdapterWithDB(t *testing.T) (*mcpadapter.Adapter, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return mcpadapter.New(service.New(store), nil), db
}

func TestFullStack_TaskList_PreciseTimestampCursors(t *testing.T) {
	a, db := setupAdapterWithDB(t)
	var ids []string
	for i, ts := range []string{
		"2026-08-19 21:31:49.277010000 +0000 UTC",
		"2026-08-19 21:31:49.100000002 +0000 UTC",
		"2026-08-19 21:31:49.100000001 +0000 UTC",
		"2026-08-19 21:31:49 +0000 UTC",
		"2026-08-19 21:31:49",
	} {
		text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": fmt.Sprintf("timestamp %d", i)})
		require.False(t, isErr, text)
		var rec map[string]interface{}
		parseData(t, text, &rec)
		id := rec["ID"].(string)
		ids = append(ids, id)
		_, err := db.Exec(`UPDATE tasks SET created_at=?, updated_at=? WHERE id=?`, ts, ts, id)
		require.NoError(t, err)
	}
	for _, col := range []string{"created_at", "updated_at"} {
		for _, tc := range []struct {
			name, dir, after, before string
			order                    []int
		}{
			{"ascending", "asc", "2026-08-19T21:31:49Z", "", []int{3, 4, 2, 1, 0}},
			{"descending", "desc", "", "", []int{0, 1, 2, 3, 4}},
			{"whole_upper", "asc", "", "2026-08-19T21:31:49Z", []int{3, 4}},
			{"offset_nano_lower", "asc", "2026-08-19T16:31:49.100000002-05:00", "", []int{1, 0}},
			{"fraction_window", "asc", "2026-08-19T21:31:49.27701Z", "2026-08-19T21:31:49.277010000Z", []int{0}},
		} {
			t.Run(col+"/"+tc.name, func(t *testing.T) {
				var seen []string
				cursor := ""
				seenCursors := map[string]bool{}
				for page := 0; ; page++ {
					require.Less(t, page, len(ids)+2, "cursor must advance")
					args := map[string]interface{}{"sort_by": col, "sort_dir": tc.dir, "limit": "1"}
					prefix := "updated"
					if col == "created_at" {
						prefix = "created"
					}
					if tc.after != "" {
						args[prefix+"_after"] = tc.after
					}
					if tc.before != "" {
						args[prefix+"_before"] = tc.before
					}
					if cursor != "" {
						args["cursor"] = cursor
					}
					text, isErr := callTool(t, a, "torque_task_list", args)
					require.False(t, isErr, text)
					var env taskListCursorEnvelope
					parseData(t, text, &env)
					for _, item := range env.Items {
						seen = append(seen, item["id"].(string))
					}
					if !env.Meta.HasMore {
						require.Nil(t, env.Meta.NextCursor)
						break
					}
					require.NotNil(t, env.Meta.NextCursor)
					cursor = *env.Meta.NextCursor
					require.False(t, seenCursors[cursor])
					seenCursors[cursor] = true
				}
				var want []string
				for _, i := range tc.order {
					want = append(want, ids[i])
				}
				require.Equal(t, want, seen)
			})
		}
	}
}

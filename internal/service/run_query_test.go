package service_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/require"
)

func TestRunQueryCursorAndTotals(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "run-page", Title: "runs", Status: "doing"}))
	for i := 0; i < 205; i++ {
		_, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "run-page", Executor: "mock", Status: "done", Cost: float64(i % 4)})
		require.NoError(t, err)
	}
	page, err := svc.Run.Query(service.RunQuery{Limit: 1000, IncludeTotal: true})
	require.NoError(t, err)
	require.Len(t, page.Runs, 200)
	require.Equal(t, 200, page.Meta.Limit)
	require.Equal(t, 205, *page.Meta.Total)
	require.True(t, page.Meta.HasMore)
	cursor := *page.Meta.NextCursor
	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "run-page", Executor: "mock"})
	require.NoError(t, err)
	next, err := svc.Run.Query(service.RunQuery{Cursor: cursor, Limit: 200, IncludeTotal: true})
	require.NoError(t, err)
	require.Len(t, next.Runs, 5)
	require.Equal(t, 206, *next.Meta.Total)
	require.False(t, next.Meta.HasMore)
	require.Nil(t, next.Meta.NextCursor)
	seen := map[int64]bool{}
	for _, r := range page.Runs {
		seen[r.ID] = true
	}
	for _, r := range next.Runs {
		require.False(t, seen[r.ID])
		require.NotEqual(t, id, r.ID)
	}
	first, err := svc.Run.Query(service.RunQuery{})
	require.NoError(t, err)
	require.Len(t, first.Runs, 50)
	require.Nil(t, first.Meta.Total)
	for _, q := range []service.RunQuery{{Cursor: cursor, SortBy: "cost"}, {Cursor: cursor, SortDir: "asc"}, {Cursor: cursor, Offset: 1}, {Cursor: "broken"}, {Limit: -1}, {Offset: -1}, {SortBy: "SQL"}, {SortDir: "sideways"}, {Since: "bad"}, {Since: "2026-02-01T00:00:00Z", Until: "2026-01-01T00:00:00Z"}, {Cursor: pagination.Encode("cost", "desc", "NaN", "1"), SortBy: "cost"}, {Cursor: pagination.Encode("duration", "desc", "bad", "1"), SortBy: "duration"}, {Cursor: pagination.Encode("started_at", "desc", "bad", "1")}, {Cursor: pagination.Encode("started_at", "desc", "2026-01-01 00:00:00", "-1")}} {
		_, err := svc.Run.Query(q)
		var validation *service.ValidationError
		require.ErrorAs(t, err, &validation, "%+v", q)
	}
}

func TestRunQuerySortsAndCohort(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "runs-project", Name: "P"}))
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "runs-sprint", Name: "S"}))
	require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: "runs-epic", Name: "E"}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "runs-scoped", Title: "scoped", ProjectID: sql.NullString{String: "runs-project", Valid: true}, SprintID: sql.NullString{String: "runs-sprint", Valid: true}, EpicID: sql.NullString{String: "runs-epic", Valid: true}}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "runs-other", Title: "other"}))
	for i := 0; i < 6; i++ {
		r := &sqlstore.RunRecord{TaskID: "runs-scoped", Executor: "mock", Status: []string{"running", "done", "failed"}[i%3], Cost: float64(i % 2)}
		_, err := store.CreateRun(r)
		require.NoError(t, err)
		if i != 0 {
			require.NoError(t, store.CompleteRun(r.ID, sqlstore.RunCompletion{Status: r.Status, Cost: r.Cost}))
		}
	}
	_, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "runs-other", Executor: "mock"})
	require.NoError(t, err)
	for _, sortBy := range service.RunQuerySortFields {
		for _, dir := range []string{"asc", "desc"} {
			t.Run(sortBy+"/"+dir, func(t *testing.T) {
				q := service.RunQuery{TaskID: "runs-scoped", SortBy: sortBy, SortDir: dir, Limit: 1}
				seen := map[int64]bool{}
				var values []string
				for n := 0; ; n++ {
					require.Less(t, n, 7)
					p, err := svc.Run.Query(q)
					require.NoError(t, err)
					require.Len(t, p.Runs, 1)
					require.False(t, seen[p.Runs[0].ID])
					seen[p.Runs[0].ID] = true
					values = append(values, p.Runs[0].QuerySortValue)
					if !p.Meta.HasMore {
						break
					}
					q.Cursor = *p.Meta.NextCursor
				}
				require.Len(t, seen, 6)
				if sortBy == "duration" {
					require.Contains(t, values, "-1")
					require.NotEqual(t, []string{"-1", "-1", "-1", "-1", "-1", "-1"}, values)
				}
			})
		}
	}
	page, err := svc.Run.Query(service.RunQuery{ProjectID: "runs-project", SprintID: "runs-sprint", EpicID: "runs-epic", Statuses: []string{"done", "failed"}, Since: time.Now().Add(-time.Hour).Format(time.RFC3339Nano), Until: fmt.Sprint(time.Now().Add(time.Hour).UnixMilli()), IncludeTotal: true, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 4, *page.Meta.Total)
	next, err := svc.Run.Query(service.RunQuery{TaskID: "runs-scoped", Limit: 2, Offset: 5, IncludeTotal: true})
	require.NoError(t, err)
	require.Len(t, next.Runs, 1)
	require.Equal(t, 6, *next.Meta.Total)
	empty, err := svc.Run.Query(service.RunQuery{ProjectID: "missing", IncludeTotal: true})
	require.NoError(t, err)
	require.Empty(t, empty.Runs)
	require.NotNil(t, empty.Runs)
	require.Equal(t, 0, *empty.Meta.Total)
}

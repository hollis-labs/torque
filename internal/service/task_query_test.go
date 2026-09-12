package service_test

import (
	"database/sql"
	"math"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func setupTaskQueryService(t *testing.T) *service.Service {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return service.New(store)
}

func TestTaskQueryValidationBoundaries(t *testing.T) {
	svc := setupTaskQueryService(t)
	inf := math.Inf(1)
	nan := math.NaN()

	for _, tc := range []struct {
		name  string
		query service.TaskQuery
		field string
	}{
		{name: "negative offset", query: service.TaskQuery{Offset: -1}, field: "offset"},
		{name: "empty priorities", query: service.TaskQuery{Priorities: []int{}}, field: "priority"},
		{name: "nan cost", query: service.TaskQuery{CostBudgetGte: &nan}, field: "cost_budget_gte"},
		{name: "inf cost", query: service.TaskQuery{CostBudgetLte: &inf}, field: "cost_budget_lte"},
		{name: "blank missing field", query: service.TaskQuery{MissingFields: []string{"project_id", " "}}, field: "missing"},
		{name: "unknown missing field", query: service.TaskQuery{MissingFields: []string{"metadata.foo"}}, field: "missing"},
		{name: "blank present field", query: service.TaskQuery{PresentFields: []string{""}}, field: "present"},
		{name: "unknown present field", query: service.TaskQuery{PresentFields: []string{"budget"}}, field: "present"},
		{name: "whitespace sort", query: service.TaskQuery{SortBy: " "}, field: "sort_by"},
		{name: "whitespace date", query: service.TaskQuery{CreatedAfter: " "}, field: "created_after"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Task.Query(tc.query)
			require.Error(t, err)
			var verr *service.ValidationError
			require.ErrorAs(t, err, &verr)
			require.Equal(t, tc.field, verr.Field)
		})
	}
}

func TestTaskQueryPreservesExactWhitespaceValues(t *testing.T) {
	svc := setupTaskQueryService(t)
	_, err := svc.Task.Create(service.TaskCreateInput{Title: "ordinary", Description: "x"})
	require.NoError(t, err)

	page, err := svc.Task.Query(service.TaskQuery{Search: " "})
	require.NoError(t, err)
	require.Empty(t, page.Tasks)

	page, err = svc.Task.Query(service.TaskQuery{Statuses: []string{" "}})
	require.NoError(t, err)
	require.Empty(t, page.Tasks)
}

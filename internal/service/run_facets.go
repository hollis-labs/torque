package service

import "github.com/hollis-labs/torque/internal/persistence/sqlstore"

type RunFacetQuery struct {
	RunQuery
	FacetOptions
}

func (s *RunService) Facets(q RunFacetQuery) (sqlstore.EntityFacetResult, error) {
	if q.Offset != 0 {
		return sqlstore.EntityFacetResult{}, &ValidationError{Field: "offset", Message: "offset is not supported by facets"}
	}
	dims, limit, err := normalizeEntityFacets(q.FacetOptions, CursorQuery{Limit: q.Limit, Cursor: q.Cursor, SortBy: q.SortBy, SortDir: q.SortDir}, []string{"status", "executor", "profile"})
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	f, _, _, _, err := NormalizeRunQuery(q.RunQuery)
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	return s.store.RunFacets(f, dims, limit)
}

package service

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

// FacetOptions bounds aggregate buckets independently of row pagination.
type FacetOptions struct {
	Dimensions  []string
	BucketLimit int
	// Nil selects the default top parents; non-nil selects only these cohort IDs.
	RollupIDs []string
}
type ProjectFacetQuery struct {
	ProjectQuery
	FacetOptions
}
type EpicFacetQuery struct {
	EpicQuery
	FacetOptions
}
type SprintFacetQuery struct {
	SprintQuery
	FacetOptions
}

func normalizeEntityFacets(o FacetOptions, c CursorQuery, allowed []string) ([]string, int, error) {
	for _, p := range []struct {
		field string
		set   bool
	}{{"limit", c.Limit != 0}, {"cursor", c.Cursor != ""}, {"sort_by", c.SortBy != ""}, {"sort_dir", c.SortDir != ""}} {
		if p.set {
			return nil, 0, &ValidationError{Field: p.field, Message: p.field + " is not supported by facets; facets count the whole matching cohort"}
		}
	}
	limit, err := normalizeTaskFacetLimit(o.BucketLimit)
	if err != nil {
		return nil, 0, err
	}
	if o.Dimensions == nil {
		return append([]string{}, allowed...), limit, nil
	}
	dims := []string{}
	for _, raw := range o.Dimensions {
		dim := strings.TrimSpace(raw)
		if dim == "" {
			continue
		}
		if !slices.Contains(allowed, dim) {
			return nil, 0, &ValidationError{Field: "dimensions", Message: "unsupported facet dimension: " + dim}
		}
		if !slices.Contains(dims, dim) {
			dims = append(dims, dim)
		}
	}
	if len(dims) == 0 {
		return nil, 0, &ValidationError{Field: "dimensions", Message: "dimensions must include at least one supported dimension"}
	}
	return dims, limit, nil
}
func normalizeRollupIDs(ids []string) ([]string, error) {
	if ids == nil {
		return nil, nil
	}
	out := []string{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) > pagination.MaxLimit {
		return nil, &ValidationError{Field: "rollup_ids", Message: fmt.Sprintf("rollup_ids must contain at most %d distinct IDs", pagination.MaxLimit)}
	}
	return out, nil
}

func (s *ProjectService) Facets(q ProjectFacetQuery) (sqlstore.EntityFacetResult, error) {
	if err := s.feature.Require("projects"); err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	dims, limit, err := normalizeEntityFacets(q.FacetOptions, q.CursorQuery, []string{"status"})
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	ids, err := normalizeRollupIDs(q.RollupIDs)
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	f, _, err := NormalizeProjectQuery(q.ProjectQuery)
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	return s.store.ProjectFacets(f, dims, limit, ids)
}
func (s *EpicService) Facets(q EpicFacetQuery) (sqlstore.EntityFacetResult, error) {
	if err := s.feature.Require("epics"); err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	dims, limit, err := normalizeEntityFacets(q.FacetOptions, q.CursorQuery, []string{"status", "project_id", "priority"})
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	ids, err := normalizeRollupIDs(q.RollupIDs)
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	f, _, err := NormalizeEpicQuery(q.EpicQuery)
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	return s.store.EpicFacets(sqlstore.EpicFilter{Status: f.Status, ProjectID: f.ProjectID, Search: f.Search, IncludeArchived: f.IncludeArchived}, dims, limit, ids)
}
func (s *SprintService) Facets(q SprintFacetQuery) (sqlstore.EntityFacetResult, error) {
	if err := s.feature.Require("sprints"); err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	dims, limit, err := normalizeEntityFacets(q.FacetOptions, q.CursorQuery, []string{"status", "project_id", "approval_mode"})
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	ids, err := normalizeRollupIDs(q.RollupIDs)
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	f, _, err := NormalizeSprintQuery(q.SprintQuery)
	if err != nil {
		return sqlstore.EntityFacetResult{}, err
	}
	return s.store.SprintFacets(f, dims, limit, ids)
}

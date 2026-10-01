package sqlstore

import (
	"database/sql"
	"fmt"
	"strings"
)

// EntityFacetBucket preserves SQL NULL separately from empty strings.
type EntityFacetBucket struct {
	Value any `json:"value"`
	Count int `json:"count"`
}
type EntityFacet struct {
	Dimension     string              `json:"dimension"`
	Buckets       []EntityFacetBucket `json:"buckets"`
	TotalDistinct int                 `json:"total_distinct"`
	Returned      int                 `json:"returned"`
	Truncated     bool                `json:"truncated"`
}
type ParentTaskRollup struct {
	ScopeID string         `json:"scope_id"`
	Total   int            `json:"total"`
	Counts  map[string]int `json:"counts"`
}
type ParentTaskRollups struct {
	Scopes        []ParentTaskRollup `json:"scopes"`
	TotalDistinct int                `json:"total_distinct"`
	Returned      int                `json:"returned"`
	Truncated     bool               `json:"truncated"`
}
type EntityFacetResult struct {
	MatchingCount int                `json:"matching_count"`
	BucketLimit   int                `json:"bucket_limit"`
	Dimensions    []string           `json:"dimensions"`
	Facets        []EntityFacet      `json:"facets"`
	TaskRollups   *ParentTaskRollups `json:"task_rollups,omitempty"`
	Totals        *RunFacetTotals    `json:"totals,omitempty"`
}
type RunFacetTotals struct {
	Cost             float64 `json:"cost"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
}

func cohortWhere(conditions []string) string {
	if len(conditions) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conditions, " AND ")
}

// columnFacets performs bounded GROUP BY queries; it never loads cohort rows.
// columns comes from a closed store-owned allow-list, never user SQL.
func (s *Store) columnFacets(from, where string, args []any, dims []string, columns map[string]string, limit int) (EntityFacetResult, error) {
	if limit < 1 || limit > 200 {
		return EntityFacetResult{}, fmt.Errorf("invalid bucket limit")
	}
	out := EntityFacetResult{BucketLimit: limit, Dimensions: dims, Facets: []EntityFacet{}}
	if err := s.ReadDB().QueryRow(s.runBind("SELECT COUNT(*)"+from+where), args...).Scan(&out.MatchingCount); err != nil {
		return out, err
	}
	for _, dim := range dims {
		col, ok := columns[dim]
		if !ok {
			return out, fmt.Errorf("unsupported facet dimension %q", dim)
		}
		f := EntityFacet{Dimension: dim, Buckets: []EntityFacetBucket{}}
		if err := s.ReadDB().QueryRow(s.runBind("SELECT COUNT(*) FROM (SELECT "+col+from+where+" GROUP BY "+col+") facet_values"), args...).Scan(&f.TotalDistinct); err != nil {
			return out, err
		}
		q := "SELECT " + col + ", COUNT(*) AS c" + from + where + " GROUP BY " + col + " ORDER BY c DESC, CASE WHEN " + col + " IS NULL THEN 1 ELSE 0 END ASC, " + col + " ASC LIMIT ?"
		rows, err := s.ReadDB().Query(s.runBind(q), append(append([]any{}, args...), limit)...)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var value any
			var count int
			if err := rows.Scan(&value, &count); err != nil {
				rows.Close()
				return out, err
			}
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			f.Buckets = append(f.Buckets, EntityFacetBucket{value, count})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		f.Returned = len(f.Buckets)
		f.Truncated = f.Returned < f.TotalDistinct
		out.Facets = append(out.Facets, f)
	}
	return out, nil
}

func (s *Store) ProjectFacets(f ProjectFilter, dims []string, limit int) (EntityFacetResult, error) {
	where, args := projectListPredicates(f)
	return s.parentFacets("projects", "project_id", where, args, dims, map[string]string{"status": "status"}, limit)
}
func (s *Store) EpicFacets(f EpicFilter, dims []string, limit int) (EntityFacetResult, error) {
	where, args := epicListPredicates(f)
	return s.parentFacets("epics", "epic_id", where, args, dims, map[string]string{"status": "status", "project_id": "project_id", "priority": "priority"}, limit)
}
func (s *Store) SprintFacets(f SprintFilter, dims []string, limit int) (EntityFacetResult, error) {
	where, args := sprintListPredicates(f)
	return s.parentFacets("sprints", "sprint_id", where, args, dims, map[string]string{"status": "status", "project_id": "project_id", "approval_mode": "approval_mode"}, limit)
}

func (s *Store) parentFacets(table, scope string, conditions []string, args []any, dims []string, columns map[string]string, limit int) (EntityFacetResult, error) {
	where := cohortWhere(conditions)
	out, err := s.columnFacets(" FROM "+table, where, args, dims, columns, limit)
	if err != nil {
		return out, err
	}
	// Bound parent groups, not individual status cells, so every returned parent
	// gets complete task counts. Include zero-task parents. Internal tasks are
	// hidden just as on the default task list and task rollup.
	base := "WITH cohort AS (SELECT id FROM " + table + where + "), parents AS (SELECT p.id, COUNT(t.id) AS total FROM cohort p LEFT JOIN tasks t ON t." + scope + " = p.id AND t.kind <> 'internal' GROUP BY p.id ORDER BY total DESC, p.id ASC LIMIT ?) "
	q := base + "SELECT p.id, p.total, t.status, COUNT(t.id) FROM parents p LEFT JOIN tasks t ON t." + scope + " = p.id AND t.kind <> 'internal' GROUP BY p.id, p.total, t.status ORDER BY p.total DESC, p.id ASC, t.status ASC"
	rows, err := s.ReadDB().Query(s.runBind(q), append(append([]any{}, args...), limit)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	roll := &ParentTaskRollups{Scopes: []ParentTaskRollup{}, TotalDistinct: out.MatchingCount}
	for rows.Next() {
		var id string
		var total, count int
		var status sql.NullString
		if err := rows.Scan(&id, &total, &status, &count); err != nil {
			return out, err
		}
		if n := len(roll.Scopes); n == 0 || roll.Scopes[n-1].ScopeID != id {
			roll.Scopes = append(roll.Scopes, ParentTaskRollup{ScopeID: id, Total: total, Counts: map[string]int{}})
		}
		if status.Valid {
			roll.Scopes[len(roll.Scopes)-1].Counts[status.String] = count
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	roll.Returned = len(roll.Scopes)
	roll.Truncated = roll.Returned < roll.TotalDistinct
	out.TaskRollups = roll
	return out, nil
}

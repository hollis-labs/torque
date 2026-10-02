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
	ScopeID  string              `json:"scope_id"`
	Total    int                 `json:"total"`
	Counts   map[string]int      `json:"counts"`
	Children *ProjectChildCounts `json:"children,omitempty"`
}
type ParentTaskTotals struct {
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}
type ProjectChildCounts struct {
	Sprints int `json:"sprints"`
	Epics   int `json:"epics"`
}
type ParentTaskRollups struct {
	Scopes        []ParentTaskRollup `json:"scopes"`
	TotalDistinct int                `json:"total_distinct"`
	Returned      int                `json:"returned"`
	Truncated     bool               `json:"truncated"`
}
type EntityFacetResult struct {
	MatchingCount int                 `json:"matching_count"`
	BucketLimit   int                 `json:"bucket_limit"`
	Dimensions    []string            `json:"dimensions"`
	Facets        []EntityFacet       `json:"facets"`
	TaskRollups   *ParentTaskRollups  `json:"task_rollups,omitempty"`
	TaskTotals    *ParentTaskTotals   `json:"task_totals,omitempty"`
	ChildTotals   *ProjectChildCounts `json:"child_totals,omitempty"`
	Totals        *RunFacetTotals     `json:"totals,omitempty"`
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

func (s *Store) ProjectFacets(f ProjectFilter, dims []string, limit int, ids []string) (EntityFacetResult, error) {
	where, args := projectListPredicates(f)
	return s.parentFacets("projects", "project_id", where, args, dims, map[string]string{"status": "status"}, limit, ids, f.IncludeArchived)
}
func (s *Store) EpicFacets(f EpicFilter, dims []string, limit int, ids []string) (EntityFacetResult, error) {
	where, args := epicListPredicates(f)
	return s.parentFacets("epics", "epic_id", where, args, dims, map[string]string{"status": "status", "project_id": "project_id", "priority": "priority"}, limit, ids, f.IncludeArchived)
}
func (s *Store) SprintFacets(f SprintFilter, dims []string, limit int, ids []string) (EntityFacetResult, error) {
	where, args := sprintListPredicates(f)
	return s.parentFacets("sprints", "sprint_id", where, args, dims, map[string]string{"status": "status", "project_id": "project_id", "approval_mode": "approval_mode"}, limit, ids, f.IncludeArchived)
}

func (s *Store) parentFacets(table, scope string, conditions []string, args []any, dims []string, columns map[string]string, limit int, ids []string, includeArchived bool) (EntityFacetResult, error) {
	where := cohortWhere(conditions)
	out, err := s.columnFacets(" FROM "+table, where, args, dims, columns, limit)
	if err != nil {
		return out, err
	}
	// Count all cohort tasks independently of the selected parent groups.
	cohort := "WITH cohort AS (SELECT id FROM " + table + where + ") "
	totals, err := s.parentTaskTotals(cohort, scope, args)
	if err != nil {
		return out, err
	}
	out.TaskTotals = totals
	// Bound parent groups, not status cells. An explicit ID set addresses the
	// page on screen and is independent of bucket_limit.
	selection := ""
	queryArgs := append([]any{}, args...)
	if ids != nil {
		if len(ids) == 0 {
			selection = " WHERE 1 = 0"
		} else {
			selection = " WHERE p.id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
			for _, id := range ids {
				queryArgs = append(queryArgs, id)
			}
		}
	}
	base := strings.TrimSuffix(cohort, " ") + ", parents AS (SELECT p.id, COUNT(t.id) AS total FROM cohort p LEFT JOIN tasks t ON t." + scope + " = p.id AND t.kind <> 'internal'" + selection + " GROUP BY p.id ORDER BY total DESC, p.id ASC"
	if ids == nil {
		base += " LIMIT ?"
		queryArgs = append(queryArgs, limit)
	}
	base += ") "
	q := base + "SELECT p.id, p.total, t.status, COUNT(t.id) FROM parents p LEFT JOIN tasks t ON t." + scope + " = p.id AND t.kind <> 'internal' GROUP BY p.id, p.total, t.status ORDER BY p.total DESC, p.id ASC, t.status ASC"
	rows, err := s.ReadDB().Query(s.runBind(q), queryArgs...)
	if err != nil {
		return out, err
	}
	roll := &ParentTaskRollups{Scopes: []ParentTaskRollup{}, TotalDistinct: out.MatchingCount}
	for rows.Next() {
		var id string
		var total, count int
		var status sql.NullString
		if err := rows.Scan(&id, &total, &status, &count); err != nil {
			rows.Close()
			return out, err
		}
		if n := len(roll.Scopes); n == 0 || roll.Scopes[n-1].ScopeID != id {
			roll.Scopes = append(roll.Scopes, ParentTaskRollup{ScopeID: id, Total: total, Counts: map[string]int{}})
		}
		if status.Valid {
			roll.Scopes[len(roll.Scopes)-1].Counts[status.String] = count
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	roll.Returned = len(roll.Scopes)
	if ids != nil {
		roll.TotalDistinct = roll.Returned
	}
	roll.Truncated = roll.Returned < roll.TotalDistinct
	out.TaskRollups = roll
	if table == "projects" {
		if err := s.projectChildFacets(&out, cohort, args, includeArchived); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Store) parentTaskTotals(cohort, scope string, args []any) (*ParentTaskTotals, error) {
	q := cohort + "SELECT t.status, COUNT(*) FROM tasks t JOIN cohort p ON t." + scope + " = p.id WHERE t.kind <> 'internal' GROUP BY t.status"
	rows, err := s.ReadDB().Query(s.runBind(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	totals := &ParentTaskTotals{Counts: map[string]int{}}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		totals.Counts[status] = count
		totals.Total += count
	}
	return totals, rows.Err()
}

// Aggregate all cohort children in SQL, but return only cohort totals and the
// selected parent groups. No query is issued per parent.
func (s *Store) projectChildFacets(out *EntityFacetResult, cohort string, args []any, includeArchived bool) error {
	archive := ""
	if !includeArchived {
		archive = " WHERE child.archived_at IS NULL"
	}
	base := strings.TrimSuffix(cohort, " ") + ", child_counts AS (SELECT child.project_id, 'sprints' AS kind, COUNT(*) AS total FROM sprints child JOIN cohort p ON child.project_id = p.id" + archive + " GROUP BY child.project_id UNION ALL SELECT child.project_id, 'epics', COUNT(*) FROM epics child JOIN cohort p ON child.project_id = p.id" + archive + " GROUP BY child.project_id) "
	q := base + "SELECT NULL, kind, SUM(total) FROM child_counts GROUP BY kind"
	queryArgs := append([]any{}, args...)
	selected := map[string]*ProjectChildCounts{}
	for i := range out.TaskRollups.Scopes {
		scope := &out.TaskRollups.Scopes[i]
		scope.Children = &ProjectChildCounts{}
		selected[scope.ScopeID] = scope.Children
		queryArgs = append(queryArgs, scope.ScopeID)
	}
	if len(selected) > 0 {
		q += " UNION ALL SELECT project_id, kind, total FROM child_counts WHERE project_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(selected)), ",") + ")"
	}
	rows, err := s.ReadDB().Query(s.runBind(q), queryArgs...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out.ChildTotals = &ProjectChildCounts{}
	for rows.Next() {
		var id sql.NullString
		var kind string
		var count int
		if err := rows.Scan(&id, &kind, &count); err != nil {
			return err
		}
		children := out.ChildTotals
		if id.Valid {
			children = selected[id.String]
		}
		if kind == "sprints" {
			children.Sprints = count
		} else {
			children.Epics = count
		}
	}
	return rows.Err()
}

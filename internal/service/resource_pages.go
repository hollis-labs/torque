package service

import (
	"fmt"
	"strconv"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

// ResourceQuery centralizes validation for list families without a historical
// advanced-list mode. Scope fields are supplied by each transport separately.
type ResourceQuery struct {
	CursorQuery
	Offset *int
}

type ResourcePage struct {
	Rows   []sqlstore.ResourceRow
	Total  *int
	Query  NormalizedCursorQuery
	Offset *int
}

func ResourceSortPolicy(resource string) (fields []string, by, dir string) {
	switch resource {
	case "plans", "plan_children":
		return TaskQuerySortFields, TaskQueryDefaultSort, TaskQueryDefaultDir
	case "collection_tasks", "collection_inbox":
		return []string{"position", "priority", "status", "updated_at", "created_at"}, "position", "asc"
	case "sessions":
		return []string{"created_at", "state"}, "created_at", "desc"
	case "session_checkpoints":
		return []string{"created_at"}, "created_at", "desc"
	case "artifacts":
		return []string{"created_at", "type"}, "created_at", "desc"
	case "collections":
		return []string{"name", "status", "created_at", "updated_at"}, "name", "asc"
	case "templates":
		return []string{"name", "kind", "created_at", "updated_at"}, "name", "asc"
	case "checkpoints":
		return []string{"created_at", "status"}, "created_at", "desc"
	case "pending_checkpoints":
		return []string{"created_at", "status"}, "created_at", "asc"
	case "models":
		return []string{"name", "provider_id", "id", "cost", "context", "output"}, "name", "asc"
	case "messages":
		return []string{"created_at"}, "created_at", "asc"
	}
	return nil, "", ""
}

func NormalizeResourceQuery(resource string, q ResourceQuery) (NormalizedCursorQuery, error) {
	fields, by, dir := ResourceSortPolicy(resource)
	if fields == nil {
		return NormalizedCursorQuery{}, &ValidationError{Field: "resource", Message: "unsupported list resource"}
	}
	if q.Offset != nil {
		if *q.Offset < 0 {
			return NormalizedCursorQuery{}, &ValidationError{Field: "offset", Message: "offset must be non-negative"}
		}
		if q.Cursor != "" {
			return NormalizedCursorQuery{}, &ValidationError{Field: "cursor", Message: "cursor and offset are mutually exclusive"}
		}
	}
	return normalizeCursorQuery(q.CursorQuery, pagination.DefaultLimit, pagination.MaxLimit, by, dir, fields)
}

func (s *Service) ListResourcePage(resource string, f sqlstore.ResourcePageFilter, q ResourceQuery) (ResourcePage, error) {
	n, err := NormalizeResourceQuery(resource, q)
	if err != nil {
		return ResourcePage{}, err
	}

	switch resource {
	case "collections", "collection_tasks", "collection_inbox":
		if err := s.Feature.Require("collections"); err != nil {
			return ResourcePage{}, err
		}
		if resource == "collections" {
			if !validCollectionStatusFilters[f.Status] {
				return ResourcePage{}, &ValidationError{Field: "status", Message: "must be active, archived or all"}
			}
			if f.Status == "" {
				f.Status = "active"
			}
		}
		if resource == "collection_tasks" {
			if f.CollectionID == "" {
				return ResourcePage{}, &ValidationError{Field: "id", Message: "collection id required"}
			}
		}
	case "artifacts":
		if f.TaskID == "" {
			return ResourcePage{}, &ValidationError{Field: "task_id", Message: "task_id required"}
		}
		if f.RunID != "" {
			v, e := strconv.ParseInt(f.RunID, 10, 64)
			if e != nil || v < 1 {
				return ResourcePage{}, &ValidationError{Field: "run_id", Message: "run_id must be a positive integer"}
			}
		}
	case "plan_children":
		if f.ParentID == "" {
			return ResourcePage{}, &ValidationError{Field: "plan_id", Message: "plan_id required"}
		}
	case "checkpoints":
		if f.TaskID == "" {
			return ResourcePage{}, &ValidationError{Field: "task_id", Message: "task_id required"}
		}
	case "session_checkpoints":
		if f.SessionID == "" {
			return ResourcePage{}, &ValidationError{Field: "session_id", Message: "session_id required"}
		}
	}
	f.Limit = n.Limit + 1
	f.SortBy = n.SortBy
	f.SortDir = n.SortDir
	f.AfterSortValue = n.AfterSortValue
	f.AfterID = n.AfterID
	if q.Offset != nil {
		f.Offset = *q.Offset
	}
	rows, total, err := pageWithTotal(q.IncludeTotal, func() ([]sqlstore.ResourceRow, error) { return s.store.ListResourcePage(resource, f) }, func() (int, error) { return s.store.CountResourcePage(resource, f) })
	if err != nil {
		return ResourcePage{}, fmt.Errorf("list %s: %w", resource, err)
	}
	return ResourcePage{rows, total, n, q.Offset}, nil
}

package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

const (
	DefaultTaskQueryLimit = 50
	MaxTaskQueryLimit     = 200
	TaskQueryDefaultSort  = "priority"
	TaskQueryDefaultDir   = "asc"
)

var TaskQuerySortFields = []string{"priority", "status", "updated_at", "created_at"}

// TaskQuery is the public task-list contract shared by HTTP and MCP.
// Transports may decode arrays differently, but validation/defaulting,
// internal visibility, date bounds, sort/cursor binding, paging, and optional
// total-count behavior live here.
type TaskQuery struct {
	Status         string
	Statuses       []string
	Priorities     []int
	SprintID       string
	ProjectID      string
	EpicID         string
	Executor       string
	Kind           string
	SourceType     string
	SourceRef      string
	Trust          string
	CheckpointMode string
	ParentID       string
	ParentIDSet    bool
	Manual         *bool
	TagSlugs       []string
	Search         string
	AgentProfile   string
	LaunchProfile  string

	CreatedAfter  string
	CreatedBefore string
	UpdatedAfter  string
	UpdatedBefore string

	CostBudgetGte    *float64
	CostBudgetLte    *float64
	TokenBudgetGte   *int64
	TokenBudgetLte   *int64
	MaxDurationMsGte *int64
	MaxDurationMsLte *int64
	MaxRetriesGte    *int
	MaxRetriesLte    *int

	IncludeInternal bool
	Limit           int
	Offset          int
	SortBy          string
	SortDir         string
	Cursor          string
	WithTotal       bool
}

type TaskQueryResult struct {
	Tasks            []sqlstore.TaskRecord
	Total            int
	TotalValid       bool
	Limit            int
	Offset           int
	SortBy           string
	SortDir          string
	HasMoreFromQuery bool
}

func (s *TaskService) Query(q TaskQuery) (TaskQueryResult, error) {
	filter, limit, sortBy, sortDir, err := normalizeTaskQuery(q)
	if err != nil {
		return TaskQueryResult{}, err
	}
	filter.Limit = limit + 1
	if q.WithTotal {
		result, err := s.store.ListTasksPage(filter)
		if err != nil {
			if errors.Is(err, sqlstore.ErrInvalidCursor) {
				return TaskQueryResult{}, &ValidationError{Field: "cursor", Message: err.Error()}
			}
			return TaskQueryResult{}, err
		}
		tasks, hasMore := trimTaskQueryPage(result.Tasks, limit)
		return TaskQueryResult{
			Tasks:            tasks,
			Total:            result.Total,
			TotalValid:       true,
			Limit:            limit,
			Offset:           q.Offset,
			SortBy:           sortBy,
			SortDir:          sortDir,
			HasMoreFromQuery: hasMore,
		}, nil
	}
	tasks, err := s.store.ListTasks(filter)
	if err != nil {
		if errors.Is(err, sqlstore.ErrInvalidCursor) {
			return TaskQueryResult{}, &ValidationError{Field: "cursor", Message: err.Error()}
		}
		return TaskQueryResult{}, err
	}
	tasks, hasMore := trimTaskQueryPage(tasks, limit)
	return TaskQueryResult{
		Tasks:            tasks,
		Limit:            limit,
		Offset:           q.Offset,
		SortBy:           sortBy,
		SortDir:          sortDir,
		HasMoreFromQuery: hasMore,
	}, nil
}

func normalizeTaskQuery(q TaskQuery) (sqlstore.TaskFilter, int, string, string, error) {
	if q.Cursor != "" && q.Offset > 0 {
		return sqlstore.TaskFilter{}, 0, "", "", &ValidationError{Field: "cursor", Message: "cursor cannot be combined with a positive offset"}
	}
	if q.Offset < 0 {
		return sqlstore.TaskFilter{}, 0, "", "", &ValidationError{Field: "offset", Message: "offset must be greater than or equal to 0"}
	}
	if q.Priorities != nil && len(q.Priorities) == 0 {
		return sqlstore.TaskFilter{}, 0, "", "", &ValidationError{Field: "priority", Message: "priority filter must contain at least one integer"}
	}
	for field, value := range map[string]*float64{
		"cost_budget_gte": q.CostBudgetGte,
		"cost_budget_lte": q.CostBudgetLte,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return sqlstore.TaskFilter{}, 0, "", "", &ValidationError{Field: field, Message: field + " must be a finite number"}
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultTaskQueryLimit
	}
	if limit > MaxTaskQueryLimit {
		limit = MaxTaskQueryLimit
	}

	sortBy := TaskQueryDefaultSort
	if q.SortBy != "" {
		v, err := pagination.ValidateSortBy(q.SortBy, TaskQuerySortFields...)
		if err != nil {
			return sqlstore.TaskFilter{}, 0, "", "", &ValidationError{Field: "sort_by", Message: err.Error()}
		}
		sortBy = v
	}
	sortDir := TaskQueryDefaultDir
	if q.SortDir != "" {
		v, err := pagination.ValidateSortDir(q.SortDir)
		if err != nil {
			return sqlstore.TaskFilter{}, 0, "", "", &ValidationError{Field: "sort_dir", Message: err.Error()}
		}
		sortDir = v
	}

	var afterSortValue, afterID string
	if q.Cursor != "" {
		c, err := pagination.Decode(q.Cursor)
		if err != nil {
			return sqlstore.TaskFilter{}, 0, "", "", &ValidationError{Field: "cursor", Message: fmt.Sprintf("invalid cursor: %v", err)}
		}
		if err := c.Validate(sortBy, sortDir); err != nil {
			return sqlstore.TaskFilter{}, 0, "", "", &ValidationError{Field: "cursor", Message: err.Error()}
		}
		afterSortValue, afterID = c.SortValue, c.ID
	}

	filter := sqlstore.TaskFilter{
		Status:           q.Status,
		Statuses:         q.Statuses,
		Priorities:       normalizeIntSet(q.Priorities),
		SprintID:         q.SprintID,
		ProjectID:        q.ProjectID,
		EpicID:           q.EpicID,
		Executor:         q.Executor,
		Kind:             q.Kind,
		SourceType:       q.SourceType,
		SourceRef:        q.SourceRef,
		Trust:            q.Trust,
		CheckpointMode:   q.CheckpointMode,
		Manual:           q.Manual,
		TagSlugs:         trimUniqueNonEmpty(q.TagSlugs),
		Search:           q.Search,
		AgentProfile:     q.AgentProfile,
		LaunchProfile:    q.LaunchProfile,
		CostBudgetGte:    q.CostBudgetGte,
		CostBudgetLte:    q.CostBudgetLte,
		TokenBudgetGte:   q.TokenBudgetGte,
		TokenBudgetLte:   q.TokenBudgetLte,
		MaxDurationMsGte: q.MaxDurationMsGte,
		MaxDurationMsLte: q.MaxDurationMsLte,
		MaxRetriesGte:    q.MaxRetriesGte,
		MaxRetriesLte:    q.MaxRetriesLte,
		ExcludeInternal:  !q.IncludeInternal,
		Offset:           q.Offset,
		SortBy:           sortBy,
		SortDir:          sortDir,
		AfterSortValue:   afterSortValue,
		AfterID:          afterID,
	}
	if q.ParentIDSet {
		if q.ParentID == "" || q.ParentID == "null" {
			filter.ParentIDNull = true
		} else {
			filter.ParentID = q.ParentID
		}
	}
	var err error
	if filter.CreatedAfter, err = parseTaskQueryTime(q.CreatedAfter, "created_after"); err != nil {
		return sqlstore.TaskFilter{}, 0, "", "", err
	}
	if filter.CreatedBefore, err = parseTaskQueryTime(q.CreatedBefore, "created_before"); err != nil {
		return sqlstore.TaskFilter{}, 0, "", "", err
	}
	if filter.UpdatedAfter, err = parseTaskQueryTime(q.UpdatedAfter, "updated_after"); err != nil {
		return sqlstore.TaskFilter{}, 0, "", "", err
	}
	if filter.UpdatedBefore, err = parseTaskQueryTime(q.UpdatedBefore, "updated_before"); err != nil {
		return sqlstore.TaskFilter{}, 0, "", "", err
	}
	return filter, limit, sortBy, sortDir, nil
}

func parseTaskQueryTime(raw, field string) (string, error) {
	if raw == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", &ValidationError{Field: field, Message: "invalid " + field + ": " + err.Error()}
	}
	return t.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional), nil
}

func trimTaskQueryPage(tasks []sqlstore.TaskRecord, limit int) ([]sqlstore.TaskRecord, bool) {
	if len(tasks) > limit {
		return tasks[:limit], true
	}
	if tasks == nil {
		return []sqlstore.TaskRecord{}, false
	}
	return tasks, false
}

func TaskQueryCursor(t sqlstore.TaskRecord, sortBy, sortDir string) string {
	return pagination.Encode(sortBy, sortDir, TaskQuerySortValue(t, sortBy), t.ID)
}

func TaskQuerySortValue(t sqlstore.TaskRecord, sortBy string) string {
	switch sortBy {
	case "priority":
		return fmt.Sprint(t.Priority)
	case "status":
		return t.Status
	case "updated_at":
		return t.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	case "created_at":
		return t.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	default:
		return ""
	}
}

func trimUniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		s := strings.TrimSpace(v)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func normalizeIntSet(values []int) []int {
	seen := make(map[int]bool, len(values))
	out := make([]int, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

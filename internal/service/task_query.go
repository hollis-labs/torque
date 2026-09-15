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
	DefaultTaskFacetLimit = 50
	MaxTaskFacetLimit     = 200
)

var TaskQuerySortFields = []string{"priority", "status", "updated_at", "created_at"}
var TaskFacetDimensions = []string{
	"status", "priority", "manual", "kind", "executor", "agent_profile", "launch_profile",
	"project_id", "sprint_id", "epic_id", "parent_id", "tags",
}
var TaskPresenceFields = []string{
	"project_id", "sprint_id", "epic_id", "parent_id", "source_ref", "collection_id",
	"cost_budget", "token_budget", "max_duration_ms", "tags",
}

var taskFacetDimensionSet = func() map[string]bool {
	m := make(map[string]bool, len(TaskFacetDimensions))
	for _, d := range TaskFacetDimensions {
		m[d] = true
	}
	return m
}()

var taskPresenceFieldSet = func() map[string]bool {
	m := make(map[string]bool, len(TaskPresenceFields))
	for _, f := range TaskPresenceFields {
		m[f] = true
	}
	return m
}()

// TaskQuery is the public task-list contract shared by HTTP and MCP.
// Transports may decode arrays differently, but validation/defaulting,
// internal visibility, date bounds, sort/cursor binding, paging, and optional
// total-count behavior live here.
type TaskQuery struct {
	Status         string
	Statuses       []string
	Priorities     []int
	PriorityGte    *int
	PriorityLte    *int
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
	TagSlugsAny    []string
	TagSlugsNone   []string
	MissingFields  []string
	PresentFields  []string
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

type TaskFacetQuery struct {
	TaskQuery
	Dimensions  []string
	BucketLimit int
}

type TaskFacetValue struct {
	Value  any
	IsNull bool
}

type TaskFacetBucket struct {
	Value any `json:"value"`
	Count int `json:"count"`
}

type TaskFacet struct {
	Dimension     string            `json:"dimension"`
	Buckets       []TaskFacetBucket `json:"buckets"`
	TotalDistinct int               `json:"total_distinct"`
	Returned      int               `json:"returned"`
	Truncated     bool              `json:"truncated"`
}

type TaskFacetQueryResult struct {
	MatchingCount int         `json:"matching_count"`
	BucketLimit   int         `json:"bucket_limit"`
	Dimensions    []string    `json:"dimensions"`
	Facets        []TaskFacet `json:"facets"`
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

func (s *TaskService) Facets(q TaskFacetQuery) (TaskFacetQueryResult, error) {
	dims, err := normalizeTaskFacetDimensions(q.Dimensions)
	if err != nil {
		return TaskFacetQueryResult{}, err
	}
	limit, err := normalizeTaskFacetLimit(q.BucketLimit)
	if err != nil {
		return TaskFacetQueryResult{}, err
	}
	if q.Limit != 0 {
		return TaskFacetQueryResult{}, &ValidationError{Field: "limit", Message: "limit is not supported by task facets; use bucket_limit for facet bucket bounds"}
	}
	if q.Offset != 0 {
		return TaskFacetQueryResult{}, &ValidationError{Field: "offset", Message: "offset is not supported by task facets; facets always count the whole matching cohort"}
	}
	if q.Cursor != "" {
		return TaskFacetQueryResult{}, &ValidationError{Field: "cursor", Message: "cursor is not supported by task facets; facets always count the whole matching cohort"}
	}
	if q.SortBy != "" {
		return TaskFacetQueryResult{}, &ValidationError{Field: "sort_by", Message: "sort_by is not supported by task facets; buckets sort by count desc then value asc"}
	}
	if q.SortDir != "" {
		return TaskFacetQueryResult{}, &ValidationError{Field: "sort_dir", Message: "sort_dir is not supported by task facets; buckets sort by count desc then value asc"}
	}
	filter, _, _, _, err := normalizeTaskQuery(q.TaskQuery)
	if err != nil {
		return TaskFacetQueryResult{}, err
	}
	filter.Limit = 0
	filter.Offset = 0
	filter.AfterSortValue = ""
	filter.AfterID = ""
	storeFacets, matching, err := s.store.TaskFacets(sqlstore.TaskFacetRequest{
		Filter:     filter,
		Dimensions: dims,
		Limit:      limit,
	})
	if err != nil {
		if errors.Is(err, sqlstore.ErrInvalidCursor) {
			return TaskFacetQueryResult{}, &ValidationError{Field: "cursor", Message: err.Error()}
		}
		return TaskFacetQueryResult{}, err
	}
	facets := make([]TaskFacet, 0, len(storeFacets))
	for _, sf := range storeFacets {
		buckets := make([]TaskFacetBucket, 0, len(sf.Buckets))
		for _, b := range sf.Buckets {
			buckets = append(buckets, TaskFacetBucket{
				Value: b.Value.Value,
				Count: b.Count,
			})
		}
		facets = append(facets, TaskFacet{
			Dimension:     sf.Dimension,
			Buckets:       buckets,
			TotalDistinct: sf.TotalDistinct,
			Returned:      len(buckets),
			Truncated:     sf.Truncated,
		})
	}
	return TaskFacetQueryResult{
		MatchingCount: matching,
		BucketLimit:   limit,
		Dimensions:    dims,
		Facets:        facets,
	}, nil
}

func normalizeTaskFacetDimensions(values []string) ([]string, error) {
	if values == nil {
		return append([]string{}, TaskFacetDimensions...), nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		d := strings.TrimSpace(raw)
		if d == "" {
			continue
		}
		if !taskFacetDimensionSet[d] {
			return nil, &ValidationError{Field: "dimensions", Message: fmt.Sprintf("unsupported task facet dimension %q; supported dimensions are %s", d, strings.Join(TaskFacetDimensions, ", "))}
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, &ValidationError{Field: "dimensions", Message: "dimensions must include at least one supported dimension"}
	}
	return out, nil
}

func normalizeTaskFacetLimit(n int) (int, error) {
	if n < 0 {
		return 0, &ValidationError{Field: "bucket_limit", Message: "bucket_limit must be greater than or equal to 0"}
	}
	if n == 0 {
		return DefaultTaskFacetLimit, nil
	}
	if n > MaxTaskFacetLimit {
		return MaxTaskFacetLimit, nil
	}
	return n, nil
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
	missingFields, err := normalizeTaskPresenceFields("missing", q.MissingFields)
	if err != nil {
		return sqlstore.TaskFilter{}, 0, "", "", err
	}
	presentFields, err := normalizeTaskPresenceFields("present", q.PresentFields)
	if err != nil {
		return sqlstore.TaskFilter{}, 0, "", "", err
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
		PriorityGte:      q.PriorityGte,
		PriorityLte:      q.PriorityLte,
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
		TagSlugsAny:      trimUniqueNonEmpty(q.TagSlugsAny),
		TagSlugsNone:     trimUniqueNonEmpty(q.TagSlugsNone),
		MissingFields:    missingFields,
		PresentFields:    presentFields,
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

func normalizeTaskPresenceFields(field string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			return nil, &ValidationError{Field: field, Message: field + " fields cannot be blank"}
		}
		if !taskPresenceFieldSet[v] {
			return nil, &ValidationError{Field: field, Message: fmt.Sprintf("unsupported task presence field %q; supported fields are %s", v, strings.Join(TaskPresenceFields, ", "))}
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
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

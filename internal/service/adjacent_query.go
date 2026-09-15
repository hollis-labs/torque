package service

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

const (
	DefaultGenericQueryLimit = 100
	MaxGenericQueryLimit     = 500

	DefaultCommentListLimit   = 50
	MaxCommentListLimit       = 200
	DefaultCommentSearchLimit = 25
	MaxCommentSearchLimit     = 100

	DefaultIssueQueryLimit = 50
	MaxIssueQueryLimit     = 200
)

var (
	ProjectQuerySortFields = []string{"name", "status", "updated_at", "created_at"}
	SprintQuerySortFields  = []string{"name", "status", "updated_at", "created_at"}
	EpicQuerySortFields    = []string{"name", "status", "updated_at", "created_at"}
	IssueQuerySortFields   = TaskQuerySortFields
	CommentQuerySortFields = []string{"created_at"}
)

const (
	ProjectQueryDefaultSort = "name"
	ProjectQueryDefaultDir  = "asc"
	SprintQueryDefaultSort  = "updated_at"
	SprintQueryDefaultDir   = "desc"
	EpicQueryDefaultSort    = "updated_at"
	EpicQueryDefaultDir     = "desc"
	IssueQueryDefaultSort   = TaskQueryDefaultSort
	IssueQueryDefaultDir    = TaskQueryDefaultDir
	CommentListDefaultSort  = "created_at"
	CommentListDefaultDir   = "asc"
	CommentSearchDefaultDir = "desc"
)

type CursorQuery struct {
	Limit   int
	SortBy  string
	SortDir string
	Cursor  string
}

type ProjectQuery struct {
	Status          string
	IncludeArchived bool
	CursorQuery
}

type SprintQuery struct {
	Status          string
	ProjectID       string
	IncludeArchived bool
	OverBudget      bool
	CostBudgetMin   *float64
	CostBudgetMax   *float64
	CursorQuery
}

type EpicQuery struct {
	Status          string
	ProjectID       string
	Search          string
	IncludeArchived bool
	CursorQuery
}

type IssueQuery struct {
	ProjectID string
	Status    string
	Query     string
	CursorQuery
}

type CommentQuery struct {
	Search        string
	EntityType    string
	EntityID      string
	EntityIDs     []string
	Author        string
	CreatedAfter  string
	CreatedBefore string
	CursorQuery
}

type NormalizedCursorQuery struct {
	Limit          int
	SortBy         string
	SortDir        string
	AfterSortValue string
	AfterID        string
}

func NormalizeProjectQuery(q ProjectQuery) (sqlstore.ProjectFilter, NormalizedCursorQuery, error) {
	n, err := normalizeCursorQuery(q.CursorQuery, DefaultGenericQueryLimit, MaxGenericQueryLimit, ProjectQueryDefaultSort, ProjectQueryDefaultDir, ProjectQuerySortFields)
	if err != nil {
		return sqlstore.ProjectFilter{}, NormalizedCursorQuery{}, err
	}
	return sqlstore.ProjectFilter{
		Status:          q.Status,
		IncludeArchived: q.IncludeArchived,
		Limit:           n.Limit + 1,
		SortBy:          n.SortBy,
		SortDir:         n.SortDir,
		AfterSortValue:  n.AfterSortValue,
		AfterID:         n.AfterID,
	}, n, nil
}

func NormalizeSprintQuery(q SprintQuery) (sqlstore.SprintFilter, NormalizedCursorQuery, error) {
	if q.CostBudgetMin != nil && (math.IsNaN(*q.CostBudgetMin) || math.IsInf(*q.CostBudgetMin, 0)) {
		return sqlstore.SprintFilter{}, NormalizedCursorQuery{}, &ValidationError{Field: "cost_budget_min", Message: "cost_budget_min must be a finite number"}
	}
	if q.CostBudgetMax != nil && (math.IsNaN(*q.CostBudgetMax) || math.IsInf(*q.CostBudgetMax, 0)) {
		return sqlstore.SprintFilter{}, NormalizedCursorQuery{}, &ValidationError{Field: "cost_budget_max", Message: "cost_budget_max must be a finite number"}
	}
	n, err := normalizeCursorQuery(q.CursorQuery, DefaultGenericQueryLimit, MaxGenericQueryLimit, SprintQueryDefaultSort, SprintQueryDefaultDir, SprintQuerySortFields)
	if err != nil {
		return sqlstore.SprintFilter{}, NormalizedCursorQuery{}, err
	}
	return sqlstore.SprintFilter{
		Status:          q.Status,
		ProjectID:       q.ProjectID,
		IncludeArchived: q.IncludeArchived,
		CostBudgetMin:   q.CostBudgetMin,
		CostBudgetMax:   q.CostBudgetMax,
		OverBudget:      q.OverBudget,
		Limit:           n.Limit + 1,
		SortBy:          n.SortBy,
		SortDir:         n.SortDir,
		AfterSortValue:  n.AfterSortValue,
		AfterID:         n.AfterID,
	}, n, nil
}

func NormalizeEpicQuery(q EpicQuery) (EpicListInput, NormalizedCursorQuery, error) {
	n, err := normalizeCursorQuery(q.CursorQuery, DefaultGenericQueryLimit, MaxGenericQueryLimit, EpicQueryDefaultSort, EpicQueryDefaultDir, EpicQuerySortFields)
	if err != nil {
		return EpicListInput{}, NormalizedCursorQuery{}, err
	}
	return EpicListInput{
		Status:          q.Status,
		ProjectID:       q.ProjectID,
		Search:          q.Search,
		IncludeArchived: q.IncludeArchived,
		Limit:           n.Limit + 1,
		SortBy:          n.SortBy,
		SortDir:         n.SortDir,
		AfterSortValue:  n.AfterSortValue,
		AfterID:         n.AfterID,
	}, n, nil
}

func NormalizeIssueQuery(q IssueQuery) (IssueListInput, NormalizedCursorQuery, error) {
	n, err := normalizeCursorQuery(q.CursorQuery, DefaultIssueQueryLimit, MaxIssueQueryLimit, IssueQueryDefaultSort, IssueQueryDefaultDir, IssueQuerySortFields)
	if err != nil {
		return IssueListInput{}, NormalizedCursorQuery{}, err
	}
	return IssueListInput{
		ProjectID:      q.ProjectID,
		Status:         q.Status,
		Query:          q.Query,
		Limit:          n.Limit + 1,
		SortBy:         n.SortBy,
		SortDir:        n.SortDir,
		AfterSortValue: n.AfterSortValue,
		AfterID:        n.AfterID,
	}, n, nil
}

func NormalizeCommentListQuery(q CommentQuery) (sqlstore.CommentFilter, NormalizedCursorQuery, error) {
	return normalizeCommentQuery(q, DefaultCommentListLimit, MaxCommentListLimit, CommentListDefaultDir)
}

func NormalizeCommentSearchQuery(q CommentQuery) (sqlstore.CommentFilter, NormalizedCursorQuery, error) {
	return normalizeCommentQuery(q, DefaultCommentSearchLimit, MaxCommentSearchLimit, CommentSearchDefaultDir)
}

func normalizeCommentQuery(q CommentQuery, defLimit, maxLimit int, defDir string) (sqlstore.CommentFilter, NormalizedCursorQuery, error) {
	n, err := normalizeCursorQuery(q.CursorQuery, defLimit, maxLimit, CommentListDefaultSort, defDir, CommentQuerySortFields)
	if err != nil {
		return sqlstore.CommentFilter{}, NormalizedCursorQuery{}, err
	}
	after, err := parseAdjacentQueryTime(q.CreatedAfter, "created_after")
	if err != nil {
		return sqlstore.CommentFilter{}, NormalizedCursorQuery{}, err
	}
	before, err := parseAdjacentQueryTime(q.CreatedBefore, "created_before")
	if err != nil {
		return sqlstore.CommentFilter{}, NormalizedCursorQuery{}, err
	}
	return sqlstore.CommentFilter{
		Search:         q.Search,
		EntityType:     q.EntityType,
		EntityID:       q.EntityID,
		EntityIDs:      q.EntityIDs,
		Author:         q.Author,
		CreatedAfter:   after,
		CreatedBefore:  before,
		Limit:          n.Limit + 1,
		SortBy:         n.SortBy,
		SortDir:        n.SortDir,
		AfterSortValue: n.AfterSortValue,
		AfterID:        n.AfterID,
	}, n, nil
}

func normalizeCursorQuery(q CursorQuery, defLimit, maxLimit int, defSortBy, defSortDir string, sortFields []string) (NormalizedCursorQuery, error) {
	limit := q.Limit
	if limit < 0 {
		return NormalizedCursorQuery{}, &ValidationError{Field: "limit", Message: "limit must be non-negative"}
	}
	if limit == 0 {
		limit = defLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	sortBy := defSortBy
	if q.SortBy != "" {
		v, err := pagination.ValidateSortBy(q.SortBy, sortFields...)
		if err != nil {
			return NormalizedCursorQuery{}, &ValidationError{Field: "sort_by", Message: err.Error()}
		}
		sortBy = v
	}
	sortDir := defSortDir
	if q.SortDir != "" {
		v, err := pagination.ValidateSortDir(q.SortDir)
		if err != nil {
			return NormalizedCursorQuery{}, &ValidationError{Field: "sort_dir", Message: err.Error()}
		}
		sortDir = v
	}
	n := NormalizedCursorQuery{Limit: limit, SortBy: sortBy, SortDir: sortDir}
	if q.Cursor != "" {
		c, err := pagination.Decode(q.Cursor)
		if err != nil {
			return NormalizedCursorQuery{}, &ValidationError{Field: "cursor", Message: fmt.Sprintf("invalid cursor: %v", err)}
		}
		if err := c.Validate(sortBy, sortDir); err != nil {
			return NormalizedCursorQuery{}, &ValidationError{Field: "cursor", Message: err.Error()}
		}
		n.AfterSortValue = c.SortValue
		n.AfterID = c.ID
	}
	return n, nil
}

func parseAdjacentQueryTime(raw, field string) (string, error) {
	if raw == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", &ValidationError{Field: field, Message: "invalid " + field + ": " + err.Error()}
	}
	return t.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional), nil
}

func ProjectQuerySortValue(p sqlstore.ProjectRecord, sortBy string) string {
	switch sortBy {
	case "name":
		return p.Name
	case "status":
		return p.Status
	case "updated_at":
		return p.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	case "created_at":
		return p.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	default:
		return ""
	}
}

func SprintQuerySortValue(sp sqlstore.SprintRecord, sortBy string) string {
	switch sortBy {
	case "name":
		return sp.Name
	case "status":
		return sp.Status
	case "updated_at":
		return sp.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	case "created_at":
		return sp.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	default:
		return ""
	}
}

func EpicQuerySortValue(e sqlstore.EpicRecord, sortBy string) string {
	switch sortBy {
	case "name":
		return e.Name
	case "status":
		return e.Status
	case "updated_at":
		return e.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	case "created_at":
		return e.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	default:
		return ""
	}
}

func CommentQuerySortValue(c sqlstore.CommentRecord, sortBy string) string {
	switch sortBy {
	case "created_at":
		return c.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayoutWithFractional)
	default:
		return ""
	}
}

func CommentQueryCursorID(c sqlstore.CommentRecord) string {
	return strconv.FormatInt(c.ID, 10)
}

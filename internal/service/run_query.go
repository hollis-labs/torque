package service

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

var RunQuerySortFields = []string{"started_at", "status", "duration", "cost"}

type RunQuery struct {
	TaskID       string
	ProjectID    string
	SprintID     string
	EpicID       string
	Executors    []string
	Profiles     []string
	Statuses     []string
	Since        string
	Until        string
	Limit        int
	Offset       int
	OffsetSet    bool
	SortBy       string
	SortDir      string
	Cursor       string
	IncludeTotal bool
}

type RunQueryResult struct {
	SortBy  string
	SortDir string
	Runs    []sqlstore.RunRecord
	Meta    pagination.PageMeta
}

// NormalizeRunQuery exposes the shared cohort validation to run facets.
func NormalizeRunQuery(q RunQuery) (sqlstore.RunFilter, int, string, string, error) {
	invalid := func(field, msg string) (sqlstore.RunFilter, int, string, string, error) {
		return sqlstore.RunFilter{}, 0, "", "", &ValidationError{Field: field, Message: msg}
	}
	if q.Limit < 0 {
		return invalid("limit", "limit must be non-negative")
	}
	if q.Offset < 0 {
		return invalid("offset", "offset must be non-negative")
	}
	if q.Cursor != "" && q.Offset > 0 {
		return invalid("cursor", "cursor cannot be combined with a positive offset")
	}
	limit := q.Limit
	if limit == 0 {
		limit = pagination.DefaultLimit
	}
	if limit > pagination.MaxLimit {
		limit = pagination.MaxLimit
	}
	sortBy := q.SortBy
	if sortBy == "" {
		sortBy = "started_at"
	}
	sortBy, err := pagination.ValidateSortBy(sortBy, RunQuerySortFields...)
	if err != nil {
		return invalid("sort_by", err.Error())
	}
	dir := q.SortDir
	if dir == "" {
		dir = "desc"
	}
	dir, err = pagination.ValidateSortDir(dir)
	if err != nil {
		return invalid("sort_dir", err.Error())
	}
	f := sqlstore.RunFilter{TaskID: q.TaskID, ProjectID: q.ProjectID, SprintID: q.SprintID, EpicID: q.EpicID, Statuses: trimUniqueNonEmpty(q.Statuses), Executors: trimUniqueNonEmpty(q.Executors), Profiles: trimUniqueNonEmpty(q.Profiles), Limit: limit + 1, Offset: q.Offset, SortBy: sortBy, SortDir: dir}
	if f.Since, err = ParseRunQueryTime(q.Since); err != nil {
		return invalid("since", err.Error())
	}
	if f.Until, err = ParseRunQueryTime(q.Until); err != nil {
		return invalid("until", err.Error())
	}
	if !f.Since.IsZero() && !f.Until.IsZero() && f.Since.After(f.Until) {
		return invalid("until", "until must be at or after since")
	}
	if q.Cursor != "" {
		c, err := pagination.Decode(q.Cursor)
		if err != nil {
			return invalid("cursor", err.Error())
		}
		if err = c.Validate(sortBy, dir); err != nil {
			return invalid("cursor", err.Error())
		}
		id, err := strconv.ParseInt(c.ID, 10, 64)
		if err != nil || id <= 0 {
			return invalid("cursor", "cursor run ID must be a positive integer")
		}
		f.AfterID = id
		f.AfterSortValue = c.SortValue
	}
	return f, limit, sortBy, dir, nil
}

// ParseRunQueryTime accepts RFC3339 (including fractions) or Unix millis.
func ParseRunQueryTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.UnixMilli(n).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339 timestamp or unix millis")
}

func (s *RunService) Query(q RunQuery) (RunQueryResult, error) {
	f, limit, sortBy, dir, err := NormalizeRunQuery(q)
	if err != nil {
		return RunQueryResult{}, err
	}
	var runs []sqlstore.RunRecord
	var total int
	if q.IncludeTotal {
		runs, total, err = s.store.ListRunsPage(f)
	} else {
		runs, err = s.store.ListRunsFiltered(f)
	}
	if err != nil {
		if errors.Is(err, sqlstore.ErrInvalidCursor) {
			err = &ValidationError{Field: "cursor", Message: err.Error()}
		}
		return RunQueryResult{}, err
	}
	hasMore := len(runs) > limit
	var nextCursor *string
	if hasMore {
		runs = runs[:limit]
		last := runs[len(runs)-1]
		cursor := pagination.Encode(sortBy, dir, last.QuerySortValue, strconv.FormatInt(last.ID, 10))
		nextCursor = &cursor
	}
	var totalPtr, offset *int
	if q.IncludeTotal {
		totalPtr = &total
	}
	if (q.OffsetSet || q.Offset > 0) && q.Cursor == "" {
		offset = &q.Offset
	}
	meta := pagination.NewPageMeta(len(runs), limit, hasMore, nextCursor, totalPtr, offset)
	return RunQueryResult{Runs: runs, Meta: meta, SortBy: sortBy, SortDir: dir}, nil
}

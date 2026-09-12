package httpserver

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// Adjacent list compatibility contract:
//
// Family    Legacy keys                      Advanced opt-in keys                                             Defaults / max / order               Shape
// project   status, include_archived         limit, cursor, sort_by, sort_dir                                  100 / 500 / name asc                {items,meta}
// sprint    status, project_id               include_archived, over_budget, cost_budget_min, cost_budget_max,  100 / 500 / updated_at desc         {items,meta}
//                                             limit, cursor, sort_by, sort_dir
// epic      status, project_id               include_archived, search, limit, cursor, sort_by, sort_dir        100 / 500 / updated_at desc         {items,meta}
// issue     project_id; search: q,limit      status, query, limit, cursor, sort_by, sort_dir                   50 / 200 / priority asc             {items,meta}; legacy search total=len(page), q+query rejected
// comment   entity_type, entity_id           author, created_after, created_before, entity_ids, limit,         list 50/200 asc; search 25/100 desc {items,meta}; entity_id wins when both entity_id/entity_ids are supplied
//                                             cursor, sort_by, sort_dir
//
// Every handler parses RawQuery first and rejects malformed URL encodings,
// unknown keys, and repeated scalar keys before choosing legacy vs advanced.
// Valid legacy calls keep their old response shapes and fetch-all behavior.
// Advanced mode uses +1 overfetch and returns no total unless a real count is
// computed; today meta contains returned/limit/has_more/next_cursor only.

type httpQueryError struct {
	field   string
	message string
}

func (e httpQueryError) Error() string { return e.message }

func parseStrictQuery(r *http.Request, allowed map[string]bool) (url.Values, *httpQueryError) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, &httpQueryError{field: "query", message: "invalid query string: " + err.Error()}
	}
	for key, vals := range q {
		if !allowed[key] {
			return nil, &httpQueryError{field: key, message: "unknown query parameter: " + key}
		}
		if len(vals) > 1 {
			return nil, &httpQueryError{field: key, message: key + " cannot be repeated"}
		}
	}
	return q, nil
}

func hasAnyQueryKey(q url.Values, keys ...string) bool {
	for _, key := range keys {
		if _, ok := q[key]; ok {
			return true
		}
	}
	return false
}

func queryString(q url.Values, key string) string {
	if vals, ok := q[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}

func queryInt(q url.Values, key string) (int, *httpQueryError) {
	if _, ok := q[key]; !ok {
		return 0, nil
	}
	raw := strings.TrimSpace(queryString(q, key))
	if raw == "" {
		return 0, &httpQueryError{field: key, message: key + " must be an integer"}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, &httpQueryError{field: key, message: key + " must be an integer"}
	}
	if key == "limit" && n < 0 {
		return 0, &httpQueryError{field: key, message: key + " must be non-negative"}
	}
	if int64(int(n)) != n {
		return 0, &httpQueryError{field: key, message: key + " must fit in a Go int"}
	}
	return int(n), nil
}

func queryFloatPtr(q url.Values, key string) (*float64, *httpQueryError) {
	if _, ok := q[key]; !ok {
		return nil, nil
	}
	raw := strings.TrimSpace(queryString(q, key))
	if raw == "" {
		return nil, &httpQueryError{field: key, message: key + " must be a finite number"}
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, &httpQueryError{field: key, message: key + " must be a finite number"}
	}
	return &f, nil
}

func queryBool(q url.Values, key string) (bool, *httpQueryError) {
	if _, ok := q[key]; !ok {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(queryString(q, key))) {
	case "", "false", "0", "no":
		return false, nil
	case "true", "1", "yes":
		return true, nil
	default:
		return false, &httpQueryError{field: key, message: key + " must be true/false, 1/0, or yes/no"}
	}
}

func queryCursor(q url.Values) (service.CursorQuery, *httpQueryError) {
	limit, err := queryInt(q, "limit")
	if err != nil {
		return service.CursorQuery{}, err
	}
	return service.CursorQuery{
		Limit:   limit,
		SortBy:  queryString(q, "sort_by"),
		SortDir: queryString(q, "sort_dir"),
		Cursor:  queryString(q, "cursor"),
	}, nil
}

func writeHTTPQueryError(w http.ResponseWriter, err *httpQueryError) {
	writeFieldError(w, http.StatusBadRequest, err.field, err.Error())
}

func writeAdjacentServiceError(w http.ResponseWriter, err error) {
	var validation *service.ValidationError
	var disabled *service.FeatureDisabledError
	switch {
	case errors.As(err, &validation):
		writeFieldError(w, http.StatusBadRequest, validation.Field, validation.Message)
	case errors.As(err, &disabled):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, sqlstore.ErrInvalidCursor), errors.Is(err, sqlstore.ErrInvalidCommentCursor):
		writeFieldError(w, http.StatusBadRequest, "cursor", fmt.Sprintf("cursor: %v", err))
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func advancedMeta(limit int, sortBy, sortDir string, hasMore bool, nextCursor string, returned int) map[string]any {
	return map[string]any{
		"returned":    returned,
		"limit":       limit,
		"has_more":    hasMore,
		"next_cursor": nullableString(nextCursor),
		"sort_by":     sortBy,
		"sort_dir":    sortDir,
	}
}

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

// Adjacent lists always use bounded cursor pages (default 50, max 200).
// HTTP and MCP share service normalization. include_total is opt-in and counts
// the complete filtered cohort, excluding cursor and page bounds. There is no
// legacy fetch-all mode; malformed, unknown and repeated query keys reject.

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
	includeTotal, err := queryBool(q, "include_total")
	if err != nil {
		return service.CursorQuery{}, err
	}
	return service.CursorQuery{
		IncludeTotal: includeTotal,
		Limit:        limit,
		SortBy:       queryString(q, "sort_by"),
		SortDir:      queryString(q, "sort_dir"),
		Cursor:       queryString(q, "cursor"),
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

func advancedMeta(limit int, sortBy, sortDir string, hasMore bool, nextCursor string, returned int, total ...*int) map[string]any {
	meta := map[string]any{
		"returned":    returned,
		"limit":       limit,
		"has_more":    hasMore,
		"next_cursor": nullableString(nextCursor),
		"sort_by":     sortBy,
		"sort_dir":    sortDir,
	}
	if len(total) > 0 && total[0] != nil {
		meta["total"] = *total[0]
	}
	return meta
}

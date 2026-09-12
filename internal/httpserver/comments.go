package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

// defaultCommentAuthor is used when a POST has no explicit author and no
// X-User header. Keeps the GUI composer able to post without forcing the user
// to pick a name.
const defaultCommentAuthor = "user"

// commentEntityFromRequest resolves the (entity_type, entity_id) target for a
// comment request.
//
// The nested route `/tasks/{id}/comments` always implies entity_type="task";
// the URL param is the entity id. The flat `/comments` endpoint requires the
// pair via query string (`?entity_type=…&entity_id=…`).
func commentEntityFromRequest(r *http.Request) (entityType, entityID string) {
	if id := chi.URLParam(r, "id"); id != "" {
		return sqlstore.EntityTypeTask, id
	}
	q := r.URL.Query()
	entityType = q.Get("entity_type")
	entityID = q.Get("entity_id")
	if entityType == "" {
		entityType = sqlstore.EntityTypeTask
	}
	return entityType, entityID
}

// legacyTaskIDError is the rejection message for the legacy task_id alias on
// the flat /comments endpoint. The alias was removed in CW-20260503-0007;
// callers must use entity_type + entity_id directly.
const legacyTaskIDError = "task_id is no longer accepted on /comments; use entity_type=task&entity_id=<id> (or POST {\"entity_type\":\"task\",\"entity_id\":\"<id>\"})"

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	allowed := map[string]bool{"entity_type": true, "entity_id": true, "task_id": true, "entity_ids": true, "author": true, "created_after": true, "created_before": true, "limit": true, "cursor": true, "sort_by": true, "sort_dir": true}
	q, qerr := parseStrictQuery(r, allowed)
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	// Nested route resolves via URL param; flat route never accepts task_id.
	if chi.URLParam(r, "id") == "" && queryString(q, "task_id") != "" {
		writeError(w, http.StatusBadRequest, legacyTaskIDError)
		return
	}
	isNested := chi.URLParam(r, "id") != ""
	if isNested {
		if et := queryString(q, "entity_type"); et != "" && et != sqlstore.EntityTypeTask {
			writeFieldError(w, http.StatusBadRequest, "entity_type", "nested task comments require entity_type=task")
			return
		}
		if eid := queryString(q, "entity_id"); eid != "" && eid != chi.URLParam(r, "id") {
			writeFieldError(w, http.StatusBadRequest, "entity_id", "nested task comments cannot switch entity_id")
			return
		}
		if _, ok := q["entity_ids"]; ok {
			writeFieldError(w, http.StatusBadRequest, "entity_ids", "nested task comments cannot use entity_ids")
			return
		}
	}
	entityType, entityID := sqlstore.EntityTypeTask, chi.URLParam(r, "id")
	var entityIDs []string
	if !isNested {
		entityType = queryString(q, "entity_type")
		entityID = queryString(q, "entity_id")
		if entityType == "" {
			entityType = sqlstore.EntityTypeTask
		}
		var arrErr *httpQueryError
		entityIDs, arrErr = queryStringArray(q, "entity_ids")
		if arrErr != nil {
			writeHTTPQueryError(w, arrErr)
			return
		}
	}
	if entityID == "" && len(entityIDs) == 0 {
		writeFieldError(w, http.StatusBadRequest, "entity_id", "entity_id or entity_ids is required")
		return
	}
	if !hasAnyQueryKey(q, "entity_ids", "author", "created_after", "created_before", "limit", "cursor", "sort_by", "sort_dir") {
		comments, err := s.svc.Comment.List(entityType, entityID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if comments == nil {
			comments = []sqlstore.CommentRecord{}
		}
		writeJSON(w, http.StatusOK, comments)
		return
	}
	cursor, qerr := queryCursor(q)
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	filter, normalized, err := service.NormalizeCommentListQuery(service.CommentQuery{
		EntityType:    entityType,
		EntityID:      entityID,
		EntityIDs:     entityIDs,
		Author:        queryString(q, "author"),
		CreatedAfter:  queryString(q, "created_after"),
		CreatedBefore: queryString(q, "created_before"),
		CursorQuery:   cursor,
	})
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	comments, err := s.svc.Comment.ListFiltered(filter)
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	hasMore := len(comments) > normalized.Limit
	if hasMore {
		comments = comments[:normalized.Limit]
	}
	if comments == nil {
		comments = []sqlstore.CommentRecord{}
	}
	nextCursor := ""
	if hasMore && len(comments) > 0 {
		last := comments[len(comments)-1]
		nextCursor = pagination.Encode(normalized.SortBy, normalized.SortDir, service.CommentQuerySortValue(last, normalized.SortBy), service.CommentQueryCursorID(last))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": comments, "meta": advancedMeta(normalized.Limit, normalized.SortBy, normalized.SortDir, hasMore, nextCursor, len(comments))})
}

func (s *Server) searchComments(w http.ResponseWriter, r *http.Request) {
	allowed := map[string]bool{"query": true, "entity_type": true, "entity_id": true, "entity_ids": true, "author": true, "created_after": true, "created_before": true, "limit": true, "cursor": true, "sort_by": true, "sort_dir": true}
	q, qerr := parseStrictQuery(r, allowed)
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	if queryString(q, "query") == "" {
		writeFieldError(w, http.StatusBadRequest, "query", "query is required")
		return
	}
	cursor, qerr := queryCursor(q)
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	entityIDs, qerr := queryStringArray(q, "entity_ids")
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	filter, normalized, err := service.NormalizeCommentSearchQuery(service.CommentQuery{
		Search:        queryString(q, "query"),
		EntityType:    queryString(q, "entity_type"),
		EntityID:      queryString(q, "entity_id"),
		EntityIDs:     entityIDs,
		Author:        queryString(q, "author"),
		CreatedAfter:  queryString(q, "created_after"),
		CreatedBefore: queryString(q, "created_before"),
		CursorQuery:   cursor,
	})
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	comments, err := s.svc.Comment.Search(filter)
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	hasMore := len(comments) > normalized.Limit
	if hasMore {
		comments = comments[:normalized.Limit]
	}
	if comments == nil {
		comments = []sqlstore.CommentRecord{}
	}
	nextCursor := ""
	if hasMore && len(comments) > 0 {
		last := comments[len(comments)-1]
		nextCursor = pagination.Encode(normalized.SortBy, normalized.SortDir, service.CommentQuerySortValue(last, normalized.SortBy), service.CommentQueryCursorID(last))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": comments, "meta": advancedMeta(normalized.Limit, normalized.SortBy, normalized.SortDir, hasMore, nextCursor, len(comments))})
}

func queryExactBool(q url.Values, key string) (bool, *httpQueryError) {
	if _, ok := q[key]; !ok {
		return false, nil
	}
	switch queryString(q, key) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, &httpQueryError{field: key, message: key + " must be exactly true or false"}
	}
}

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeFieldError(w, http.StatusBadRequest, "id", "id must be a positive integer")
		return
	}
	q, qerr := parseStrictQuery(r, map[string]bool{"author": true, "force": true})
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	if _, ok := q["author"]; !ok {
		writeFieldError(w, http.StatusBadRequest, "author", "author is required")
		return
	}
	force, qerr := queryExactBool(q, "force")
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	if err := s.svc.Comment.Delete(id, queryString(q, "author"), force); err != nil {
		var permission *service.PermissionError
		switch {
		case errors.Is(err, sqlstore.ErrCommentNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.As(err, &permission):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func queryStringArray(q url.Values, key string) ([]string, *httpQueryError) {
	if _, ok := q[key]; !ok {
		return nil, nil
	}
	raw := queryString(q, key)
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return nil, &httpQueryError{field: key, message: key + " must be a JSON array of strings"}
	}
	var elems []any
	if err := json.Unmarshal([]byte(raw), &elems); err != nil {
		return nil, &httpQueryError{field: key, message: "invalid " + key + " JSON: " + err.Error()}
	}
	out := make([]string, 0, len(elems))
	for i, elem := range elems {
		s, ok := elem.(string)
		if !ok {
			return nil, &httpQueryError{field: key, message: fmt.Sprintf("%s[%d] must be a string", key, i)}
		}
		out = append(out, s)
	}
	return out, nil
}

func (s *Server) addComment(w http.ResponseWriter, r *http.Request) {
	// Reject the legacy task_id query alias on the flat endpoint up front so
	// the error message is consistent with listComments.
	isNested := chi.URLParam(r, "id") != ""
	if !isNested && r.URL.Query().Get("task_id") != "" {
		writeError(w, http.StatusBadRequest, legacyTaskIDError)
		return
	}

	var req struct {
		EntityType string `json:"entity_type"`
		EntityID   string `json:"entity_id"`
		// TaskID is decoded only to detect and reject legacy {"task_id":...}
		// bodies on the flat endpoint with a clear error.
		TaskID  string `json:"task_id"`
		Author  string `json:"author"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		// tolerate empty body for nested route where fields come from URL/header
		if _, ok := err.(*json.SyntaxError); ok {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Reject legacy {"task_id":...} bodies on the flat endpoint.
	if !isNested && req.TaskID != "" {
		writeError(w, http.StatusBadRequest, legacyTaskIDError)
		return
	}

	// Nested route /tasks/{id}/comments — URL param wins and implies type=task.
	if isNested {
		req.EntityType = sqlstore.EntityTypeTask
		req.EntityID = chi.URLParam(r, "id")
	}

	if req.EntityType == "" {
		req.EntityType = sqlstore.EntityTypeTask
	}

	if req.Author == "" {
		if h := strings.TrimSpace(r.Header.Get("X-User")); h != "" {
			req.Author = h
		} else {
			req.Author = defaultCommentAuthor
		}
	}

	if req.EntityID == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "entity_id and content are required")
		return
	}

	created, err := s.svc.Comment.Add(req.EntityType, req.EntityID, req.Author, req.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

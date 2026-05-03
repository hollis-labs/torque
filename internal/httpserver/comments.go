package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
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
	// Nested route resolves via URL param; flat route never accepts task_id.
	if chi.URLParam(r, "id") == "" && r.URL.Query().Get("task_id") != "" {
		writeError(w, http.StatusBadRequest, legacyTaskIDError)
		return
	}
	entityType, entityID := commentEntityFromRequest(r)
	if entityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}
	comments, err := s.svc.Comment.List(entityType, entityID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if comments == nil {
		comments = []sqlstore.CommentRecord{}
	}
	writeJSON(w, http.StatusOK, comments)
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

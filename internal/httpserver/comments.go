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
// the URL param is the entity id. The flat `/comments` endpoint accepts the
// pair via query string (`?entity_type=…&entity_id=…`) or the legacy
// `?task_id=…` shape, which is normalized to entity_type="task".
func commentEntityFromRequest(r *http.Request) (entityType, entityID string) {
	if id := chi.URLParam(r, "id"); id != "" {
		return sqlstore.EntityTypeTask, id
	}
	q := r.URL.Query()
	entityType = q.Get("entity_type")
	entityID = q.Get("entity_id")
	if entityType == "" && entityID == "" {
		// Legacy ?task_id= shape — preserved for curl/scripts.
		if tid := q.Get("task_id"); tid != "" {
			return sqlstore.EntityTypeTask, tid
		}
	}
	if entityType == "" {
		entityType = sqlstore.EntityTypeTask
	}
	return entityType, entityID
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		EntityType string `json:"entity_type"`
		EntityID   string `json:"entity_id"`
		// TaskID is accepted on the flat endpoint as a legacy alias so curl /
		// scripts that POST {"task_id":...} keep working; nested-route callers
		// don't need it. Server-side it normalizes to entity_type="task".
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

	// Nested route /tasks/{id}/comments — URL param wins and implies type=task.
	if id := chi.URLParam(r, "id"); id != "" {
		req.EntityType = sqlstore.EntityTypeTask
		req.EntityID = id
	} else if req.EntityType == "" && req.EntityID == "" && req.TaskID != "" {
		// Legacy {"task_id":...} body on the flat endpoint.
		req.EntityType = sqlstore.EntityTypeTask
		req.EntityID = req.TaskID
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

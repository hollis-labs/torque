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

// taskIDFromRequest returns the task id for a comments request, preferring the
// nested URL param (/tasks/{id}/comments) and falling back to the ?task_id=
// query string used by the flat endpoint.
func taskIDFromRequest(r *http.Request) string {
	if id := chi.URLParam(r, "id"); id != "" {
		return id
	}
	return r.URL.Query().Get("task_id")
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	taskID := taskIDFromRequest(r)
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task_id is required")
		return
	}
	comments, err := s.svc.Comment.List(taskID)
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

	if id := chi.URLParam(r, "id"); id != "" {
		req.TaskID = id
	}

	if req.Author == "" {
		if h := strings.TrimSpace(r.Header.Get("X-User")); h != "" {
			req.Author = h
		} else {
			req.Author = defaultCommentAuthor
		}
	}

	if req.TaskID == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "task_id and content are required")
		return
	}

	created, err := s.svc.Comment.Add(req.TaskID, req.Author, req.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

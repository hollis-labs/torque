package httpserver

import (
	"net/http"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
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
	writeJSON(w, http.StatusOK, map[string]interface{}{"comments": comments})
}

func (s *Server) addComment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID  string `json:"task_id"`
		Author  string `json:"author"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.TaskID == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "task_id and content are required")
		return
	}
	if err := s.svc.Comment.Add(req.TaskID, req.Author, req.Content); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

package httpserver

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"runs": []sqlstore.RunRecord{}})
		return
	}
	runs, err := s.svc.Run.List(taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []sqlstore.RunRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"runs": runs})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run ID")
		return
	}
	run, err := s.svc.Run.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}

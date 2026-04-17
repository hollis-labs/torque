package httpserver

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	// Accept task_id either from the URL (alias route /tasks/{id}/artifacts)
	// or from the query string (/artifacts?task_id=...). URL wins.
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		taskID = r.URL.Query().Get("task_id")
	}
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task_id is required")
		return
	}
	artifacts, err := s.svc.Artifact.List(taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if artifacts == nil {
		artifacts = []sqlstore.ArtifactRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"artifacts": artifacts})
}

func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid artifact ID")
		return
	}
	art, err := s.svc.Artifact.Get(id)
	if err != nil {
		if errors.Is(err, sqlstore.ErrArtifactNotFound) {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, art)
}

func (s *Server) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid artifact ID")
		return
	}
	if err := s.svc.Artifact.Delete(id); err != nil {
		if errors.Is(err, sqlstore.ErrArtifactNotFound) {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createArtifact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID   string `json:"task_id"`
		RunID    *int64 `json:"run_id"`
		Type     string `json:"type"`
		Content  string `json:"content"`
		URL      string `json:"url"`
		FilePath string `json:"file_path"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.TaskID == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "task_id and type are required")
		return
	}

	rec := &sqlstore.ArtifactRecord{
		TaskID:   req.TaskID,
		Type:     req.Type,
		Content:  req.Content,
		URL:      req.URL,
		FilePath: req.FilePath,
	}
	if req.RunID != nil {
		rec.RunID = sql.NullInt64{Int64: *req.RunID, Valid: true}
	}

	if err := s.svc.Artifact.Create(rec); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("artifact.created", map[string]interface{}{"task_id": req.TaskID, "artifact_id": rec.ID})
	writeJSON(w, http.StatusCreated, map[string]interface{}{"id": rec.ID})
}

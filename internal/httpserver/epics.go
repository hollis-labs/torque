package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func epicJSON(e *sqlstore.EpicRecord) map[string]interface{} {
	var priority interface{}
	if e.Priority.Valid {
		priority = e.Priority.Int64
	}
	return map[string]interface{}{
		"id":          e.ID,
		"name":        e.Name,
		"description": e.Description,
		"status":      e.Status,
		"priority":    priority,
		"project_id":  nullStr(e.ProjectID),
		"created_at":  e.CreatedAt,
		"updated_at":  e.UpdatedAt,
	}
}

func epicsJSON(epics []sqlstore.EpicRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(epics))
	for i := range epics {
		out[i] = epicJSON(&epics[i])
	}
	return out
}

func (s *Server) listEpics(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	projectID := r.URL.Query().Get("project_id")
	epics, err := s.svc.Epic.List(status, projectID)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if epics == nil {
		epics = []sqlstore.EpicRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"epics": epicsJSON(epics)})
}

func (s *Server) getEpic(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	epic, err := s.svc.Epic.Get(id)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, epicJSON(epic))
}

func (s *Server) createEpic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Priority    *int64 `json:"priority"`
		ProjectID   string `json:"project_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	epic, err := s.svc.Epic.Create(service.EpicCreateInput{
		Name:        req.Name,
		Description: req.Description,
		Priority:    req.Priority,
		ProjectID:   req.ProjectID,
	})
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("epic.created", map[string]interface{}{"epic_id": epic.ID, "name": epic.Name})
	writeJSON(w, http.StatusCreated, epicJSON(epic))
}

func (s *Server) updateEpic(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req map[string]interface{}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	input := service.EpicUpdateInput{}
	if v, ok := req["name"].(string); ok {
		input.Name = &v
	}
	if v, ok := req["description"].(string); ok {
		input.Description = &v
	}
	if v, ok := req["status"].(string); ok {
		input.Status = &v
	}
	if v, ok := req["priority"].(float64); ok {
		p := int64(v)
		input.Priority = &p
	}
	if v, ok := req["project_id"].(string); ok {
		input.ProjectID = &v
	}

	if err := s.svc.Epic.Update(id, input); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	epic, err := s.svc.Epic.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("epic.updated", map[string]interface{}{"epic_id": id})
	writeJSON(w, http.StatusOK, epicJSON(epic))
}

func (s *Server) deleteEpic(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Epic.Delete(id); err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("epic.deleted", map[string]interface{}{"epic_id": id})
	w.WriteHeader(http.StatusNoContent)
}

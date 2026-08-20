package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func sprintJSON(sp *sqlstore.SprintRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":            sp.ID,
		"name":          sp.Name,
		"goal":          sp.Goal,
		"status":        sp.Status,
		"approval_mode": sp.ApprovalMode,
		"cost_budget":   nullFloat(sp.CostBudget),
		"project_id":    nullStr(sp.ProjectID),
		"started_at":    nullTime(sp.StartedAt),
		"ended_at":      nullTime(sp.EndedAt),
		"created_at":    sp.CreatedAt,
		"updated_at":    sp.UpdatedAt,
	}
}

func sprintsJSON(sprints []sqlstore.SprintRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(sprints))
	for i := range sprints {
		out[i] = sprintJSON(&sprints[i])
	}
	return out
}

func (s *Server) listSprints(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	projectID := r.URL.Query().Get("project_id")
	sprints, err := s.svc.Sprint.List(status, projectID, false)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sprints == nil {
		sprints = []sqlstore.SprintRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"sprints": sprintsJSON(sprints)})
}

func (s *Server) getSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sprint, err := s.svc.Sprint.Get(id)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sprintJSON(sprint))
}

func (s *Server) createSprint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string   `json:"name"`
		Goal         string   `json:"goal"`
		ApprovalMode string   `json:"approval_mode"`
		CostBudget   *float64 `json:"cost_budget"`
		ProjectID    string   `json:"project_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	sprint, err := s.svc.Sprint.Create(service.SprintCreateInput{
		Name:         req.Name,
		Goal:         req.Goal,
		ApprovalMode: req.ApprovalMode,
		CostBudget:   req.CostBudget,
		ProjectID:    req.ProjectID,
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

	s.sse.Broadcast("sprint.created", map[string]interface{}{"sprint_id": sprint.ID, "name": sprint.Name})
	writeJSON(w, http.StatusCreated, sprintJSON(sprint))
}

func (s *Server) updateSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req map[string]interface{}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	update := sqlstore.SprintUpdate{}
	if v, ok := req["name"].(string); ok {
		update.Name = &v
	}
	if v, ok := req["goal"].(string); ok {
		update.Goal = &v
	}
	if v, ok := req["approval_mode"].(string); ok {
		update.ApprovalMode = &v
	}
	if v, ok := req["cost_budget"].(float64); ok {
		update.CostBudget = &v
	}
	if v, ok := req["project_id"].(string); ok {
		update.ProjectID = &v
	}

	if err := s.svc.Sprint.Update(id, update); err != nil {
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

	sprint, err := s.svc.Sprint.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("sprint.updated", map[string]interface{}{"sprint_id": id})
	writeJSON(w, http.StatusOK, sprintJSON(sprint))
}

func (s *Server) deleteSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Sprint.Delete(id); err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("sprint.deleted", map[string]interface{}{"sprint_id": id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) transitionSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := s.svc.Sprint.Transition(id, req.Status); err != nil {
		if _, ok := err.(*service.TransitionError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sprint, err := s.svc.Sprint.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("sprint.transitioned", map[string]interface{}{"sprint_id": id, "status": req.Status})
	writeJSON(w, http.StatusOK, sprintJSON(sprint))
}

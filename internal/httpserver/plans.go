package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// PlanCreateRequest is the POST /api/v1/plans body.
type PlanCreateRequest struct {
	Title       string                    `json:"title"`
	Description string                    `json:"description,omitempty"`
	Priority    int                       `json:"priority,omitempty"`
	ProjectID   string                    `json:"project_id,omitempty"`
	SprintID    string                    `json:"sprint_id,omitempty"`
	EpicID      string                    `json:"epic_id,omitempty"`
	Phases      []service.PlanPhaseInput  `json:"phases,omitempty"`
	Tags        []string                  `json:"tags,omitempty"`
}

// PlanAddPhaseRequest is the POST /api/v1/plans/{id}/phases body.
type PlanAddPhaseRequest struct {
	Name       string `json:"name"`
	Acceptance string `json:"acceptance,omitempty"`
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.Task.List(sqlstore.TaskFilter{Kind: "plan"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := s.tasksJSON(tasks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"plans": out})
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	detail, err := s.svc.Plan.Get(id)
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var req PlanCreateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	task, err := s.svc.Plan.Create(service.PlanCreateInput{
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		ProjectID:   req.ProjectID,
		SprintID:    req.SprintID,
		EpicID:      req.EpicID,
		Phases:      req.Phases,
		Tags:        req.Tags,
	})
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	detail, err := s.svc.Plan.Get(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("plan.created", map[string]interface{}{"plan_id": task.ID, "title": task.Title})
	writeJSON(w, http.StatusCreated, detail)
}

func (s *Server) addPlanPhase(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req PlanAddPhaseRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	phaseID, err := s.svc.Plan.AddPhase(id, req.Name, req.Acceptance)
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("plan.phase.added", map[string]interface{}{"plan_id": id, "phase_id": phaseID})
	writeJSON(w, http.StatusCreated, map[string]string{"phase_id": phaseID})
}

func (s *Server) removePlanPhase(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	phaseID := chi.URLParam(r, "phase_id")
	if err := s.svc.Plan.RemovePhase(id, phaseID); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("plan.phase.removed", map[string]interface{}{"plan_id": id, "phase_id": phaseID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPlanChildren(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	phaseID := r.URL.Query().Get("phase_id")
	children, err := s.svc.Plan.ListChildren(id, phaseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := s.tasksJSON(children)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out})
}

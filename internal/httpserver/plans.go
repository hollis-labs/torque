package httpserver

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/planstart"
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
	payload, err := s.planDetailJSON(detail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// planDetailJSON renders a PlanDetail with the same lowercase-keyed task
// shape the rest of the HTTP API uses, so the GUI can reuse its existing
// Task type for the plan task field.
func (s *Server) planDetailJSON(detail *service.PlanDetail) (map[string]interface{}, error) {
	tags, err := s.svc.Task.ListTags(detail.Task.ID)
	if err != nil {
		return nil, err
	}
	agg, err := s.svc.Run.Aggregate(detail.Task.ID)
	if err != nil {
		return nil, err
	}
	subs, err := s.svc.Task.ListSubtodos(detail.Task.ID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"task":     taskJSON(detail.Task, tags, agg, subs, s.collectionNameForTask(detail.Task)),
		"plan":     detail.Plan,
		"progress": detail.Progress,
	}, nil
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
	payload, err := s.planDetailJSON(detail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("plan.created", map[string]interface{}{"plan_id": task.ID, "title": task.Title})
	writeJSON(w, http.StatusCreated, payload)
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

// PlanStartRequest is the optional body for POST /api/v1/plans/{id}/start.
// Both fields are optional — workdir defaults to the plan task's
// WorkingDir column when empty (CW-20260503-0017, S2.1).
type PlanStartRequest struct {
	Workdir string   `json:"workdir,omitempty"`
	Env     []string `json:"env,omitempty"`
}

// startPlan boots an Orchestrator session for the named plan and
// transitions the plan task to `doing`. Returns
// `{session_id, plan_id, started_at}` on success. Idempotency: when
// an orchestrator session is already running, responds 409 with the
// existing session_id surfaced in the body so the client can route
// to the live session view rather than show an error.
func (s *Server) startPlan(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "session manager not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	var req PlanStartRequest
	// Body is optional — empty bodies are fine.
	if r.ContentLength > 0 {
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	res, err := planstart.Start(r.Context(), s.svc.Store(), s.sessions, id, planstart.Options{
		Workdir: req.Workdir,
		Env:     req.Env,
	})
	if err != nil {
		switch {
		case errors.Is(err, planstart.ErrAlreadyOrchestrating):
			// Surface the existing session_id alongside the 409 so the
			// GUI can route to its detail view.
			payload := map[string]interface{}{
				"error":      err.Error(),
				"session_id": "",
				"plan_id":    id,
			}
			if res != nil {
				payload["session_id"] = res.SessionID
				payload["started_at"] = res.StartedAt.Format("2006-01-02T15:04:05Z07:00")
			}
			writeJSON(w, http.StatusConflict, payload)
			return
		case errors.Is(err, planstart.ErrPlanNotFound),
			errors.Is(err, planstart.ErrPlanWrongStatus),
			errors.Is(err, planstart.ErrWorkdirRequired):
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		case errors.Is(err, planstart.ErrSessionMgrMissing):
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.sse.Broadcast("plan.started", map[string]interface{}{
		"plan_id":    res.PlanID,
		"session_id": res.SessionID,
	})
	writeJSON(w, http.StatusOK, res)
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

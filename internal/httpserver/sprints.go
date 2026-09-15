package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
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
	allowed := map[string]bool{"status": true, "project_id": true, "include_archived": true, "over_budget": true, "cost_budget_min": true, "cost_budget_max": true, "limit": true, "cursor": true, "sort_by": true, "sort_dir": true}
	q, qerr := parseStrictQuery(r, allowed)
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	status := queryString(q, "status")
	projectID := queryString(q, "project_id")
	if !hasAnyQueryKey(q, "include_archived", "over_budget", "cost_budget_min", "cost_budget_max", "limit", "cursor", "sort_by", "sort_dir") {
		sprints, err := s.svc.Sprint.List(sqlstore.SprintFilter{Status: status, ProjectID: projectID})
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
		return
	}
	includeArchived, qerr := queryBool(q, "include_archived")
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	overBudget, qerr := queryBool(q, "over_budget")
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	min, qerr := queryFloatPtr(q, "cost_budget_min")
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	max, qerr := queryFloatPtr(q, "cost_budget_max")
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	cursor, qerr := queryCursor(q)
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	filter, normalized, err := service.NormalizeSprintQuery(service.SprintQuery{
		Status:          status,
		ProjectID:       projectID,
		IncludeArchived: includeArchived,
		OverBudget:      overBudget,
		CostBudgetMin:   min,
		CostBudgetMax:   max,
		CursorQuery:     cursor,
	})
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	sprints, err := s.svc.Sprint.List(filter)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeAdjacentServiceError(w, err)
		return
	}
	hasMore := len(sprints) > normalized.Limit
	if hasMore {
		sprints = sprints[:normalized.Limit]
	}
	if sprints == nil {
		sprints = []sqlstore.SprintRecord{}
	}
	nextCursor := ""
	if hasMore && len(sprints) > 0 {
		last := sprints[len(sprints)-1]
		nextCursor = pagination.Encode(normalized.SortBy, normalized.SortDir, service.SprintQuerySortValue(last, normalized.SortBy), last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": sprintsJSON(sprints), "meta": advancedMeta(normalized.Limit, normalized.SortBy, normalized.SortDir, hasMore, nextCursor, len(sprints))})
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

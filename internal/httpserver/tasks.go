package httpserver

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// taskJSON converts a TaskRecord to a JSON-friendly map with snake_case keys
// and proper null handling for sql.Null* types. Tags are passed in so the
// caller can batch-load them rather than requiring a store handle here.
func taskJSON(t *sqlstore.TaskRecord, tags []sqlstore.TagRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":             t.ID,
		"title":          t.Title,
		"description":    t.Description,
		"status":         t.Status,
		"priority":       t.Priority,
		"tags":           tagsJSON(tags),
		"manual":         t.Manual,
		"executor":       t.Executor,
		"agent_profile":  t.AgentProfile,
		"working_dir":    t.WorkingDir,
		"system_prompt":  t.SystemPrompt,
		"cost_budget":    nullFloat(t.CostBudget),
		"max_retries":    t.MaxRetries,
		"on_done":        t.OnDone,
		"on_fail":        t.OnFail,
		"on_review":      t.OnReview,
		"on_done_merge":  t.OnDoneMerge,
		"blocked_reason": t.BlockedReason,
		"sprint_id":      nullStr(t.SprintID),
		"project_id":     nullStr(t.ProjectID),
		"epic_id":        nullStr(t.EpicID),
		"created_at":     t.CreatedAt,
		"updated_at":     t.UpdatedAt,
	}
}

// tasksJSON converts a slice of TaskRecord to a JSON-friendly slice.
// Loads linked tags per-task (N+1 — acceptable at current scale).
func (s *Server) tasksJSON(tasks []sqlstore.TaskRecord) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, len(tasks))
	for i := range tasks {
		tags, err := s.svc.Task.ListTags(tasks[i].ID)
		if err != nil {
			return nil, err
		}
		out[i] = taskJSON(&tasks[i], tags)
	}
	return out, nil
}

func nullStr(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return nil
}

func nullFloat(nf sql.NullFloat64) interface{} {
	if nf.Valid {
		return nf.Float64
	}
	return nil
}

func nullInt(ni sql.NullInt64) interface{} {
	if ni.Valid {
		return ni.Int64
	}
	return nil
}

func nullTime(nt sql.NullTime) interface{} {
	if nt.Valid {
		return nt.Time
	}
	return nil
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	filter := sqlstore.TaskFilter{}
	if v := r.URL.Query().Get("status"); v != "" {
		parts := strings.Split(v, ",")
		if len(parts) == 1 {
			filter.Status = parts[0]
		} else {
			filter.Statuses = parts
		}
	}
	if v := r.URL.Query().Get("priority"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			filter.Priority = p
		}
	}
	if v := r.URL.Query().Get("sprint_id"); v != "" {
		filter.SprintID = v
	}
	if v := r.URL.Query().Get("project_id"); v != "" {
		filter.ProjectID = v
	}
	if v := r.URL.Query().Get("epic_id"); v != "" {
		filter.EpicID = v
	}
	if v := r.URL.Query().Get("executor"); v != "" {
		filter.Executor = v
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Offset = n
		}
	}

	tasks, err := s.svc.Task.List(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []sqlstore.TaskRecord{}
	}
	out, err := s.tasksJSON(tasks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out})
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskJSON(task, tags))
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Priority    int      `json:"priority"`
		Tags        []string `json:"tags"`
		Executor    string   `json:"executor"`
		Manual      bool     `json:"manual"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	task, err := s.svc.Task.Create(service.TaskCreateInput{
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		Tags:        req.Tags,
		Executor:    req.Executor,
		Manual:      req.Manual,
	})
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("task.created", map[string]interface{}{"task_id": task.ID, "title": task.Title})
	writeJSON(w, http.StatusCreated, taskJSON(task, tags))
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req map[string]interface{}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	update := sqlstore.TaskUpdate{}
	if v, ok := req["title"].(string); ok {
		update.Title = &v
	}
	if v, ok := req["description"].(string); ok {
		update.Description = &v
	}
	if v, ok := req["priority"].(float64); ok {
		p := int(v)
		update.Priority = &p
	}

	input := service.TaskUpdateInput{TaskUpdate: update}
	if raw, ok := req["tags"]; ok {
		if arr, ok := raw.([]interface{}); ok {
			slugs := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					slugs = append(slugs, s)
				}
			}
			input.Tags = &slugs
		}
	}

	if err := s.svc.Task.Update(id, input); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("task.updated", map[string]interface{}{"task_id": id})
	writeJSON(w, http.StatusOK, taskJSON(task, tags))
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Task.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) transitionTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := s.svc.Task.Transition(id, req.Status); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("task.transitioned", map[string]interface{}{"task_id": id, "status": req.Status})
	writeJSON(w, http.StatusOK, taskJSON(task, tags))
}

func (s *Server) bulkTransitionTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []string `json:"ids"`
		Status string   `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	updated, _ := s.svc.Task.BulkTransition(req.IDs, req.Status)
	for _, id := range req.IDs[:updated] {
		s.sse.Broadcast("task.transitioned", map[string]interface{}{"task_id": id, "status": req.Status})
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

func (s *Server) searchTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}
	tasks, err := s.svc.Task.Search(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []sqlstore.TaskRecord{}
	}
	out, err := s.tasksJSON(tasks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out})
}

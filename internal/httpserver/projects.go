package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

func projectJSON(p *sqlstore.ProjectRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":          p.ID,
		"name":        p.Name,
		"description": p.Description,
		"repo_path":   p.RepoPath,
		"status":      p.Status,
		"icon":        p.Icon,
		"created_at":  p.CreatedAt,
		"updated_at":  p.UpdatedAt,
	}
}

func projectsJSON(projects []sqlstore.ProjectRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(projects))
	for i := range projects {
		out[i] = projectJSON(&projects[i])
	}
	return out
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	projects, err := s.svc.Project.List(status)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if projects == nil {
		projects = []sqlstore.ProjectRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"projects": projectsJSON(projects)})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := s.svc.Project.Get(id)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projectJSON(project))
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		RepoPath    string `json:"repo_path"`
		Icon        string `json:"icon"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	project, err := s.svc.Project.Create(service.ProjectCreateInput{
		Name:        req.Name,
		Description: req.Description,
		RepoPath:    req.RepoPath,
		Icon:        req.Icon,
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

	s.sse.Broadcast("project.created", map[string]interface{}{"project_id": project.ID, "name": project.Name})
	writeJSON(w, http.StatusCreated, projectJSON(project))
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req map[string]interface{}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	update := sqlstore.ProjectUpdate{}
	if v, ok := req["name"].(string); ok {
		update.Name = &v
	}
	if v, ok := req["description"].(string); ok {
		update.Description = &v
	}
	if v, ok := req["repo_path"].(string); ok {
		update.RepoPath = &v
	}
	if v, ok := req["status"].(string); ok {
		update.Status = &v
	}
	if v, ok := req["icon"].(string); ok {
		update.Icon = &v
	}

	if err := s.svc.Project.Update(id, update); err != nil {
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

	project, err := s.svc.Project.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("project.updated", map[string]interface{}{"project_id": id})
	writeJSON(w, http.StatusOK, projectJSON(project))
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Project.Delete(id); err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("project.deleted", map[string]interface{}{"project_id": id})
	w.WriteHeader(http.StatusNoContent)
}

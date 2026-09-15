package httpserver

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

func projectJSON(p *sqlstore.ProjectRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":            p.ID,
		"name":          p.Name,
		"description":   p.Description,
		"repo_path":     p.RepoPath,
		"agent_path":    p.AgentPath,
		"read_paths":    parseStringArray(p.ReadPaths),
		"write_paths":   parseStringArray(p.WritePaths),
		"context_paths": parseStringArray(p.ContextPaths),
		"permissions":   parseStringMap(p.Permissions),
		"rules":         parseStringArray(p.Rules),
		"status":        p.Status,
		"icon":          p.Icon,
		"created_at":    p.CreatedAt,
		"updated_at":    p.UpdatedAt,
	}
}

func projectArtifactJSON(a *sqlstore.ProjectArtifactRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":          a.ID,
		"project_id":  a.ProjectID,
		"entry_type":  a.EntryType,
		"title":       a.Title,
		"description": a.Description,
		"file_path":   a.FilePath,
		"url":         a.URL,
		"content":     a.Content,
		"permissions": parseStringMap(a.Permissions),
		"rules":       parseStringArray(a.Rules),
		"metadata":    parseFreeMap(a.Metadata),
		"created_at":  a.CreatedAt,
		"updated_at":  a.UpdatedAt,
	}
}

func projectArtifactsJSON(items []sqlstore.ProjectArtifactRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(items))
	for i := range items {
		out[i] = projectArtifactJSON(&items[i])
	}
	return out
}

func projectsJSON(projects []sqlstore.ProjectRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(projects))
	for i := range projects {
		out[i] = projectJSON(&projects[i])
	}
	return out
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	allowed := map[string]bool{"status": true, "include_archived": true, "limit": true, "cursor": true, "sort_by": true, "sort_dir": true}
	q, qerr := parseStrictQuery(r, allowed)
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	status := queryString(q, "status")
	includeArchived, qerr := queryBool(q, "include_archived")
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	if !hasAnyQueryKey(q, "limit", "cursor", "sort_by", "sort_dir") {
		projects, err := s.svc.Project.List(status, includeArchived)
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
		return
	}
	cursor, qerr := queryCursor(q)
	if qerr != nil {
		writeHTTPQueryError(w, qerr)
		return
	}
	filter, normalized, err := service.NormalizeProjectQuery(service.ProjectQuery{
		Status:          status,
		IncludeArchived: includeArchived,
		CursorQuery:     cursor,
	})
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	projects, err := s.svc.Project.ListPage(filter)
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	hasMore := len(projects) > normalized.Limit
	if hasMore {
		projects = projects[:normalized.Limit]
	}
	if projects == nil {
		projects = []sqlstore.ProjectRecord{}
	}
	nextCursor := ""
	if hasMore && len(projects) > 0 {
		last := projects[len(projects)-1]
		nextCursor = pagination.Encode(normalized.SortBy, normalized.SortDir, service.ProjectQuerySortValue(last, normalized.SortBy), last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": projectsJSON(projects), "meta": advancedMeta(normalized.Limit, normalized.SortBy, normalized.SortDir, hasMore, nextCursor, len(projects))})
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
		Name         string            `json:"name"`
		Description  string            `json:"description"`
		RepoPath     string            `json:"repo_path"`
		AgentPath    string            `json:"agent_path"`
		ReadPaths    []string          `json:"read_paths"`
		WritePaths   []string          `json:"write_paths"`
		ContextPaths []string          `json:"context_paths"`
		Permissions  map[string]string `json:"permissions"`
		Rules        []string          `json:"rules"`
		Icon         string            `json:"icon"`
		Status       *string           `json:"status,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	project, err := s.svc.Project.Create(service.ProjectCreateInput{
		Name:         req.Name,
		Description:  req.Description,
		RepoPath:     req.RepoPath,
		AgentPath:    req.AgentPath,
		ReadPaths:    req.ReadPaths,
		WritePaths:   req.WritePaths,
		ContextPaths: req.ContextPaths,
		Permissions:  req.Permissions,
		Rules:        req.Rules,
		Icon:         req.Icon,
		Status:       req.Status,
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
	var req struct {
		Name         *string            `json:"name,omitempty"`
		Description  *string            `json:"description,omitempty"`
		RepoPath     *string            `json:"repo_path,omitempty"`
		AgentPath    *string            `json:"agent_path,omitempty"`
		ReadPaths    *[]string          `json:"read_paths,omitempty"`
		WritePaths   *[]string          `json:"write_paths,omitempty"`
		ContextPaths *[]string          `json:"context_paths,omitempty"`
		Permissions  *map[string]string `json:"permissions,omitempty"`
		Rules        *[]string          `json:"rules,omitempty"`
		Status       *string            `json:"status,omitempty"`
		Icon         *string            `json:"icon,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	update := sqlstore.ProjectUpdate{}
	update.Name = req.Name
	update.Description = req.Description
	update.RepoPath = req.RepoPath
	update.AgentPath = req.AgentPath
	update.Status = req.Status
	update.Icon = req.Icon
	if req.ReadPaths != nil {
		update.ReadPaths = nullJSONString(*req.ReadPaths)
	}
	if req.WritePaths != nil {
		update.WritePaths = nullJSONString(*req.WritePaths)
	}
	if req.ContextPaths != nil {
		update.ContextPaths = nullJSONString(*req.ContextPaths)
	}
	if req.Permissions != nil {
		update.Permissions = nullJSONString(*req.Permissions)
	}
	if req.Rules != nil {
		update.Rules = nullJSONString(*req.Rules)
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

func (s *Server) listProjectArtifacts(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	items, err := s.svc.Project.ListArtifacts(projectID)
	if err != nil {
		if _, ok := err.(*service.FeatureDisabledError); ok {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"artifacts": projectArtifactsJSON(items)})
}

func (s *Server) createProjectArtifact(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	var req struct {
		EntryType   string            `json:"entry_type"`
		Title       string            `json:"title"`
		Description string            `json:"description"`
		FilePath    string            `json:"file_path"`
		URL         string            `json:"url"`
		Content     string            `json:"content"`
		Permissions map[string]string `json:"permissions"`
		Rules       []string          `json:"rules"`
		Metadata    map[string]any    `json:"metadata"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	item, err := s.svc.Project.CreateArtifact(projectID, service.ProjectArtifactCreateInput{
		EntryType:   req.EntryType,
		Title:       req.Title,
		Description: req.Description,
		FilePath:    req.FilePath,
		URL:         req.URL,
		Content:     req.Content,
		Permissions: req.Permissions,
		Rules:       req.Rules,
		Metadata:    req.Metadata,
	})
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("project.updated", map[string]interface{}{"project_id": projectID})
	writeJSON(w, http.StatusCreated, projectArtifactJSON(item))
}

func (s *Server) updateProjectArtifact(w http.ResponseWriter, r *http.Request) {
	rawID := chi.URLParam(r, "artifactID")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid artifact id")
		return
	}
	var req struct {
		EntryType   *string            `json:"entry_type,omitempty"`
		Title       *string            `json:"title,omitempty"`
		Description *string            `json:"description,omitempty"`
		FilePath    *string            `json:"file_path,omitempty"`
		URL         *string            `json:"url,omitempty"`
		Content     *string            `json:"content,omitempty"`
		Permissions *map[string]string `json:"permissions,omitempty"`
		Rules       *[]string          `json:"rules,omitempty"`
		Metadata    *map[string]any    `json:"metadata,omitempty"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	update := sqlstore.ProjectArtifactUpdate{
		EntryType:   req.EntryType,
		Title:       req.Title,
		Description: req.Description,
		FilePath:    req.FilePath,
		URL:         req.URL,
		Content:     req.Content,
	}
	if req.Permissions != nil {
		update.Permissions = nullJSONString(*req.Permissions)
	}
	if req.Rules != nil {
		update.Rules = nullJSONString(*req.Rules)
	}
	if req.Metadata != nil {
		update.Metadata = nullJSONString(*req.Metadata)
	}
	if err := s.svc.Project.UpdateArtifact(id, update); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	item, err := s.svc.Project.GetArtifact(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("project.updated", map[string]interface{}{"project_id": item.ProjectID})
	writeJSON(w, http.StatusOK, projectArtifactJSON(item))
}

func (s *Server) deleteProjectArtifact(w http.ResponseWriter, r *http.Request) {
	rawID := chi.URLParam(r, "artifactID")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid artifact id")
		return
	}
	projectID := chi.URLParam(r, "id")
	if err := s.svc.Project.DeleteArtifact(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("project.updated", map[string]interface{}{"project_id": projectID})
	w.WriteHeader(http.StatusNoContent)
}

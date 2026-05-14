package httpserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

type IssueCreateRequest struct {
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	Context   string `json:"context,omitempty"`
	Issue     string `json:"issue,omitempty"`
	Details   string `json:"details,omitempty"`
	ProjectID string `json:"project_id"`
}

type IssueUpdateRequest struct {
	Title     *string `json:"title,omitempty"`
	Body      *string `json:"body,omitempty"`
	Context   *string `json:"context,omitempty"`
	Issue     *string `json:"issue,omitempty"`
	Details   *string `json:"details,omitempty"`
	ProjectID *string `json:"project_id,omitempty"`
}

func issueBody(req IssueCreateRequest) string {
	switch {
	case req.Body != "":
		return req.Body
	case req.Context != "":
		return req.Context
	case req.Issue != "":
		return req.Issue
	default:
		return req.Details
	}
}

func issueBodyUpdate(req IssueUpdateRequest) *string {
	switch {
	case req.Body != nil:
		return req.Body
	case req.Context != nil:
		return req.Context
	case req.Issue != nil:
		return req.Issue
	default:
		return req.Details
	}
}

func (s *Server) issueJSON(task *sqlstore.TaskRecord) (map[string]interface{}, error) {
	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		return nil, err
	}
	agg, err := s.svc.Run.Aggregate(task.ID)
	if err != nil {
		return nil, err
	}
	subs, err := s.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		return nil, err
	}
	out := taskJSON(task, tags, agg, subs, s.collectionNameForTask(task))
	out["body"] = task.Description
	return out, nil
}

func (s *Server) issuesJSON(tasks []sqlstore.TaskRecord) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, len(tasks))
	for i := range tasks {
		item, err := s.issueJSON(&tasks[i])
		if err != nil {
			return nil, err
		}
		out[i] = item
	}
	return out, nil
}

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	issues, err := s.svc.Issue.List(r.URL.Query().Get("project_id"))
	if err != nil {
		writeIssueError(w, err)
		return
	}
	if issues == nil {
		issues = []sqlstore.TaskRecord{}
	}
	out, err := s.issuesJSON(issues)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"issues": out, "total": len(out)})
}

func (s *Server) searchIssues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	issues, err := s.svc.Issue.Search(q, r.URL.Query().Get("project_id"), limit)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	if issues == nil {
		issues = []sqlstore.TaskRecord{}
	}
	out, err := s.issuesJSON(issues)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"issues": out, "total": len(out)})
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	issue, err := s.svc.Issue.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeIssueError(w, err)
		return
	}
	out, err := s.issueJSON(issue)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createIssue(w http.ResponseWriter, r *http.Request) {
	var req IssueCreateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	issue, err := s.svc.Issue.Create(service.IssueCreateInput{
		Title:     req.Title,
		Body:      issueBody(req),
		ProjectID: req.ProjectID,
	})
	if err != nil {
		writeIssueError(w, err)
		return
	}
	out, err := s.issueJSON(issue)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("issue.created", map[string]interface{}{"issue_id": issue.ID, "title": issue.Title})
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateIssue(w http.ResponseWriter, r *http.Request) {
	var req IssueUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.svc.Issue.Update(id, service.IssueUpdateInput{
		Title:     req.Title,
		Body:      issueBodyUpdate(req),
		ProjectID: req.ProjectID,
	}); err != nil {
		writeIssueError(w, err)
		return
	}
	issue, err := s.svc.Issue.Get(id)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	out, err := s.issueJSON(issue)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("issue.updated", map[string]interface{}{"issue_id": id})
	writeJSON(w, http.StatusOK, out)
}

func writeIssueError(w http.ResponseWriter, err error) {
	var validation *service.ValidationError
	if errors.As(err, &validation) {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var disabled *service.FeatureDisabledError
	if errors.As(err, &disabled) {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if errors.Is(err, sqlstore.ErrTaskNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

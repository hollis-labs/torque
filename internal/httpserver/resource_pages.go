package httpserver

import (
	"net/http"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

func (s *Server) serveResourceList(w http.ResponseWriter, r *http.Request, resource string, f sqlstore.ResourcePageFilter) {
	allowed := map[string]bool{"limit": true, "cursor": true, "offset": true, "sort_by": true, "sort_dir": true, "include_total": true, "search": true}
	switch resource {
	case "plans", "plan_children", "collection_tasks", "collection_inbox":
		for _, k := range []string{"status", "priority", "project_id", "sprint_id", "epic_id", "tags"} {
			allowed[k] = true
		}
		if resource == "plan_children" {
			allowed["phase_id"] = true
		}
	case "sessions":
		for _, k := range []string{"state", "task_id", "project_id"} {
			allowed[k] = true
		}
	case "artifacts":
		for _, k := range []string{"task_id", "type", "run_id"} {
			allowed[k] = true
		}
	case "collections", "checkpoints":
		allowed["status"] = true
	case "templates":
		allowed["kind"] = true
		allowed["include_archived"] = true
	}
	q, err := parseStrictQuery(r, allowed)
	if err != nil {
		writeHTTPQueryError(w, err)
		return
	}
	cursor, err := queryCursor(q)
	if err != nil {
		writeHTTPQueryError(w, err)
		return
	}
	query := service.ResourceQuery{CursorQuery: cursor}
	if _, ok := q["offset"]; ok {
		v, e := queryInt(q, "offset")
		if e != nil {
			writeHTTPQueryError(w, e)
			return
		}
		query.Offset = &v
	}
	f.TagSlugs = strings.Split(queryString(q, "tags"), ",")
	f.Search = queryString(q, "search")
	f.Status = queryString(q, "status")
	if resource == "sessions" {
		f.Status = queryString(q, "state")
	}
	if f.TaskID == "" {
		f.TaskID = queryString(q, "task_id")
	}
	f.ProjectID = queryString(q, "project_id")
	f.SprintID = queryString(q, "sprint_id")
	f.EpicID = queryString(q, "epic_id")
	f.PhaseID = queryString(q, "phase_id")
	f.Kind = queryString(q, "kind")
	f.Type = queryString(q, "type")
	f.RunID = queryString(q, "run_id")
	if _, ok := q["priority"]; ok {
		v, e := queryInt(q, "priority")
		if e != nil {
			writeHTTPQueryError(w, e)
			return
		}
		f.Priority = &v
	}
	f.IncludeArchived, err = queryBool(q, "include_archived")
	if err != nil {
		writeHTTPQueryError(w, err)
		return
	}
	page, e := s.svc.ListResourcePage(resource, f, query)
	if e != nil {
		writeAdjacentServiceError(w, e)
		return
	}
	hasMore := len(page.Rows) > page.Query.Limit
	if hasMore {
		page.Rows = page.Rows[:page.Query.Limit]
	}
	items := make([]any, 0, len(page.Rows))
	for _, row := range page.Rows {
		var item any = row.Record
		switch v := row.Record.(type) {
		case sqlstore.TaskRecord:
			out, err := s.tasksJSON([]sqlstore.TaskRecord{v})
			if err != nil {
				writeError(w, 500, err.Error())
				return
			}
			item = out[0]
		case sqlstore.CollectionRecord:
			item = collectionJSON(&v)
		case sqlstore.TemplateRecord:
			item = templateJSON(&v)
		case sqlstore.CheckpointRecord:
			item = checkpointJSON(&v, s.requiredWorkflowPolicyForTask(v.TaskID))
		case *sqlstore.SessionRecord:
			item = agent.SessionSnapshot(v)
		}
		items = append(items, item)
	}
	next := ""
	if hasMore && len(page.Rows) > 0 {
		last := page.Rows[len(page.Rows)-1]
		next = pagination.Encode(page.Query.SortBy, page.Query.SortDir, last.SortValue, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "meta": advancedPageMeta(page.Query.Limit, page.Query.SortBy, page.Query.SortDir, hasMore, next, len(items), page.Total, page.Offset)})
}

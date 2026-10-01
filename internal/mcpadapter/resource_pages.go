package mcpadapter

import (
	"context"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/service"
)

func withResourcePageParams(resource string) toolOpt {
	return func(t *toolSpec) {
		fields, by, dir := service.ResourceSortPolicy(resource)
		withString("limit", desc("Page size (default 50, max 200)"))(t)
		withString("cursor", desc("Opaque continuation; omit for first page"))(t)
		withString("offset", desc("Optional offset mode; mutually exclusive with cursor"))(t)
		withString("sort_by", desc("Allowed sort fields: "+joinResourceFields(fields)+"; default "+by))(t)
		withString("sort_dir", desc("asc or desc; default "+dir))(t)
		withString("include_total", desc("Opt-in filtered cohort total, excluding cursor and page bounds"))(t)
		withString("search", desc("Server-side text search"))(t)
		switch resource {
		case "plans", "plan_children", "collection_tasks", "collection_inbox":
			for _, key := range []string{"status", "priority", "project_id", "sprint_id", "epic_id"} {
				withString(key, desc("Filter task rows by "+key))(t)
			}
			withString("tags", desc("JSON array of tag slugs; every listed tag must match"))(t)
			withString("verbose", desc("Return full task records instead of brief"))(t)
		case "artifacts":
			withString("type", desc("Artifact type"))(t)
			withString("run_id", desc("Owning run ID"))(t)
		case "checkpoints":
			withString("status", desc("Checkpoint status"))(t)
		}

	}
}
func joinResourceFields(fields []string) string {
	out := ""
	for _, s := range fields {
		if out != "" {
			out += ", "
		}
		out += s
	}
	return out
}

func reqResourceQuery(req map[string]any) (service.ResourceQuery, error) {
	withoutOffset := make(map[string]any, len(req))
	for k, v := range req {
		if k != "offset" {
			withoutOffset[k] = v
		}
	}
	c, err := reqQueryCursor(withoutOffset)
	if err != nil {
		return service.ResourceQuery{}, err
	}
	q := service.ResourceQuery{CursorQuery: c}
	if _, ok := req["offset"]; ok {
		v, e := reqQueryInt(req, "offset")
		if e != nil {
			return q, e
		}
		q.Offset = &v
	}
	return q, nil
}

func (a *Adapter) handleResourceList(ctx context.Context, req map[string]any, resource string, f sqlstore.ResourcePageFilter) (any, error) {
	query, err := reqResourceQuery(req)
	if err != nil {
		return nil, err
	}
	scopeKeys := map[string]*string{}
	switch resource {
	case "artifacts", "checkpoints":
		scopeKeys["task_id"] = &f.TaskID
	case "plan_children":
		scopeKeys["plan_id"] = &f.ParentID
	case "collection_tasks":
		scopeKeys["collection_id"] = &f.CollectionID
	case "session_checkpoints":
		scopeKeys["session_id"] = &f.SessionID
	}
	for key, target := range scopeKeys {
		v, e := reqQueryString(req, key)
		if e != nil {
			return nil, e
		}
		*target = v
	}

	for key, target := range map[string]*string{"search": &f.Search, "status": &f.Status, "project_id": &f.ProjectID, "sprint_id": &f.SprintID, "epic_id": &f.EpicID, "phase_id": &f.PhaseID, "kind": &f.Kind, "type": &f.Type, "run_id": &f.RunID} {
		v, e := reqQueryString(req, key)
		if e != nil {
			return nil, e
		}
		*target = v
	}
	if resource == "sessions" {
		f.Status, err = reqQueryString(req, "state")
		if err != nil {
			return nil, err
		}
		f.TaskID, err = reqQueryString(req, "task_id")
		if err != nil {
			return nil, err
		}
	}
	if _, ok := req["priority"]; ok {
		v, e := reqQueryInt(req, "priority")
		if e != nil {
			return nil, e
		}
		f.Priority = &v
	}
	f.IncludeArchived, err = reqQueryBool(req, "include_archived")
	if err != nil {
		return nil, err
	}
	f.TagSlugs, err = reqStrSlice(req, "tags")
	if err != nil {
		return errResult(ErrCodeArgInvalid, err.Error(), "tags")
	}
	page, err := a.svc.ListResourcePage(resource, f, query)
	if err != nil {
		return errFromService(err)
	}
	hasMore := len(page.Rows) > page.Query.Limit
	if hasMore {
		page.Rows = page.Rows[:page.Query.Limit]
	}
	items := make([]any, 0, len(page.Rows))
	verbose := reqStrBool(req, "verbose")
	for _, row := range page.Rows {
		item := row.Record
		switch v := row.Record.(type) {
		case sqlstore.TaskRecord:
			if verbose {
				tags, e := a.svc.Task.ListTags(v.ID)
				if e != nil {
					return errFromService(e)
				}
				deps, e := a.svc.Task.ListDependencyIDs(v.ID)
				if e != nil {
					return errFromService(e)
				}
				if tags == nil {
					tags = []sqlstore.TagRecord{}
				}
				if deps == nil {
					deps = []string{}
				}
				item = taskWithTags{TaskRecord: &v, Tags: tags, DependsOn: deps}
			} else {
				item = toBriefTask(v, briefTagSlugs(a.svc, v.ID))
			}
		case sqlstore.ArtifactRecord:
			if !verbose {
				item = toBriefArtifact(v)
			}
		case sqlstore.TemplateRecord:
			if !verbose {
				item = toBriefTemplate(v)
			}
		case sqlstore.CheckpointRecord:
			if !verbose {
				item = toBriefCheckpoint(v)
			}
		case *sqlstore.SessionRecord:
			item = agent.SessionSnapshot(v)
		}
		items = append(items, item)
	}
	offset := []int{}
	if page.Offset != nil {
		offset = append(offset, *page.Offset)
	}
	return cappedCursorJSONResultWithTotal(items, page.Query.Limit, page.Total, page.Query.SortBy, page.Query.SortDir, hasMore, func(i int) (string, string) { return page.Rows[i].SortValue, page.Rows[i].ID }, offset...)
}

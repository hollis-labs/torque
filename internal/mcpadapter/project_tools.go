package mcpadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/mark3labs/mcp-go/mcp"
)

// projectSortAllowList is torque_project_list's sort_by allow-list
// (PRIM-002), per pagination.ValidateSortBy's doc comment ("Epic/Sprint/
// Project: name, status, updated_at, created_at").
var projectSortAllowList = []string{"name", "status", "updated_at", "created_at"}

// projectSortDefaultBy/projectSortDefaultDir are torque_project_list's
// default sort_by/sort_dir when the caller omits both — "name ASC" matches
// the tool's own historical/documented default order (FIX-004), so a
// caller that never touches sort_by/sort_dir sees the same order as
// before cursor pagination landed.
const (
	projectSortDefaultBy  = "name"
	projectSortDefaultDir = "asc"
)

func (a *Adapter) registerProjectTools() {
	a.addTool(mcp.NewTool("torque_project_create",
		mcp.WithDescription(`Create a project (feature-flagged: requires features.projects). Returns the ProjectRecord.
Use to group long-lived work by repo/app; sprints scope short-cycle execution, epics scope multi-sprint initiatives.
repo_path must resolve to an existing directory (~ is expanded) — a missing path returns error.code=arg_invalid, field=repo_path, so stale metadata can never be created.
Response shape: data = {<ProjectRecord fields>} — singleton.
Example: {"name":"Torque","repo_path":"/Users/me/Projects/torque"}`),
		mcp.WithString("name", mcp.Required(), mcp.Description("Project name")),
		mcp.WithString("description", mcp.Description("Project description")),
		mcp.WithString("repo_path", mcp.Description("Repository path — absolute or ~-prefixed; must point at an existing directory")),
		mcp.WithString("agent_path", mcp.Description("Path to an agent spec file for this project, relative to repo_path or absolute")),
		mcp.WithString("icon", mcp.Description("Icon identifier/name for UI display")),
		mcp.WithString("read_paths", mcp.Description("JSON array of paths this project's agents may read")),
		mcp.WithString("write_paths", mcp.Description("JSON array of paths this project's agents may write")),
		mcp.WithString("context_paths", mcp.Description("JSON array of paths providing background context")),
		mcp.WithString("permissions", mcp.Description("JSON object of permission key/value pairs")),
		mcp.WithString("rules", mcp.Description("JSON array of rule strings agents must follow in this project")),
	), a.handleProjectCreate)

	a.addTool(mcp.NewTool("torque_project_get",
		mcp.WithDescription(`Fetch a project's full record by ID.
Use when you know the ID; torque_project_list for browsing, torque_task_list with project_id filter for the project's task set.
Response shape: data = {<ProjectRecord fields>} — singleton.
Example: {"id":"PRJ-20260820-0001"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Project ID")),
	), a.handleProjectGet)

	a.addTool(mcp.NewTool("torque_project_update",
		mcp.WithDescription(`True partial patch of a project's fields; only keys present in the payload change (presence-in-payload, not value-based — an explicit empty string clears a scalar; an explicit empty array/object clears a JSON column). Omitted keys are left untouched.
Use for edits, active<->inactive transitions, or attaching an agent spec/paths/permissions. Use torque_project_archive/unarchive for soft-delete, which is orthogonal to status.
Response shape: data = {id, updated: bool, message}.
Example: {"id":"PRJ-20260820-0001","status":"inactive"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Project ID")),
		mcp.WithString("name", mcp.Description("New name")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("repo_path", mcp.Description("New repo_path — must point at an existing directory; cannot be cleared to empty")),
		mcp.WithString("agent_path", mcp.Description("New agent_path; empty string clears")),
		mcp.WithString("icon", mcp.Description("New icon; empty string clears")),
		mcp.WithString("status", mcp.Description("New status: active|inactive")),
		mcp.WithString("read_paths", mcp.Description("JSON array replacing the read_paths set; empty array/string clears")),
		mcp.WithString("write_paths", mcp.Description("JSON array replacing the write_paths set; empty array/string clears")),
		mcp.WithString("context_paths", mcp.Description("JSON array replacing the context_paths set; empty array/string clears")),
		mcp.WithString("permissions", mcp.Description("JSON object replacing the permissions map; empty string clears")),
		mcp.WithString("rules", mcp.Description("JSON array replacing the rules set; empty array/string clears")),
	), a.handleProjectUpdate)

	a.addTool(mcp.NewTool("torque_project_list",
		mcp.WithDescription(`List projects with optional status filter; ordered name ASC (tiebreak id ASC) by default. Pass sort_by (name|status|updated_at|created_at) and sort_dir (asc|desc) to change order; an unrecognized value returns error.code=arg_invalid.
Use for project discovery; torque_project_get when you know the ID, torque_task_list with project_id filter for the project's task set. Default brief shape; pass verbose="true" for full records.
Cursor pagination: pass the previous call's meta.next_cursor back as cursor to fetch the next page; meta.next_cursor is null once exhausted. A cursor is only valid for the exact sort_by/sort_dir it was issued under.
Response shape: data = {items: [<briefProject or ProjectRecord>...], meta: {truncated, returned, limit, has_more, next_cursor}}.
Example: {"status":"active","limit":"50"}`),
		mcp.WithString("status", mcp.Description("Filter: active|inactive")),
		mcp.WithString("include_archived", mcp.Description("Include archived projects (default false, string 'true'/'false')")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 100, max 500)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
		mcp.WithString("sort_by", mcp.Description("Sort field: name|status|updated_at|created_at (default name)")),
		mcp.WithString("sort_dir", mcp.Description("Sort direction: asc|desc (default asc)")),
		mcp.WithString("cursor", mcp.Description("Opaque pagination cursor from a previous call's meta.next_cursor; omit for the first page. Must match this call's sort_by/sort_dir.")),
	), a.handleProjectList)

	a.addTool(mcp.NewTool("torque_project_delete",
		mcp.WithDescription(`Hard-delete a project; linked tasks have project_id cleared but remain.
Use sparingly — prefer torque_project_archive for audit-preserving removal. Similar surfaces: torque_sprint_delete, torque_epic_delete.
Response shape: data = {id, deleted: true, message}.
Example: {"id":"PRJ-4"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Project ID")),
	), a.handleProjectDelete)

	a.addTool(mcp.NewTool("torque_project_archive",
		mcp.WithDescription(`Archive a project (soft-delete; preserves audit trail — the row and its history stay intact, just hidden from default list results).
Orthogonal to status — archiving a project is a separate fact from it being active/inactive; use torque_project_update status=inactive for the workflow-state change instead. Idempotent: archiving an already-archived project just refreshes the timestamp.
Use torque_project_unarchive to restore. Similar surfaces: torque_collection_archive, torque_template_archive.
Response shape: data = {id, archived: true, message}.
Example: {"id":"PRJ-20260820-0001"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Project ID")),
	), a.handleProjectArchive)

	a.addTool(mcp.NewTool("torque_project_unarchive",
		mcp.WithDescription(`Restore an archived project back to active (in the archive sense only — status is untouched, so an unarchived project keeps whatever active/inactive value it had before archiving).
Use after torque_project_archive to reverse an accidental or premature archive; the project reappears in torque_project_list's default (include_archived=false) results.
Similar surfaces: torque_collection_unarchive.
Response shape: data = {id, unarchived: true, message}.
Example: {"id":"PRJ-20260820-0001"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Project ID")),
	), a.handleProjectUnarchive)
}

// projectStrSliceArg reads a JSON-array-of-strings argument (create-time
// shape: tolerates the raw-JSON-string, []string, or post-sanitize []any
// forms via reqStrSlice, matching the tags/depends_on convention) and
// returns (nil result, nil error) when absent/empty so callers can `if v
// != nil { input.Field = v }` without a separate presence check.
func projectStrSliceArg(req mcp.CallToolRequest, key string) ([]string, *mcp.CallToolResult) {
	vals, err := reqStrSlice(req, key)
	if err != nil {
		res, _ := errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid %s JSON: %v", key, err), key)
		return nil, res
	}
	return vals, nil
}

// projectPermissionsMapArg parses a JSON-object-of-strings argument for
// torque_project_create's permissions field into a map[string]string.
func projectPermissionsMapArg(req mcp.CallToolRequest, key string) (map[string]string, *mcp.CallToolResult) {
	raw := reqStr(req, key)
	if raw == "" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		res, _ := errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid %s JSON: %v", key, err), key)
		return nil, res
	}
	return m, nil
}

func (a *Adapter) handleProjectCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	input := service.ProjectCreateInput{
		Name:        reqStr(req, "name"),
		Description: reqStr(req, "description"),
		RepoPath:    reqStr(req, "repo_path"),
		AgentPath:   reqStr(req, "agent_path"),
		Icon:        reqStr(req, "icon"),
	}

	for _, f := range []struct {
		key string
		set func([]string)
	}{
		{"read_paths", func(v []string) { input.ReadPaths = v }},
		{"write_paths", func(v []string) { input.WritePaths = v }},
		{"context_paths", func(v []string) { input.ContextPaths = v }},
		{"rules", func(v []string) { input.Rules = v }},
	} {
		v, errRes := projectStrSliceArg(req, f.key)
		if errRes != nil {
			return errRes, nil
		}
		if v != nil {
			f.set(v)
		}
	}

	perms, errRes := projectPermissionsMapArg(req, "permissions")
	if errRes != nil {
		return errRes, nil
	}
	if perms != nil {
		input.Permissions = perms
	}

	project, err := a.svc.Project.Create(input)
	if err != nil {
		return errFromService(err)
	}
	return okResult(project)
}

func (a *Adapter) handleProjectGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project, err := a.svc.Project.Get(reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(project)
}

// projectPathsUpdateArg builds the *sql.NullString for a presence-checked
// path/rules array field (read_paths/write_paths/context_paths/rules) on
// torque_project_update. Caller must already have confirmed the key is
// present in the payload — presence is the "change this" signal (true
// partial-patch semantics); an empty array/string clears the column
// (Valid:false), a non-empty array re-marshals to canonical JSON.
func projectPathsUpdateArg(req mcp.CallToolRequest, key string) (sql.NullString, *mcp.CallToolResult) {
	vals, err := reqStrSlice(req, key)
	if err != nil {
		res, _ := errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid %s JSON: %v", key, err), key)
		return sql.NullString{}, res
	}
	if len(vals) == 0 {
		return sql.NullString{Valid: false}, nil
	}
	b, _ := json.Marshal(vals)
	return sql.NullString{String: string(b), Valid: true}, nil
}

// projectPermissionsUpdateArg builds the *sql.NullString for
// torque_project_update's permissions field. Matches Task's metadata/
// environment/permissions convention (buildTaskUpdateInput): a JSON-encoded
// object string, stored as-is once validated; empty string clears.
func projectPermissionsUpdateArg(req mcp.CallToolRequest) (sql.NullString, *mcp.CallToolResult) {
	raw := reqStr(req, "permissions")
	if raw == "" {
		return sql.NullString{Valid: false}, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		res, _ := errResult(ErrCodeArgInvalid, "invalid permissions JSON: "+err.Error(), "permissions")
		return sql.NullString{}, res
	}
	return sql.NullString{String: raw, Valid: true}, nil
}

func (a *Adapter) handleProjectUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	args := req.GetArguments()
	update := sqlstore.ProjectUpdate{}
	hasUpdate := false

	// Scalar fields: presence in args is the trigger (true partial-patch
	// semantics per ENT-PROJECT scope) — an explicit empty string clears
	// name/description/agent_path/icon; status is validated downstream by
	// ProjectService.Update, repo_path rejects an explicit empty string
	// there too (repo_path can never be cleared, only changed).
	for _, f := range []struct {
		key string
		set func(*string)
	}{
		{"name", func(v *string) { update.Name = v }},
		{"description", func(v *string) { update.Description = v }},
		{"repo_path", func(v *string) { update.RepoPath = v }},
		{"agent_path", func(v *string) { update.AgentPath = v }},
		{"icon", func(v *string) { update.Icon = v }},
		{"status", func(v *string) { update.Status = v }},
	} {
		if _, ok := args[f.key]; ok {
			v := reqStr(req, f.key)
			f.set(&v)
			hasUpdate = true
		}
	}

	for _, f := range []struct {
		key string
		set func(*sql.NullString)
	}{
		{"read_paths", func(v *sql.NullString) { update.ReadPaths = v }},
		{"write_paths", func(v *sql.NullString) { update.WritePaths = v }},
		{"context_paths", func(v *sql.NullString) { update.ContextPaths = v }},
		{"rules", func(v *sql.NullString) { update.Rules = v }},
	} {
		if _, ok := args[f.key]; ok {
			ns, errRes := projectPathsUpdateArg(req, f.key)
			if errRes != nil {
				return errRes, nil
			}
			f.set(&ns)
			hasUpdate = true
		}
	}

	if _, ok := args["permissions"]; ok {
		ns, errRes := projectPermissionsUpdateArg(req)
		if errRes != nil {
			return errRes, nil
		}
		update.Permissions = &ns
		hasUpdate = true
	}

	if !hasUpdate {
		return okResult(map[string]any{
			"id":      id,
			"updated": false,
			"message": "No changes specified",
		})
	}

	if err := a.svc.Project.Update(id, update); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"updated": true,
		"message": fmt.Sprintf("Project %s updated", id),
	})
}

// projectSortValue formats a ProjectRecord's sortBy column into the string
// encoding PRIM-001's cursor uses for meta.next_cursor. Mirrors
// taskSortValue; every Project sort column is string-typed so there's no
// integer-formatting case to handle.
func projectSortValue(p sqlstore.ProjectRecord, sortBy string) string {
	switch sortBy {
	case "name":
		return p.Name
	case "status":
		return p.Status
	case "updated_at":
		return p.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	case "created_at":
		return p.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	default:
		return ""
	}
}

func (a *Adapter) handleProjectList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := clampLimit(reqInt(req, "limit"), defaultGenericListLimit, maxGenericListLimit)
	verbose := reqStrBool(req, "verbose")
	includeArchived := reqStrBool(req, "include_archived")

	// PRIM-002: sort_by/sort_dir, allow-list validated. Omitted values fall
	// back to name/asc (this tool's historical default order).
	sortBy := projectSortDefaultBy
	if raw := reqStr(req, "sort_by"); raw != "" {
		v, err := pagination.ValidateSortBy(raw, projectSortAllowList...)
		if err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "sort_by")
		}
		sortBy = v
	}
	sortDir := projectSortDefaultDir
	if raw := reqStr(req, "sort_dir"); raw != "" {
		v, err := pagination.ValidateSortDir(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "sort_dir")
		}
		sortDir = v
	}

	// PRIM-001: decode + validate the incoming cursor, if any, against this
	// request's (now-resolved) sort_by/sort_dir. DEC-001: cursors aren't
	// portable across sort orders.
	var afterSortValue, afterID string
	if raw := reqStr(req, "cursor"); raw != "" {
		c, err := pagination.Decode(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid cursor: %v", err), "cursor")
		}
		if err := c.Validate(sortBy, sortDir); err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "cursor")
		}
		afterSortValue, afterID = c.SortValue, c.ID
	}

	filter := sqlstore.ProjectFilter{
		Status:          reqStr(req, "status"),
		IncludeArchived: includeArchived,
		// Fetch one extra row beyond limit so has_more can be determined
		// without a separate COUNT(*) query (DEC-001's cheaper-default
		// choice); trimmed below before building the response envelope.
		Limit:          limit + 1,
		SortBy:         sortBy,
		SortDir:        sortDir,
		AfterSortValue: afterSortValue,
		AfterID:        afterID,
	}
	projects, err := a.svc.Project.ListPage(filter)
	if err != nil {
		return errFromService(err)
	}

	hasMoreFromQuery := len(projects) > limit
	if hasMoreFromQuery {
		projects = projects[:limit]
	}

	items := make([]any, 0, len(projects))
	for _, p := range projects {
		if verbose {
			items = append(items, p)
		} else {
			items = append(items, toBriefProject(p))
		}
	}

	cursorAt := func(i int) (sortValue, id string) {
		return projectSortValue(projects[i], sortBy), projects[i].ID
	}
	return cappedCursorJSONResult(items, limit, sortBy, sortDir, hasMoreFromQuery, cursorAt)
}

func (a *Adapter) handleProjectDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Project.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"deleted": true,
		"message": fmt.Sprintf("Project %s deleted", id),
	})
}

func (a *Adapter) handleProjectArchive(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Project.Archive(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":       id,
		"archived": true,
		"message":  fmt.Sprintf("Project %s archived", id),
	})
}

func (a *Adapter) handleProjectUnarchive(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Project.Unarchive(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":         id,
		"unarchived": true,
		"message":    fmt.Sprintf("Project %s unarchived", id),
	})
}

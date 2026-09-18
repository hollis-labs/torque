package mcpadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func (a *Adapter) registerProjectTools() {
	a.addTool(newTool("torque_project_create",
		withDescription(`Create a project (feature-flagged: requires features.projects). Returns the ProjectRecord.
Use to group long-lived work by repo/app; sprints scope short-cycle execution, epics scope multi-sprint initiatives.
repo_path must resolve to an existing directory (~ is expanded) — a missing path returns error.code=arg_invalid, field=repo_path, so stale metadata can never be created.
Response shape: data = {<ProjectRecord fields>} — singleton.
Example: {"name":"Torque","repo_path":"/Users/me/Projects/torque"}`),
		withString("name", required(), desc("Project name")),
		withString("description", desc("Project description")),
		withString("repo_path", desc("Repository path — absolute or ~-prefixed; must point at an existing directory")),
		withString("agent_path", desc("Path to an agent spec file for this project, relative to repo_path or absolute")),
		withString("icon", desc("Icon identifier/name for UI display")),
		withString("status", desc("Initial status: active|inactive (default active when omitted)")),
		withString("read_paths", desc("JSON array of paths this project's agents may read")),
		withString("write_paths", desc("JSON array of paths this project's agents may write")),
		withString("context_paths", desc("JSON array of paths providing background context")),
		withString("permissions", desc("JSON object of permission key/value pairs")),
		withString("rules", desc("JSON array of rule strings agents must follow in this project")),
	), a.handleProjectCreate)

	a.addTool(newTool("torque_project_get",
		withDescription(`Fetch a project's full record by ID.
Use when you know the ID; torque_project_list for browsing, torque_task_list with project_id filter for the project's task set.
Response shape: data = {<ProjectRecord fields>} — singleton.
Example: {"id":"PRJ-20260820-0001"}`),
		withString("id", required(), desc("Project ID")),
	), a.handleProjectGet)

	a.addTool(newTool("torque_project_update",
		withDescription(`True partial patch of a project's fields; only keys present in the payload change (presence-in-payload, not value-based — an explicit empty string clears a scalar; an explicit empty array/object clears a JSON column). Omitted keys are left untouched.
Use for edits, active<->inactive transitions, or attaching an agent spec/paths/permissions. Use torque_project_archive/unarchive for soft-delete, which is orthogonal to status.
Response shape: data = {id, updated: bool, message}.
Example: {"id":"PRJ-20260820-0001","status":"inactive"}`),
		withString("id", required(), desc("Project ID")),
		withString("name", desc("New name")),
		withString("description", desc("New description")),
		withString("repo_path", desc("New repo_path — must point at an existing directory; cannot be cleared to empty")),
		withString("agent_path", desc("New agent_path; empty string clears")),
		withString("icon", desc("New icon; empty string clears")),
		withString("status", desc("New status: active|inactive")),
		withString("read_paths", desc("JSON array replacing the read_paths set; empty array/string clears")),
		withString("write_paths", desc("JSON array replacing the write_paths set; empty array/string clears")),
		withString("context_paths", desc("JSON array replacing the context_paths set; empty array/string clears")),
		withString("permissions", desc("JSON object replacing the permissions map; empty string clears")),
		withString("rules", desc("JSON array replacing the rules set; empty array/string clears")),
	), a.handleProjectUpdate)

	a.addTool(newTool("torque_project_list",
		withDescription(`List projects with optional status filter; ordered name ASC (tiebreak id ASC) by default. Pass sort_by (name|status|updated_at|created_at) and sort_dir (asc|desc) to change order; an unrecognized value returns error.code=arg_invalid.
Use for project discovery; torque_project_get when you know the ID, torque_task_list with project_id filter for the project's task set. Default brief shape; pass verbose="true" for full records.
Cursor pagination: pass the previous call's meta.next_cursor back as cursor to fetch the next page; meta.next_cursor is null once exhausted. A cursor is only valid for the exact sort_by/sort_dir it was issued under.
Explicit malformed, blank, fractional, overflow, unsafe native-float, or negative limit values reject with error.code=arg_invalid, field=limit; omitted limit defaults to 100 and oversized limits clamp to 500.
Response shape: data = {items: [<briefProject or ProjectRecord>...], meta: {truncated, returned, limit, has_more, next_cursor}}.
Example: {"status":"active","limit":"50"}`),
		withString("status", desc("Filter: active|inactive")),
		withString("include_archived", desc("Include archived projects (default false, string 'true'/'false')")),
		withString("limit", desc("Max results (integer, default 100, max 500)")),
		withString("verbose", desc("Return full records instead of brief (string 'true'/'false', default false)")),
		withString("sort_by", desc("Sort field: name|status|updated_at|created_at (default name)")),
		withString("sort_dir", desc("Sort direction: asc|desc (default asc)")),
		withString("cursor", desc("Opaque pagination cursor from a previous call's meta.next_cursor; omit for the first page. Must match this call's sort_by/sort_dir.")),
	), a.handleProjectList)

	a.addTool(newTool("torque_project_delete",
		withDescription(`Hard-delete a project; linked tasks have project_id cleared but remain.
Use sparingly — prefer torque_project_archive for audit-preserving removal. Similar surfaces: torque_sprint_delete, torque_epic_delete.
Response shape: data = {id, deleted: true, message}.
Example: {"id":"PRJ-4"}`),
		withString("id", required(), desc("Project ID")),
	), a.handleProjectDelete)

	a.addTool(newTool("torque_project_archive",
		withDescription(`Archive a project (soft-delete; preserves audit trail — the row and its history stay intact, just hidden from default list results).
Orthogonal to status — archiving a project is a separate fact from it being active/inactive; use torque_project_update status=inactive for the workflow-state change instead. Idempotent: archiving an already-archived project just refreshes the timestamp.
Use torque_project_unarchive to restore. Similar surfaces: torque_collection_archive, torque_template_archive.
Response shape: data = {id, archived: true, message}.
Example: {"id":"PRJ-20260820-0001"}`),
		withString("id", required(), desc("Project ID")),
	), a.handleProjectArchive)

	a.addTool(newTool("torque_project_unarchive",
		withDescription(`Restore an archived project back to active (in the archive sense only — status is untouched, so an unarchived project keeps whatever active/inactive value it had before archiving).
Use after torque_project_archive to reverse an accidental or premature archive; the project reappears in torque_project_list's default (include_archived=false) results.
Similar surfaces: torque_collection_unarchive.
Response shape: data = {id, unarchived: true, message}.
Example: {"id":"PRJ-20260820-0001"}`),
		withString("id", required(), desc("Project ID")),
	), a.handleProjectUnarchive)
}

// projectStrSliceArg reads a JSON-array-of-strings argument (create-time
// shape: tolerates the raw-JSON-string, []string, or post-sanitize []any
// forms via reqStrSlice, matching the tags/depends_on convention) and
// returns (nil result, nil error) when absent/empty so callers can `if v
// != nil { input.Field = v }` without a separate presence check.
func projectStrSliceArg(req map[string]any, key string) ([]string, error) {
	vals, err := reqStrSlice(req, key)
	if err != nil {
		return nil, argError(ErrCodeArgInvalid, fmt.Sprintf("invalid %s JSON: %v", key, err), key)
	}
	return vals, nil
}

// projectPermissionsMapArg parses a JSON-object-of-strings argument for
// torque_project_create's permissions field into a map[string]string.
func projectPermissionsMapArg(req map[string]any, key string) (map[string]string, error) {
	raw := reqStr(req, key)
	if raw == "" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, argError(ErrCodeArgInvalid, fmt.Sprintf("invalid %s JSON: %v", key, err), key)
	}
	return m, nil
}

func (a *Adapter) handleProjectCreate(ctx context.Context, req map[string]any) (any, error) {
	input := service.ProjectCreateInput{
		Name:        reqStr(req, "name"),
		Description: reqStr(req, "description"),
		RepoPath:    reqStr(req, "repo_path"),
		AgentPath:   reqStr(req, "agent_path"),
		Icon:        reqStr(req, "icon"),
	}
	if reqHasArg(req, "status") {
		status := reqStr(req, "status")
		input.Status = &status
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
			return nil, errRes
		}
		if v != nil {
			f.set(v)
		}
	}

	perms, errRes := projectPermissionsMapArg(req, "permissions")
	if errRes != nil {
		return nil, errRes
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

func (a *Adapter) handleProjectGet(ctx context.Context, req map[string]any) (any, error) {
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
func projectPathsUpdateArg(req map[string]any, key string) (sql.NullString, error) {
	vals, err := reqStrSlice(req, key)
	if err != nil {
		return sql.NullString{}, argError(ErrCodeArgInvalid, fmt.Sprintf("invalid %s JSON: %v", key, err), key)
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
func projectPermissionsUpdateArg(req map[string]any) (sql.NullString, error) {
	raw := reqStr(req, "permissions")
	if raw == "" {
		return sql.NullString{Valid: false}, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return sql.NullString{}, argError(ErrCodeArgInvalid, "invalid permissions JSON: "+err.Error(), "permissions")
	}
	return sql.NullString{String: raw, Valid: true}, nil
}

func (a *Adapter) handleProjectUpdate(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	args := req
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
				return nil, errRes
			}
			f.set(&ns)
			hasUpdate = true
		}
	}

	if _, ok := args["permissions"]; ok {
		ns, errRes := projectPermissionsUpdateArg(req)
		if errRes != nil {
			return nil, errRes
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

func (a *Adapter) handleProjectList(ctx context.Context, req map[string]any) (any, error) {
	verbose, errRes := reqQueryBool(req, "verbose")
	if errRes != nil {
		return nil, errRes
	}
	status, errRes := reqQueryString(req, "status")
	if errRes != nil {
		return nil, errRes
	}
	includeArchived, errRes := reqQueryBool(req, "include_archived")
	if errRes != nil {
		return nil, errRes
	}
	cursor, errRes := reqQueryCursor(req)
	if errRes != nil {
		return nil, errRes
	}
	filter, normalized, err := service.NormalizeProjectQuery(service.ProjectQuery{
		Status:          status,
		IncludeArchived: includeArchived,
		CursorQuery:     cursor,
	})
	if err != nil {
		return errFromService(err)
	}
	projects, err := a.svc.Project.ListPage(filter)
	if err != nil {
		return errFromService(err)
	}

	hasMoreFromQuery := len(projects) > normalized.Limit
	if hasMoreFromQuery {
		projects = projects[:normalized.Limit]
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
		return service.ProjectQuerySortValue(projects[i], normalized.SortBy), projects[i].ID
	}
	return cappedCursorJSONResult(items, normalized.Limit, normalized.SortBy, normalized.SortDir, hasMoreFromQuery, cursorAt)
}

func (a *Adapter) handleProjectDelete(ctx context.Context, req map[string]any) (any, error) {
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

func (a *Adapter) handleProjectArchive(ctx context.Context, req map[string]any) (any, error) {
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

func (a *Adapter) handleProjectUnarchive(ctx context.Context, req map[string]any) (any, error) {
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

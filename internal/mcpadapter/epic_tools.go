package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerEpicTools() {
	a.addTool(mcp.NewTool("torque_epic_create",
		mcp.WithDescription(`Create an epic (feature-flagged: requires features.epics). Returns the EpicRecord.
Use to group multiple sprints under one multi-sprint initiative; sibling torque_sprint_create for short-cycle cohorts, torque_project_create for repo-level grouping.
Response shape: data = {<EpicRecord fields>} — singleton.
Example: {"name":"Auth Overhaul","description":"Replace entire auth stack"}`),
		mcp.WithString("name", mcp.Required(), mcp.Description("Epic name")),
		mcp.WithString("description", mcp.Description("Epic description")),
		mcp.WithString("project_id", mcp.Description("Project ID to associate this epic with (requires features.projects)")),
	), a.handleEpicCreate)

	a.addTool(mcp.NewTool("torque_epic_get",
		mcp.WithDescription(`Fetch an epic's full record by ID.
Use when you know the ID; torque_epic_list for browsing, torque_task_list with epic_id filter for the epic's task set.
Response shape: data = {<EpicRecord fields>} — singleton.
Example: {"id":"EP-4"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Epic ID")),
	), a.handleEpicGet)

	a.addTool(mcp.NewTool("torque_epic_update",
		mcp.WithDescription(`Partial update of epic fields or status.
Use for edits or active<->inactive transitions. No dedicated epic approval flow — close via status=inactive.
Response shape: data = {id, updated: bool, message}.
Example: {"id":"EP-4","status":"inactive"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Epic ID")),
		mcp.WithString("name", mcp.Description("New name")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("status", mcp.Description("New status: active|inactive")),
		mcp.WithString("project_id", mcp.Description("Project ID to associate this epic with (requires features.projects); pass empty string to clear")),
	), a.handleEpicUpdate)

	a.addTool(mcp.NewTool("torque_epic_delete",
		mcp.WithDescription(`Hard-delete an epic; linked tasks have epic_id cleared.
Prefer torque_epic_update status=inactive for audit. Similar surfaces: torque_sprint_delete, torque_project_delete.
Response shape: data = {id, deleted: true, message}.
Example: {"id":"EP-4"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Epic ID")),
	), a.handleEpicDelete)

	a.addTool(mcp.NewTool("torque_epic_list",
		mcp.WithDescription(`List epics, optionally filtered by status; ordered updated_at DESC.
Use for browsing; torque_epic_get when you know the ID. Default brief shape; pass verbose="true" for full records.
Response shape: data = {items: [<briefEpic or EpicRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"status":"active"}`),
		mcp.WithString("status", mcp.Description("Filter: active|inactive")),
		mcp.WithString("project_id", mcp.Description("Filter by project ID (requires features.projects)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleEpicList)
}

func (a *Adapter) handleEpicCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	input := service.EpicCreateInput{
		Name:        reqStr(req, "name"),
		Description: reqStr(req, "description"),
		ProjectID:   reqStr(req, "project_id"),
	}

	epic, err := a.svc.Epic.Create(input)
	if err != nil {
		return errFromService(err)
	}
	return okResult(epic)
}

func (a *Adapter) handleEpicGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	epic, err := a.svc.Epic.Get(reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(epic)
}

func (a *Adapter) handleEpicUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	input := service.EpicUpdateInput{}
	hasUpdate := false

	if v := reqStr(req, "name"); v != "" {
		input.Name = &v
		hasUpdate = true
	}
	if v := reqStr(req, "description"); v != "" {
		input.Description = &v
		hasUpdate = true
	}
	if v := reqStr(req, "status"); v != "" {
		input.Status = &v
		hasUpdate = true
	}
	if reqHasArg(req, "project_id") {
		v := reqStr(req, "project_id")
		input.ProjectID = &v
		hasUpdate = true
	}

	if !hasUpdate {
		return okResult(map[string]any{
			"id":      id,
			"updated": false,
			"message": "No changes specified",
		})
	}

	if err := a.svc.Epic.Update(id, input); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"updated": true,
		"message": fmt.Sprintf("Epic %s updated", id),
	})
}

func (a *Adapter) handleEpicDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Epic.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"deleted": true,
		"message": fmt.Sprintf("Epic %s deleted", id),
	})
}

func (a *Adapter) handleEpicList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose := reqStrBool(req, "verbose")
	epics, err := a.svc.Epic.List(reqStr(req, "status"), reqStr(req, "project_id"))
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(epics))
	for _, e := range epics {
		if verbose {
			items = append(items, e)
		} else {
			items = append(items, toBriefEpic(e))
		}
	}
	return cappedJSONResult(items, limit)
}

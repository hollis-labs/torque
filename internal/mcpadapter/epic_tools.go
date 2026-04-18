package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerEpicTools() {
	a.server.AddTool(mcp.NewTool("clockwork_epic_create",
		mcp.WithDescription("Create a new epic (requires features.epics = true)"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Epic name")),
		mcp.WithString("description", mcp.Description("Epic description")),
	), a.handleEpicCreate)

	a.server.AddTool(mcp.NewTool("clockwork_epic_get",
		mcp.WithDescription("Get an epic by ID"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Epic ID")),
	), a.handleEpicGet)

	a.server.AddTool(mcp.NewTool("clockwork_epic_update",
		mcp.WithDescription("Update epic fields"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Epic ID")),
		mcp.WithString("name", mcp.Description("New name")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("status", mcp.Description("New status: open, closed")),
	), a.handleEpicUpdate)

	a.server.AddTool(mcp.NewTool("clockwork_epic_delete",
		mcp.WithDescription("Delete an epic"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Epic ID")),
	), a.handleEpicDelete)

	a.server.AddTool(mcp.NewTool("clockwork_epic_list",
		mcp.WithDescription("List epics"),
		mcp.WithString("status", mcp.Description("Filter by status: open, closed")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleEpicList)
}

func (a *Adapter) handleEpicCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	input := service.EpicCreateInput{
		Name:        reqStr(req, "name"),
		Description: reqStr(req, "description"),
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

package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerProjectTools() {
	a.server.AddTool(mcp.NewTool("clockwork_project_create",
		mcp.WithDescription("Create a new project (requires features.projects = true)"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Project name")),
		mcp.WithString("description", mcp.Description("Project description")),
		mcp.WithString("repo_path", mcp.Description("Repository path for this project")),
	), a.handleProjectCreate)

	a.server.AddTool(mcp.NewTool("clockwork_project_list",
		mcp.WithDescription("List all projects"),
	), a.handleProjectList)

	a.server.AddTool(mcp.NewTool("clockwork_project_delete",
		mcp.WithDescription("Delete a project"),
		mcp.WithString("id", mcp.Required(), mcp.Description("Project ID")),
	), a.handleProjectDelete)
}

func (a *Adapter) handleProjectCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	input := service.ProjectCreateInput{
		Name:        reqStr(req, "name"),
		Description: reqStr(req, "description"),
		RepoPath:    reqStr(req, "repo_path"),
	}

	project, err := a.svc.Project.Create(input)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(project)
}

func (a *Adapter) handleProjectList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	projects, err := a.svc.Project.List("")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]interface{}{
		"projects": projects,
		"count":    len(projects),
	})
}

func (a *Adapter) handleProjectDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Project.Delete(id); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Project %s deleted", id)), nil
}

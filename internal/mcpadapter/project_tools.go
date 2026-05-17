package mcpadapter

import (
	"context"
	"fmt"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
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
	), a.handleProjectCreate)

	a.addTool(mcp.NewTool("torque_project_list",
		mcp.WithDescription(`List all projects; ordered updated_at DESC.
Use for project discovery; no dedicated project_get tool — torque_task_list with project_id filter exposes the project's task set. Default brief shape; pass verbose="true" for full records.
Response shape: data = {items: [<briefProject or ProjectRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {}`),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleProjectList)

	a.addTool(mcp.NewTool("torque_project_delete",
		mcp.WithDescription(`Hard-delete a project; linked tasks have project_id cleared but remain.
Use sparingly. Similar surfaces: torque_sprint_delete, torque_epic_delete.
Response shape: data = {id, deleted: true, message}.
Example: {"id":"PRJ-4"}`),
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
		return errFromService(err)
	}
	return okResult(project)
}

func (a *Adapter) handleProjectList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose := reqStrBool(req, "verbose")
	projects, err := a.svc.Project.List("")
	if err != nil {
		return errFromService(err)
	}
	limit := defaultGenericListLimit
	items := make([]any, 0, len(projects))
	for _, p := range projects {
		if verbose {
			items = append(items, p)
		} else {
			items = append(items, toBriefProject(p))
		}
	}
	return cappedJSONResult(items, limit)
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

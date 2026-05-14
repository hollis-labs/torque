package mcpadapter

import (
	"context"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerIssueTools() {
	a.addTool(mcp.NewTool("torque_issue_create",
		mcp.WithDescription(`Create a project-scoped issue as a kind=issue backlog task. Requires title, body/context/issue/details, and project_id.
Use for low-friction issue capture that should appear in task views but never auto-dispatch. Response shape: data = {<TaskRecord fields>, Body, Tags[]}.
Validation: project_id is required and must reference an enabled project.
Example: {"title":"Login error","body":"Users see 500 on callback","project_id":"PRJ-..."}`),
		mcp.WithString("title", mcp.Required(), mcp.Description("Issue title")),
		mcp.WithString("body", mcp.Description("Issue body/details/context")),
		mcp.WithString("context", mcp.Description("Alias for body")),
		mcp.WithString("issue", mcp.Description("Alias for body")),
		mcp.WithString("details", mcp.Description("Alias for body")),
		mcp.WithString("project_id", mcp.Required(), mcp.Description("Project ID (requires features.projects)")),
	), a.handleIssueCreate)

	a.addTool(mcp.NewTool("torque_issue_get",
		mcp.WithDescription(`Fetch one issue by ID. Rejects non-issue task IDs.
Response shape: data = {<TaskRecord fields>, Body, Tags[]}.
Use torque_task_get for generic task IDs that may not be issues.
Example: {"id":"CW-20260514-0001"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Issue task ID")),
	), a.handleIssueGet)

	a.addTool(mcp.NewTool("torque_issue_list",
		mcp.WithDescription(`List issues only (hard-scoped to kind=issue), optionally narrowed to one project_id.
Response shape: data = {items: [<briefTask or TaskRecord>...], meta: {truncated, returned, limit, hint?}}.
Use this instead of torque_task_list when the caller specifically wants issue capture rows.
Example: {"project_id":"PRJ-...","limit":"50"}`),
		mcp.WithString("project_id", mcp.Description("Optional project ID filter")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 50, max 200)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleIssueList)

	a.addTool(mcp.NewTool("torque_issue_search",
		mcp.WithDescription(`Search issues only (hard-scoped to kind=issue) across ID, title, and body/description.
Response shape: data = {items: [<briefTask or TaskRecord>...], meta: {truncated, returned, limit, hint?}}.
Filters combine with the query via AND, so project_id narrows results to one project.
Example: {"query":"login","project_id":"PRJ-...","limit":"10"}`),
		mcp.WithString("query", mcp.Required(), mcp.Description("Free-text search query")),
		mcp.WithString("project_id", mcp.Description("Optional project ID filter")),
		mcp.WithString("limit", mcp.Description("Max results (integer, default 25, max 100)")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handleIssueSearch)

	a.addTool(mcp.NewTool("torque_issue_update",
		mcp.WithDescription(`Update the minimal issue fields. Only supplied keys change; body/context/issue/details are aliases.
Response shape: data = {<TaskRecord fields>, Body, Tags[]}.
This refuses non-issue IDs so generic task rows cannot be edited through the issue surface.
Example: {"id":"CW-...","details":"New reproduction steps"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Issue task ID")),
		mcp.WithString("title", mcp.Description("New issue title")),
		mcp.WithString("body", mcp.Description("New issue body")),
		mcp.WithString("context", mcp.Description("Alias for body")),
		mcp.WithString("issue", mcp.Description("Alias for body")),
		mcp.WithString("details", mcp.Description("Alias for body")),
		mcp.WithString("project_id", mcp.Description("New project ID (requires features.projects)")),
	), a.handleIssueUpdate)
}

type issueWithTags struct {
	*sqlstore.TaskRecord
	Body string               `json:"Body"`
	Tags []sqlstore.TagRecord `json:"Tags"`
}

func (a *Adapter) issueResult(task *sqlstore.TaskRecord) (*mcp.CallToolResult, error) {
	tags, err := a.svc.Task.ListTags(task.ID)
	if err != nil {
		return errFromService(err)
	}
	if tags == nil {
		tags = []sqlstore.TagRecord{}
	}
	return okResult(issueWithTags{TaskRecord: task, Body: task.Description, Tags: tags})
}

func reqIssueBody(req mcp.CallToolRequest) string {
	for _, key := range []string{"body", "context", "issue", "details"} {
		if v := reqStr(req, key); v != "" {
			return v
		}
	}
	return ""
}

func reqIssueBodyUpdate(req mcp.CallToolRequest) *string {
	for _, key := range []string{"body", "context", "issue", "details"} {
		if reqHasArg(req, key) {
			v := reqStr(req, key)
			return &v
		}
	}
	return nil
}

func (a *Adapter) handleIssueCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	issue, err := a.svc.Issue.Create(service.IssueCreateInput{
		Title:     reqStr(req, "title"),
		Body:      reqIssueBody(req),
		ProjectID: reqStr(req, "project_id"),
	})
	if err != nil {
		return errFromService(err)
	}
	return a.issueResult(issue)
}

func (a *Adapter) handleIssueGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	issue, err := a.svc.Issue.Get(reqStr(req, "id"))
	if err != nil {
		return errFromService(err)
	}
	return a.issueResult(issue)
}

func (a *Adapter) handleIssueList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := clampLimit(reqInt(req, "limit"), 50, maxTaskListLimit)
	verbose := reqStrBool(req, "verbose")
	issues, err := a.svc.Issue.List(reqStr(req, "project_id"))
	if err != nil {
		return errFromService(err)
	}
	if len(issues) > limit {
		issues = issues[:limit]
	}
	return a.tasksToEnvelope(issues, limit, verbose)
}

func (a *Adapter) handleIssueSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := clampLimit(reqInt(req, "limit"), defaultTaskSearchLimit, maxTaskSearchLimit)
	verbose := reqStrBool(req, "verbose")
	issues, err := a.svc.Issue.Search(reqStr(req, "query"), reqStr(req, "project_id"), limit)
	if err != nil {
		return errFromService(err)
	}
	return a.tasksToEnvelope(issues, limit, verbose)
}

func (a *Adapter) handleIssueUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	input := service.IssueUpdateInput{Body: reqIssueBodyUpdate(req)}
	if reqHasArg(req, "title") {
		v := reqStr(req, "title")
		input.Title = &v
	}
	if reqHasArg(req, "project_id") {
		v := reqStr(req, "project_id")
		input.ProjectID = &v
	}
	if err := a.svc.Issue.Update(id, input); err != nil {
		return errFromService(err)
	}
	issue, err := a.svc.Issue.Get(id)
	if err != nil {
		return errFromService(err)
	}
	return a.issueResult(issue)
}

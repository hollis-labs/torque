package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerPlanTools exposes the PlanService convenience operations over MCP.
// Plan tasks are kind=plan; phases live at metadata.plan.phases[]; execution
// children link via parent_id + metadata.phase_id. See docs/plans-v1.md.
func (a *Adapter) registerPlanTools() {
	a.server.AddTool(mcp.NewTool("clockwork_plan_create",
		mcp.WithDescription("Create a plan (kind=plan task) with optional initial phases"),
		mcp.WithString("title", mcp.Required(), mcp.Description("Plan title")),
		mcp.WithString("description", mcp.Description("Plan description (free-form)")),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5 (default 2)")),
		mcp.WithString("project_id", mcp.Description("Project ID (requires features.projects)")),
		mcp.WithString("sprint_id", mcp.Description("Sprint ID (requires features.sprints)")),
		mcp.WithString("epic_id", mcp.Description("Epic ID (requires features.epics)")),
		mcp.WithString("phases", mcp.Description("JSON array of {name, acceptance?} phase specs; order follows array position")),
		mcp.WithString("tags", mcp.Description("JSON array of tag strings")),
	), a.handlePlanCreate)

	a.server.AddTool(mcp.NewTool("clockwork_plan_get",
		mcp.WithDescription("Fetch a plan with decoded phases + child roll-up"),
		mcp.WithString("plan_id", mcp.Required(), mcp.Description("Plan task ID (kind=plan)")),
	), a.handlePlanGet)

	a.server.AddTool(mcp.NewTool("clockwork_plan_add_phase",
		mcp.WithDescription("Append a phase to the plan's phases[]. Returns the new phase_id."),
		mcp.WithString("plan_id", mcp.Required(), mcp.Description("Plan task ID (kind=plan)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Phase name")),
		mcp.WithString("acceptance", mcp.Description("Optional acceptance prose")),
	), a.handlePlanAddPhase)

	a.server.AddTool(mcp.NewTool("clockwork_plan_remove_phase",
		mcp.WithDescription("Remove a phase from the plan. Rejected if any child task still references the phase."),
		mcp.WithString("plan_id", mcp.Required(), mcp.Description("Plan task ID")),
		mcp.WithString("phase_id", mcp.Required(), mcp.Description("Phase ID (e.g. ph-1)")),
	), a.handlePlanRemovePhase)

	a.server.AddTool(mcp.NewTool("clockwork_plan_list_children",
		mcp.WithDescription("List tasks whose parent_id matches the plan. Optional phase_id narrows by metadata.phase_id."),
		mcp.WithString("plan_id", mcp.Required(), mcp.Description("Plan task ID")),
		mcp.WithString("phase_id", mcp.Description("Optional phase_id filter")),
	), a.handlePlanListChildren)
}

func (a *Adapter) handlePlanCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	input := service.PlanCreateInput{
		Title:       reqStr(req, "title"),
		Description: reqStr(req, "description"),
		Priority:    reqInt(req, "priority"),
		ProjectID:   reqStr(req, "project_id"),
		SprintID:    reqStr(req, "sprint_id"),
		EpicID:      reqStr(req, "epic_id"),
	}
	if raw := reqStr(req, "phases"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.Phases); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid phases JSON: %v", err)), nil
		}
	}
	if raw := reqStr(req, "tags"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input.Tags); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid tags JSON: %v", err)), nil
		}
	}

	task, err := a.svc.Plan.Create(input)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	detail, err := a.svc.Plan.Get(task.ID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(detail)
}

func (a *Adapter) handlePlanGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	detail, err := a.svc.Plan.Get(reqStr(req, "plan_id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(detail)
}

func (a *Adapter) handlePlanAddPhase(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	phaseID, err := a.svc.Plan.AddPhase(
		reqStr(req, "plan_id"),
		reqStr(req, "name"),
		reqStr(req, "acceptance"),
	)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]string{"phase_id": phaseID})
}

func (a *Adapter) handlePlanRemovePhase(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := a.svc.Plan.RemovePhase(reqStr(req, "plan_id"), reqStr(req, "phase_id")); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("removed"), nil
}

func (a *Adapter) handlePlanListChildren(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	children, err := a.svc.Plan.ListChildren(reqStr(req, "plan_id"), reqStr(req, "phase_id"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(children)
}

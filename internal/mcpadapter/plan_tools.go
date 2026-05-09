package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hollis-labs/clockwork-manifold/internal/planstart"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// registerPlanTools exposes the PlanService convenience operations over MCP.
// Plan tasks are kind=plan; phases live at metadata.plan.phases[]; execution
// children link via parent_id + metadata.phase_id. See docs/plans-v1.md.
func (a *Adapter) registerPlanTools() {
	a.addTool(mcp.NewTool("clockwork_plan_create",
		mcp.WithDescription(`Create a plan (kind=plan task) with optional initial phases[]. Returns the plan task plus decoded phase detail.
Use for multi-phase work with explicit acceptance per phase; clockwork_task_create for single tasks, clockwork_template_create to codify a repeat plan shape.
Response shape: data = {<PlanDetail>: task fields + decoded phases[] + child roll-up}.
Example: {"title":"Auth refactor","phases":"[{\"name\":\"extract\",\"acceptance\":\"green tests\"},{\"name\":\"migrate\"}]"}`),
		mcp.WithString("title", mcp.Required(), mcp.Description("Plan title")),
		mcp.WithString("description", mcp.Description("Plan description (free-form)")),
		mcp.WithString("priority", mcp.Description("Priority 1-5 (integer, default 2)")),
		mcp.WithString("project_id", mcp.Description("Project ID (requires features.projects)")),
		mcp.WithString("sprint_id", mcp.Description("Sprint ID (requires features.sprints)")),
		mcp.WithString("epic_id", mcp.Description("Epic ID (requires features.epics)")),
		mcp.WithString("phases", mcp.Description("JSON array of {name, acceptance?} phase specs; order follows array position")),
		mcp.WithString("tags", mcp.Description("JSON array of tag strings")),
	), a.handlePlanCreate)

	a.addTool(mcp.NewTool("clockwork_plan_get",
		mcp.WithDescription(`Fetch a plan with decoded phases[] and a child task roll-up.
Use to inspect plan structure; clockwork_plan_list_children for per-phase child tasks, clockwork_task_get for non-plan tasks. plan_id is a task ID with kind=plan.
Response shape: data = {<PlanDetail>: task fields + phases[] + child roll-up}.
Example: {"plan_id":"T-999"}`),
		mcp.WithString("plan_id", mcp.Required(), mcp.Description("Plan task ID (kind=plan)")),
	), a.handlePlanGet)

	a.addTool(mcp.NewTool("clockwork_plan_add_phase",
		mcp.WithDescription(`Append a phase to a plan's phases[]; returns the assigned phase_id (e.g. ph-3).
Use to evolve a plan after creation; clockwork_plan_remove_phase to drop (refused if children still reference it), clockwork_plan_get to see the full ordered list.
Response shape: data = {phase_id}.
Example: {"plan_id":"T-999","name":"verify","acceptance":"user sign-off"}`),
		mcp.WithString("plan_id", mcp.Required(), mcp.Description("Plan task ID (kind=plan)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Phase name")),
		mcp.WithString("acceptance", mcp.Description("Optional acceptance prose")),
	), a.handlePlanAddPhase)

	a.addTool(mcp.NewTool("clockwork_plan_remove_phase",
		mcp.WithDescription(`Remove a phase from a plan. Rejected with error.code=conflict if any child task still references the phase via metadata.phase_id.
Use to prune phases; clockwork_plan_add_phase to append, clockwork_plan_list_children to see what references a phase.
Response shape: data = {plan_id, phase_id, removed: true}.
Example: {"plan_id":"T-999","phase_id":"ph-2"}`),
		mcp.WithString("plan_id", mcp.Required(), mcp.Description("Plan task ID")),
		mcp.WithString("phase_id", mcp.Required(), mcp.Description("Phase ID (e.g. ph-1)")),
	), a.handlePlanRemovePhase)

	a.addTool(mcp.NewTool("clockwork_plan_list_children",
		mcp.WithDescription(`List tasks whose parent_id matches the plan; phase_id narrows to children of one phase via metadata.phase_id.
Use to inspect per-phase execution tasks; clockwork_task_list with parent_id filter is the lower-level analog. Reuses the task list envelope (brief/verbose).
Response shape: data = {items: [<briefTask or TaskRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"plan_id":"T-999","phase_id":"ph-1"}`),
		mcp.WithString("plan_id", mcp.Required(), mcp.Description("Plan task ID")),
		mcp.WithString("phase_id", mcp.Description("Optional phase_id filter")),
		mcp.WithString("verbose", mcp.Description("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handlePlanListChildren)

	a.addTool(mcp.NewTool("clockwork_plan_start",
		mcp.WithDescription(`Boot an Orchestrator session for a kind=plan task and transition the plan to doing. Returns {session_id, plan_id, started_at}.
Idempotent: if an Orchestrator session is already running for this plan, returns the existing session_id with error.code=conflict so callers can route to the live session view rather than retry.
Plan must be kind=plan and status in {todo, review} — done/blocked/abandoned plans are not re-runnable in V0. Workdir defaults to the plan task's WorkingDir column when omitted.
Response shape: data = {session_id, plan_id, started_at}. On 409: error contains the existing session_id token.
Example: {"plan_id":"T-PLAN-1","workdir":"/tmp/plan"}`),
		mcp.WithString("plan_id", mcp.Required(), mcp.Description("Plan task ID (kind=plan)")),
		mcp.WithString("workdir", mcp.Description("Orchestrator session workdir; defaults to plan.WorkingDir")),
	), a.handlePlanStart)
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
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid phases JSON: %v", err), "phases")
		}
	}
	if tags, err := reqStrSlice(req, "tags"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
	} else if tags != nil {
		input.Tags = tags
	}

	task, err := a.svc.Plan.Create(input)
	if err != nil {
		return errFromService(err)
	}
	detail, err := a.svc.Plan.Get(task.ID)
	if err != nil {
		return errFromService(err)
	}
	return okResult(detail)
}

func (a *Adapter) handlePlanGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	detail, err := a.svc.Plan.Get(reqStr(req, "plan_id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(detail)
}

func (a *Adapter) handlePlanAddPhase(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	phaseID, err := a.svc.Plan.AddPhase(
		reqStr(req, "plan_id"),
		reqStr(req, "name"),
		reqStr(req, "acceptance"),
	)
	if err != nil {
		return errFromService(err)
	}
	return okResult(map[string]string{"phase_id": phaseID})
}

func (a *Adapter) handlePlanRemovePhase(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	planID := reqStr(req, "plan_id")
	phaseID := reqStr(req, "phase_id")
	if err := a.svc.Plan.RemovePhase(planID, phaseID); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"plan_id":  planID,
		"phase_id": phaseID,
		"removed":  true,
	})
}

func (a *Adapter) handlePlanListChildren(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	verbose := reqStrBool(req, "verbose")
	children, err := a.svc.Plan.ListChildren(reqStr(req, "plan_id"), reqStr(req, "phase_id"))
	if err != nil {
		return errFromService(err)
	}
	// Reuse the task envelope: plan children are just tasks.
	return a.tasksToEnvelope(children, maxTaskListLimit, verbose)
}

// handlePlanStart boots an Orchestrator session for a kind=plan task
// (CW-20260503-0017, S2.1). Errors map to dual-surface MCP results:
//   - ErrAlreadyOrchestrating → conflict (existing session_id surfaced)
//   - ErrPlanNotFound / ErrPlanWrongStatus → arg_invalid
//   - ErrSessionMgrMissing → domain (substrate not wired in this host)
func (a *Adapter) handlePlanStart(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if a.sessions == nil {
		return errResult(ErrCodeDomain, planstart.ErrSessionMgrMissing.Error(), "")
	}
	planID := reqStr(req, "plan_id")
	if planID == "" {
		return errResult(ErrCodeArgInvalid, "plan_id is required", "plan_id")
	}
	res, err := planstart.Start(ctx, a.svc.Store(), a.sessions, planID, planstart.Options{
		Workdir: reqStr(req, "workdir"),
	})
	if err != nil {
		switch {
		case errors.Is(err, planstart.ErrAlreadyOrchestrating):
			// AC5 idempotency: surface as code=conflict (matches the
			// tool's documented contract) and embed the live session id
			// in the message so callers can route to the running view.
			// errResult's envelope doesn't carry structured details, so
			// the session_id rides in the message text — agents grep
			// for "session_id=" to pull it out, GUI shows it verbatim.
			msg := err.Error()
			if res != nil && res.SessionID != "" {
				msg = msg + " (session_id=" + res.SessionID + ")"
			}
			return errResult(ErrCodeConflict, msg, "")
		case errors.Is(err, planstart.ErrPlanNotFound), errors.Is(err, planstart.ErrPlanWrongStatus):
			return errResult(ErrCodeArgInvalid, err.Error(), "plan_id")
		case errors.Is(err, planstart.ErrWorkdirRequired):
			return errResult(ErrCodeArgInvalid, err.Error(), "workdir")
		case errors.Is(err, planstart.ErrSessionMgrMissing):
			return errResult(ErrCodeDomain, err.Error(), "")
		default:
			return errResult(ErrCodeDomain, err.Error(), "")
		}
	}
	return okResult(res)
}

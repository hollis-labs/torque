package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/planstart"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

// planSortAllowList is torque_plan_list's sort_by allow-list (PRIM-002).
// Identical to Task's (taskSortAllowList) since plans are Task rows with the
// same sortable columns — kept as its own var rather than sharing Task's so
// Plan's list surface doesn't silently change if Task's allow-list evolves.
var planSortAllowList = []string{"priority", "status", "updated_at", "created_at"}

// planSortDefaultBy/planSortDefaultDir are torque_plan_list's default
// sort_by/sort_dir when the caller omits both — mirrors taskSortDefaultBy/
// Dir (see task_tools.go) so the two dedicated list tools behave
// consistently for callers that don't specify an order.
const (
	planSortDefaultBy  = "priority"
	planSortDefaultDir = "asc"
)

// registerPlanTools exposes the PlanService convenience operations over MCP.
// Plan tasks are kind=plan; phases live at metadata.plan.phases[]; execution
// children link via parent_id + metadata.phase_id. See docs/plans-v1.md.
func (a *Adapter) registerPlanTools() {
	a.addTool(newTool("torque_plan_create",
		withDescription(`Create a plan (kind=plan task) with optional initial phases[]. Returns the plan task plus decoded phase detail.
Use for multi-phase work with explicit acceptance per phase; torque_task_create for single tasks, torque_template_create to codify a repeat plan shape.
Response shape: data = {<PlanDetail>: task fields + decoded phases[] + child roll-up}.
Example: {"title":"Auth refactor","phases":"[{\"name\":\"extract\",\"acceptance\":\"green tests\"},{\"name\":\"migrate\"}]"}`),
		withString("title", required(), desc("Plan title")),
		withString("description", desc("Plan description (free-form)")),
		withString("priority", desc("Priority 1-5 (integer, default 2). A non-integer value returns error.code=arg_invalid; it is not coerced to the default.")),
		withString("project_id", desc("Project ID (requires features.projects)")),
		withString("sprint_id", desc("Sprint ID (requires features.sprints)")),
		withString("epic_id", desc("Epic ID (requires features.epics)")),
		withString("phases", desc("JSON array of {name, acceptance?} phase specs; order follows array position")),
		withString("tags", desc("JSON array of tag strings")),
	), a.handlePlanCreate)

	a.addTool(newTool("torque_plan_get",
		withDescription(`Fetch a plan with decoded phases[] and a child task roll-up.
Use to inspect plan structure; torque_plan_list_children for per-phase child tasks, torque_task_get for non-plan tasks. plan_id is a task ID with kind=plan.
Response shape: data = {<PlanDetail>: task fields + phases[] + child roll-up}.
Example: {"plan_id":"T-999"}`),
		withString("plan_id", required(), desc("Plan task ID (kind=plan)")),
	), a.handlePlanGet)

	a.addTool(newTool("torque_plan_list",
		withDescription(`List plans only (hard-scoped to kind=plan) — the dedicated analog to torque_task_list kind=plan. Ordered priority ASC (tiebreak id ASC) by default. Pass sort_by (priority|status|updated_at|created_at) and sort_dir (asc|desc) to change order; an unrecognized value returns error.code=arg_invalid.
Cursor pagination: pass the previous call's meta.next_cursor back as cursor to fetch the next page; meta.next_cursor is null once exhausted. A cursor is only valid for the exact sort_by/sort_dir it was issued under.
Response shape: data = {items: [<briefTask or TaskRecord>...], meta: {truncated, returned, limit, has_more, next_cursor}}.
Example: {"status":"todo","limit":"25","sort_by":"updated_at","sort_dir":"desc"}`),
		withString("status", desc("Filter by status")),
		withString("priority", desc("Filter by priority (integer 1-5)")),
		withString("project_id", desc("Filter by project ID (requires features.projects)")),
		withString("sprint_id", desc("Filter by sprint ID (requires features.sprints)")),
		withString("epic_id", desc("Filter by epic ID (requires features.epics)")),
		withString("tags", desc("JSON array of tag slugs — AND-match; plan must have all listed tags")),
		withString("search", desc("Substring match on title + description (case-insensitive)")),
		withString("limit", desc("Max results (integer, default 100, max 200)")),
		withString("verbose", desc("Return full records instead of brief (string 'true'/'false', default false)")),
		withString("sort_by", desc("Sort field: priority|status|updated_at|created_at (default priority)")),
		withString("sort_dir", desc("Sort direction: asc|desc (default asc)")),
		withString("cursor", desc("Opaque pagination cursor from a previous call's meta.next_cursor; omit for the first page. Must match this call's sort_by/sort_dir.")),
	), a.handlePlanList)

	a.addTool(newTool("torque_plan_update",
		withDescription(`Partial update of a plan's own fields (title/description/priority/project/sprint/epic/tags); only supplied keys change. Rejects non-plan task ids with error.code=arg_invalid. Returns the updated plan.
Use for editing plan metadata; torque_plan_add_phase/torque_plan_remove_phase for structural phase changes, torque_task_update for non-plan tasks.
Response shape: data = {<PlanDetail>: task fields + decoded phases[] + child roll-up}.
Example: {"plan_id":"T-999","title":"Auth refactor v2"}`),
		withString("plan_id", required(), desc("Plan task ID (kind=plan)")),
		withString("title", desc("New plan title (cannot be cleared to empty)")),
		withString("description", desc("New plan description")),
		withString("priority", desc("New priority (integer 1-5). A non-integer value returns error.code=arg_invalid and leaves the stored priority unchanged.")),
		withString("project_id", desc("Project ID (requires features.projects); empty string unassigns")),
		withString("sprint_id", desc("Sprint ID (requires features.sprints); empty string unassigns")),
		withString("epic_id", desc("Epic ID (requires features.epics); empty string unassigns")),
		withString("tags", desc("JSON array of tag names/slugs — replaces the full linked tag set")),
	), a.handlePlanUpdate)

	a.addTool(newTool("torque_plan_delete",
		withDescription(`Hard-delete a plan task row and its linkage (runs, artifacts, comments cascade). Rejects non-plan task ids with error.code=arg_invalid. Child tasks are NOT deleted — their parent_id is cleared (ON DELETE SET NULL).
Use sparingly — prefer torque_task_transition to "abandoned" for audit-preserving closure (reachable from any status in one call). For non-plan tasks use torque_task_delete.
Response shape: data = {id, deleted: true}.
Example: {"plan_id":"T-999"}`),
		withString("plan_id", required(), desc("Plan task ID (kind=plan)")),
	), a.handlePlanDelete)

	a.addTool(newTool("torque_plan_add_phase",
		withDescription(`Append a phase to a plan's phases[]; returns the assigned phase_id (e.g. ph-3).
Use to evolve a plan after creation; torque_plan_remove_phase to drop (refused if children still reference it), torque_plan_get to see the full ordered list.
Response shape: data = {phase_id}.
Example: {"plan_id":"T-999","name":"verify","acceptance":"user sign-off"}`),
		withString("plan_id", required(), desc("Plan task ID (kind=plan)")),
		withString("name", required(), desc("Phase name")),
		withString("acceptance", desc("Optional acceptance prose")),
	), a.handlePlanAddPhase)

	a.addTool(newTool("torque_plan_remove_phase",
		withDescription(`Remove a phase from a plan. Rejected with error.code=conflict if any child task still references the phase via metadata.phase_id.
Use to prune phases; torque_plan_add_phase to append, torque_plan_list_children to see what references a phase.
Response shape: data = {plan_id, phase_id, removed: true}.
Example: {"plan_id":"T-999","phase_id":"ph-2"}`),
		withString("plan_id", required(), desc("Plan task ID")),
		withString("phase_id", required(), desc("Phase ID (e.g. ph-1)")),
	), a.handlePlanRemovePhase)

	a.addTool(newTool("torque_plan_list_children",
		withDescription(`List tasks whose parent_id matches the plan; phase_id narrows to children of one phase via metadata.phase_id.
Use to inspect per-phase execution tasks; torque_task_list with parent_id filter is the lower-level analog. Reuses the task list envelope (brief/verbose).
Response shape: data = {items: [<briefTask or TaskRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"plan_id":"T-999","phase_id":"ph-1"}`),
		withString("plan_id", required(), desc("Plan task ID")),
		withString("phase_id", desc("Optional phase_id filter")),
		withString("limit", desc("Max results (integer, default 100, max 200)")),
		withString("verbose", desc("Return full records instead of brief (string 'true'/'false', default false)")),
	), a.handlePlanListChildren)

	a.addTool(newTool("torque_plan_start",
		withDescription(`Boot an Orchestrator session for a kind=plan task and transition the plan to doing. Returns {session_id, plan_id, started_at}.
Idempotent: if an Orchestrator session is already running for this plan, returns the existing session_id with error.code=conflict so callers can route to the live session view rather than retry.
Plan must be kind=plan and status in {todo, review} — done/blocked/abandoned plans are not re-runnable in V0. Workdir defaults to the plan task's WorkingDir column when omitted.
Response shape: data = {session_id, plan_id, started_at}. On 409: error contains the existing session_id token.
Example: {"plan_id":"T-PLAN-1","workdir":"/tmp/plan"}`),
		withString("plan_id", required(), desc("Plan task ID (kind=plan)")),
		withString("workdir", desc("Orchestrator session workdir; defaults to plan.WorkingDir")),
	), a.handlePlanStart)
}

func (a *Adapter) handlePlanCreate(ctx context.Context, req map[string]any) (any, error) {
	priority, _, errRes := reqPriorityArg(req)
	if errRes != nil {
		return nil, errRes
	}

	input := service.PlanCreateInput{
		Title:       reqStr(req, "title"),
		Description: reqStr(req, "description"),
		Priority:    priority,
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

func (a *Adapter) handlePlanGet(ctx context.Context, req map[string]any) (any, error) {
	detail, err := a.svc.Plan.Get(reqStr(req, "plan_id"))
	if err != nil {
		return errFromService(err)
	}
	return okResult(detail)
}

// handlePlanList is torque_plan_list's handler — PRIM-001/PRIM-002 applied
// to the dedicated plan list surface, mirroring handleTaskList's shape
// (task_tools.go) as closely as possible: same sort/cursor validation
// sequence, same limit+1 over-fetch to compute has_more without a COUNT(*),
// and the same taskListCursorEnvelope response builder (plans are Task rows,
// so the envelope needs no plan-specific variant).
func (a *Adapter) handlePlanList(ctx context.Context, req map[string]any) (any, error) {
	limit := clampLimit(reqInt(req, "limit"), defaultGenericListLimit, maxTaskListLimit)
	verbose := reqStrBool(req, "verbose")

	sortBy := planSortDefaultBy
	if raw := reqStr(req, "sort_by"); raw != "" {
		v, err := pagination.ValidateSortBy(raw, planSortAllowList...)
		if err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "sort_by")
		}
		sortBy = v
	}
	sortDir := planSortDefaultDir
	if raw := reqStr(req, "sort_dir"); raw != "" {
		v, err := pagination.ValidateSortDir(raw)
		if err != nil {
			return errResult(ErrCodeArgInvalid, err.Error(), "sort_dir")
		}
		sortDir = v
	}

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

	priorityFilter, _, errRes := reqPriorityArg(req)
	if errRes != nil {
		return nil, errRes
	}

	filter := sqlstore.TaskFilter{
		Status:    reqStr(req, "status"),
		Priority:  priorityFilter,
		ProjectID: reqStr(req, "project_id"),
		SprintID:  reqStr(req, "sprint_id"),
		EpicID:    reqStr(req, "epic_id"),
		Search:    reqStr(req, "search"),
		// Fetch one extra row beyond limit so has_more can be determined
		// without a separate COUNT(*) query (mirrors handleTaskList).
		Limit:          limit + 1,
		SortBy:         sortBy,
		SortDir:        sortDir,
		AfterSortValue: afterSortValue,
		AfterID:        afterID,
	}
	if tags, err := reqStrSlice(req, "tags"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
	} else if tags != nil {
		filter.TagSlugs = tags
	}

	// PlanService.List force-sets filter.Kind = "plan" regardless of
	// anything set here, so no explicit Kind field is needed above.
	plans, err := a.svc.Plan.List(filter)
	if err != nil {
		return errFromService(err)
	}

	hasMoreFromQuery := len(plans) > limit
	if hasMoreFromQuery {
		plans = plans[:limit]
	}
	return a.taskListCursorEnvelope(plans, limit, verbose, sortBy, sortDir, hasMoreFromQuery)
}

// handlePlanUpdate is torque_plan_update's handler. Presence-based (not
// zero-value-based) field detection via reqHasArg, so an explicit empty
// string clears a nullable association (project/sprint/epic) the same way
// torque_task_update does; title is validated non-empty downstream by
// TaskService.Update (mirrors Create's requirement).
func (a *Adapter) handlePlanUpdate(ctx context.Context, req map[string]any) (any, error) {
	planID := reqStr(req, "plan_id")
	input := service.PlanUpdateInput{}
	if reqHasArg(req, "title") {
		v := reqStr(req, "title")
		input.Title = &v
	}
	if reqHasArg(req, "description") {
		v := reqStr(req, "description")
		input.Description = &v
	}
	if priority, present, errRes := reqPriorityArg(req); present {
		if errRes != nil {
			return nil, errRes
		}
		input.Priority = &priority
	}
	if reqHasArg(req, "project_id") {
		v := reqStr(req, "project_id")
		input.ProjectID = &v
	}
	if reqHasArg(req, "sprint_id") {
		v := reqStr(req, "sprint_id")
		input.SprintID = &v
	}
	if reqHasArg(req, "epic_id") {
		v := reqStr(req, "epic_id")
		input.EpicID = &v
	}
	if reqHasArg(req, "tags") {
		tags, err := reqStrSlice(req, "tags")
		if err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
		}
		if tags != nil {
			input.Tags = &tags
		}
	}

	if err := a.svc.Plan.Update(planID, input); err != nil {
		return errFromService(err)
	}
	detail, err := a.svc.Plan.Get(planID)
	if err != nil {
		return errFromService(err)
	}
	return okResult(detail)
}

// handlePlanDelete is torque_plan_delete's handler. PlanService.Delete
// applies the kind guard (rejects non-plan task ids) before deleting.
func (a *Adapter) handlePlanDelete(ctx context.Context, req map[string]any) (any, error) {
	planID := reqStr(req, "plan_id")
	if err := a.svc.Plan.Delete(planID); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{"id": planID, "deleted": true})
}

func (a *Adapter) handlePlanAddPhase(ctx context.Context, req map[string]any) (any, error) {
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

func (a *Adapter) handlePlanRemovePhase(ctx context.Context, req map[string]any) (any, error) {
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

func (a *Adapter) handlePlanListChildren(ctx context.Context, req map[string]any) (any, error) {
	// limit was previously hardcoded to maxTaskListLimit (200, the system
	// maximum) regardless of caller intent, with no actual truncation
	// applied — tasksToEnvelope/cappedJSONResult don't slice items down to
	// limit themselves (see handleIssueList for the established truncate-
	// before-envelope pattern), so every call silently returned the FULL
	// unbounded child set with meta.limit just stamped at 200. Now a real,
	// caller-adjustable param with a sane default (matches the other
	// generic list tools' default, response.go's defaultGenericListLimit).
	limit := clampLimit(reqInt(req, "limit"), defaultGenericListLimit, maxTaskListLimit)
	verbose := reqStrBool(req, "verbose")
	children, err := a.svc.Plan.ListChildren(reqStr(req, "plan_id"), reqStr(req, "phase_id"))
	if err != nil {
		return errFromService(err)
	}
	if len(children) > limit {
		children = children[:limit]
	}
	// Reuse the task envelope: plan children are just tasks.
	return a.tasksToEnvelope(children, limit, verbose)
}

// handlePlanStart boots an Orchestrator session for a kind=plan task
// (CW-20260503-0017, S2.1). Errors map to dual-surface MCP results:
//   - ErrAlreadyOrchestrating → conflict (existing session_id surfaced)
//   - ErrPlanNotFound / ErrPlanWrongStatus → arg_invalid
//   - ErrSessionMgrMissing → domain (substrate not wired in this host)
func (a *Adapter) handlePlanStart(ctx context.Context, req map[string]any) (any, error) {
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

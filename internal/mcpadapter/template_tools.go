package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/torque/internal/service"
)

func (a *Adapter) registerTemplateTools() {
	a.addTool(newTool("torque_template_create",
		withDescription(`Create a task template at version=1; subsequent torque_template_update calls append new versions.
Use to encode repeated task shapes with {{var}} placeholders and required_vars; torque_task_create_from_template instantiates. torque_task_create is the ad-hoc alternative.
Response shape: data = {<TemplateRecord fields>} — singleton.
Example: {"id":"backend-fix","name":"Backend Fix","description":"Fix {{issue}}","kind":"agent","executor":"cli","required_vars":"[\"issue\"]"}`),
		withString("id", required(), desc("Template id (stable across versions)")),
		withString("name", required(), desc("Human-readable name")),
		withString("description", required(), desc("Template description (supports {{var}})")),
		withString("kind", required(), desc("agent|external|wait|decision|parent")),
		withBoolean("auto_execute", desc("Default true — whether the scheduler picks up instantiated tasks")),
		withString("executor", desc("Executor name for agent-kind tasks")),
		withString("launch_profile", desc("Torque launch_profile id default — preferred over agent_profile.")),
		withString("agent_profile", desc("Legacy agent_profile override. Honored when launch_profile is empty.")),
		withString("system_prompt", desc("System prompt (supports {{var}})")),
		withString("working_dir", desc("Task working directory (supports {{var}})")),
		withString("tools", desc("JSON array of tool names")),
		withString("permissions", desc("JSON object of permissions")),
		withString("environment", desc("JSON object of env vars (values support {{var}})")),
		withString("cost_budget", desc("Cost budget (numeric; -1 unlimited, 0 none, or positive)")),
		withString("max_retries", desc("Max retries (non-negative integer, default 3)")),
		withString("max_duration_ms", desc("Max duration in ms (integer; -1 unlimited or positive)")),
		withString("token_budget", desc("Token budget (integer; -1 unlimited or positive)")),
		withString("on_done", desc("close|review|notify (default review)")),
		withString("on_fail", desc("retry|block|escalate|notify (default retry)")),
		withString("on_review", desc("pause|notify|auto-approve (default pause)")),
		withString("on_done_merge", desc("none|auto|pr|auto-resolve (default none)")),
		withString("escalation_chain", desc("JSON array of escalation-target names")),
		withString("quality_gates", desc("JSON array of gate names")),
		withString("deliverables", desc("JSON array of Deliverable objects")),
		withString("checkpoint_mode", desc("none|blocking|non_blocking (default none)")),
		withString("on_checkpoint_response", desc("resume|review|custom (default resume)")),
		withString("metadata_template", desc("JSON object; string leaves support {{var}}")),
		withString("required_vars", desc("JSON array of variable names enforced at instantiate")),
		withString("tags", desc("JSON array of tag names")),
	), a.handleTemplateCreate)

	a.addTool(newTool("torque_template_get",
		withDescription(`Fetch a template by id (and optional version). When version is omitted, returns the latest non-archived version.
Use when you have the id; torque_template_list for browsing, torque_task_create_from_template when you want to instantiate not inspect.
Response shape: data = {<TemplateRecord fields>} — singleton.
Example: {"id":"backend-fix"}`),
		withString("id", required(), desc("Template id (stable across versions)")),
		withString("version", desc("Optional integer; omit for latest non-archived")),
	), a.handleTemplateGet)

	a.addTool(newTool("torque_template_update",
		withDescription(`Append a new version with merged changes; prior versions remain queryable.
Use for forward-only template evolution; torque_template_archive retires a version, torque_template_delete wipes all versions (rejected if referenced).
Response shape: data = {<TemplateRecord fields>} — singleton, the new version.
Example: {"id":"backend-fix","description":"Fix {{issue}} in {{component}}"}`),
		withString("id", required()),
		withString("name", desc("New name")),
		withString("description", desc("New description")),
		withString("kind", desc("New kind")),
		withBoolean("auto_execute"),
		withString("executor"),
		withString("launch_profile"),
		withString("agent_profile"),
		withString("system_prompt"),
		withString("working_dir"),
		withString("tools"),
		withString("permissions"),
		withString("environment"),
		withString("cost_budget", desc("Cost budget (numeric; -1 unlimited, 0 none, or positive)")),
		withString("max_retries", desc("Max retries (non-negative integer)")),
		withString("max_duration_ms", desc("Max duration in ms (integer; -1 unlimited or positive)")),
		withString("token_budget", desc("Token budget (integer; -1 unlimited or positive)")),
		withString("on_done"),
		withString("on_fail"),
		withString("on_review"),
		withString("on_done_merge"),
		withString("escalation_chain"),
		withString("quality_gates"),
		withString("deliverables"),
		withString("checkpoint_mode"),
		withString("on_checkpoint_response"),
		withString("metadata_template"),
		withString("required_vars"),
		withString("tags"),
	), a.handleTemplateUpdate)

	a.addTool(newTool("torque_template_archive",
		withDescription(`Soft-remove one (id, version) from the live catalog; the row stays queryable with include_archived=true.
Use to retire an old template version while keeping audit; torque_template_delete when you want to hard-wipe all versions.
Response shape: data = {id, version, archived: true}.
Example: {"id":"backend-fix","version":"1"}`),
		withString("id", required(), desc("Template id")),
		withString("version", required(), desc("Template version (integer; pass as string)")),
	), a.handleTemplateArchive)

	a.addTool(newTool("torque_template_delete",
		withDescription(`Hard-delete every version of a template. Rejected with error.code=conflict if any task still references it.
Use sparingly — prefer torque_template_archive to retire. Has no version arg because it wipes all versions.
Response shape: data = {id, deleted: true}.
Example: {"id":"backend-fix"}`),
		withString("id", required(), desc("Template id")),
	), a.handleTemplateDelete)

	a.addTool(newTool("torque_template_list",
		withDescription(`List templates, optionally filtered by kind; include_archived=true surfaces retired rows.
Use for template discovery; torque_template_get when you know the id. Default brief shape excludes the description body; pass verbose="true" for full records (description MAY include {{var}} placeholders).
Response shape: data = {items: [<briefTemplate or TemplateRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"kind":"agent"}`),
		withBoolean("include_archived", desc("Surface retired rows (default false)")),
		withString("kind", desc("Filter: agent|external|wait|decision|parent")),
		withString("verbose", desc("Return full records (incl. description body) instead of brief (string 'true'/'false', default false)")),
	), a.handleTemplateList)

	a.addTool(newTool("torque_task_create_from_template",
		withDescription(`Instantiate a template into a new task; validates required_vars and resolves {{var}} placeholders in description/prompts/metadata.
Use when a matching template exists; torque_task_create for ad-hoc tasks, torque_plan_create for multi-phase plans. Missing required_vars return error.code=arg_invalid.
Response shape: data = {<TaskRecord fields>, Tags[]} — singleton, the new task.
Example: {"template_id":"backend-fix","title":"Fix auth","vars":"{\"issue\":\"auth-42\"}"}`),
		withString("template_id", required(), desc("Template id to instantiate")),
		withString("template_version", desc("Optional integer; omit for latest non-archived")),
		withString("title", required(), desc("Title for the new task")),
		withString("description", desc("Optional description override")),
		withString("vars", desc("JSON object of variable values")),
		withString("overrides", desc("JSON object of task-field overrides (last-wins)")),
		withString("sprint_id", desc("Optional sprint assignment")),
		withString("project_id", desc("Optional project assignment")),
		withString("epic_id", desc("Optional epic assignment")),
		withString("tags", desc("JSON array of extra tag names")),
	), a.handleTaskCreateFromTemplate)
}

func (a *Adapter) handleTemplateCreate(ctx context.Context, req map[string]any) (any, error) {
	in := service.TemplateCreateInput{
		ID:                   reqStr(req, "id"),
		Name:                 reqStr(req, "name"),
		Description:          reqStr(req, "description"),
		Kind:                 reqStr(req, "kind"),
		Executor:             reqStr(req, "executor"),
		LaunchProfile:        reqStr(req, "launch_profile"),
		AgentProfile:         reqStr(req, "agent_profile"),
		SystemPrompt:         reqStr(req, "system_prompt"),
		WorkingDir:           reqStr(req, "working_dir"),
		OnDone:               reqStr(req, "on_done"),
		OnFail:               reqStr(req, "on_fail"),
		OnReview:             reqStr(req, "on_review"),
		OnDoneMerge:          reqStr(req, "on_done_merge"),
		CheckpointMode:       reqStr(req, "checkpoint_mode"),
		OnCheckpointResponse: reqStr(req, "on_checkpoint_response"),
	}
	// auto_execute defaults true when omitted, matching spec §3.2 for agent kind.
	if _, ok := req["auto_execute"]; ok {
		in.AutoExecute = reqBool(req, "auto_execute")
	} else {
		in.AutoExecute = true
	}

	if raw := reqStr(req, "tools"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Tools); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tools JSON: %v", err), "tools")
		}
	}
	if raw := reqStr(req, "permissions"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Permissions); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid permissions JSON: %v", err), "permissions")
		}
	}
	if raw := reqStr(req, "environment"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Environment); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid environment JSON: %v", err), "environment")
		}
	}
	if res := applyTemplateBudgetArgs(req, &in.CostBudget, &in.MaxRetries, &in.MaxDurationMs, &in.TokenBudget); res != nil {
		return nil, res
	}
	if raw := reqStr(req, "escalation_chain"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.EscalationChain); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid escalation_chain JSON: %v", err), "escalation_chain")
		}
	}
	if raw := reqStr(req, "quality_gates"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.QualityGates); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid quality_gates JSON: %v", err), "quality_gates")
		}
	}
	if raw := reqStr(req, "deliverables"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Deliverables); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid deliverables JSON: %v", err), "deliverables")
		}
	}
	if raw := reqStr(req, "metadata_template"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.MetadataTemplate); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid metadata_template JSON: %v", err), "metadata_template")
		}
	}
	if raw := reqStr(req, "required_vars"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.RequiredVars); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid required_vars JSON: %v", err), "required_vars")
		}
	}
	if tags, err := reqStrSlice(req, "tags"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
	} else if tags != nil {
		in.Tags = tags
	}

	tpl, err := a.svc.Template.Create(in)
	if err != nil {
		return errFromService(err)
	}
	return okResult(tpl)
}

func (a *Adapter) handleTemplateGet(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	version := reqInt(req, "version")
	tpl, err := a.svc.Template.Get(id, version)
	if err != nil {
		return errFromService(err)
	}
	return okResult(tpl)
}

func (a *Adapter) handleTemplateUpdate(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	in := service.TemplateUpdateInput{
		Name:                 reqStr(req, "name"),
		Description:          reqStr(req, "description"),
		Kind:                 reqStr(req, "kind"),
		OnDone:               reqStr(req, "on_done"),
		OnFail:               reqStr(req, "on_fail"),
		OnReview:             reqStr(req, "on_review"),
		OnDoneMerge:          reqStr(req, "on_done_merge"),
		CheckpointMode:       reqStr(req, "checkpoint_mode"),
		OnCheckpointResponse: reqStr(req, "on_checkpoint_response"),
	}
	args := req
	if _, ok := args["auto_execute"]; ok {
		v := reqBool(req, "auto_execute")
		in.AutoExecute = &v
	}
	if _, ok := args["executor"]; ok {
		v := reqStr(req, "executor")
		in.Executor = &v
	}
	if _, ok := args["launch_profile"]; ok {
		v := reqStr(req, "launch_profile")
		in.LaunchProfile = &v
	}
	if _, ok := args["agent_profile"]; ok {
		v := reqStr(req, "agent_profile")
		in.AgentProfile = &v
	}
	if _, ok := args["system_prompt"]; ok {
		v := reqStr(req, "system_prompt")
		in.SystemPrompt = &v
	}
	if _, ok := args["working_dir"]; ok {
		v := reqStr(req, "working_dir")
		in.WorkingDir = &v
	}
	if raw := reqStr(req, "tools"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Tools); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tools JSON: %v", err), "tools")
		}
	}
	if raw := reqStr(req, "permissions"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Permissions); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid permissions JSON: %v", err), "permissions")
		}
	}
	if raw := reqStr(req, "environment"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Environment); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid environment JSON: %v", err), "environment")
		}
	}
	if res := applyTemplateBudgetArgs(req, &in.CostBudget, &in.MaxRetries, &in.MaxDurationMs, &in.TokenBudget); res != nil {
		return nil, res
	}
	if raw := reqStr(req, "escalation_chain"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.EscalationChain); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid escalation_chain JSON: %v", err), "escalation_chain")
		}
	}
	if raw := reqStr(req, "quality_gates"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.QualityGates); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid quality_gates JSON: %v", err), "quality_gates")
		}
	}
	if raw := reqStr(req, "deliverables"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Deliverables); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid deliverables JSON: %v", err), "deliverables")
		}
	}
	if raw := reqStr(req, "metadata_template"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.MetadataTemplate); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid metadata_template JSON: %v", err), "metadata_template")
		}
	}
	if raw := reqStr(req, "required_vars"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.RequiredVars); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid required_vars JSON: %v", err), "required_vars")
		}
	}
	if tags, err := reqStrSlice(req, "tags"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
	} else if tags != nil {
		in.Tags = tags
	}

	tpl, err := a.svc.Template.Update(id, in)
	if err != nil {
		return errFromService(err)
	}
	return okResult(tpl)
}

func (a *Adapter) handleTemplateArchive(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	version := reqInt(req, "version")
	if err := a.svc.Template.Archive(id, version); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":       id,
		"version":  version,
		"archived": true,
	})
}

func (a *Adapter) handleTemplateDelete(ctx context.Context, req map[string]any) (any, error) {
	id := reqStr(req, "id")
	if err := a.svc.Template.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"deleted": true,
	})
}

func (a *Adapter) handleTemplateList(ctx context.Context, req map[string]any) (any, error) {
	verbose := reqStrBool(req, "verbose")
	list, err := a.svc.Template.List(service.TemplateListOpts{
		IncludeArchived: reqBool(req, "include_archived"),
		Kind:            reqStr(req, "kind"),
	})
	if err != nil {
		return errFromService(err)
	}
	limit := clampLimit(0, defaultTemplateListLimit, maxTemplateListLimit)
	items := make([]any, 0, len(list))
	for _, tpl := range list {
		if verbose {
			// Verbose includes full record with description body; template
			// description can hold prose + {{var}} placeholders, which is
			// the point — callers asking for verbose want it.
			items = append(items, tpl)
		} else {
			items = append(items, toBriefTemplate(tpl))
		}
	}
	return cappedJSONResult(items, limit)
}

func (a *Adapter) handleTaskCreateFromTemplate(ctx context.Context, req map[string]any) (any, error) {
	in := service.TemplateInstantiateInput{
		TemplateID:      reqStr(req, "template_id"),
		TemplateVersion: reqInt(req, "template_version"),
		Title:           reqStr(req, "title"),
		Description:     reqStr(req, "description"),
		SprintID:        reqStr(req, "sprint_id"),
		ProjectID:       reqStr(req, "project_id"),
		EpicID:          reqStr(req, "epic_id"),
	}
	if raw := reqStr(req, "vars"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Vars); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid vars JSON: %v", err), "vars")
		}
	}
	if raw := reqStr(req, "overrides"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Overrides); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid overrides JSON: %v", err), "overrides")
		}
	}
	if tags, err := reqStrSlice(req, "tags"); err != nil {
		return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
	} else if tags != nil {
		in.Tags = tags
	}

	task, err := a.svc.Template.Instantiate(in)
	if err != nil {
		return errFromService(err)
	}
	return a.taskResult(task)
}

func applyTemplateBudgetArgs(req map[string]any, cost **float64, retries **int, duration **int64, tokens **int64) error {
	args := req
	if _, ok := args["cost_budget"]; ok {
		v, err := reqTaskListFloat(req, "cost_budget")
		if err != nil {
			return argError(ErrCodeArgInvalid, err.Error(), "cost_budget")
		}
		*cost = &v
	}
	if _, ok := args["max_retries"]; ok {
		v, _, err := reqTaskListInt(req, "max_retries")
		if err != nil {
			return argError(ErrCodeArgInvalid, err.Error(), "max_retries")
		}
		*retries = &v
	}
	if _, ok := args["max_duration_ms"]; ok {
		v, err := reqTaskListInt64(req, "max_duration_ms")
		if err != nil {
			return argError(ErrCodeArgInvalid, err.Error(), "max_duration_ms")
		}
		*duration = &v
	}
	if _, ok := args["token_budget"]; ok {
		v, err := reqTaskListInt64(req, "token_budget")
		if err != nil {
			return argError(ErrCodeArgInvalid, err.Error(), "token_budget")
		}
		*tokens = &v
	}
	return nil
}

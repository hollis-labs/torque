package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

func (a *Adapter) registerTemplateTools() {
	a.server.AddTool(mcp.NewTool("clockwork_template_create",
		mcp.WithDescription(`Create a task template at version=1; subsequent clockwork_template_update calls append new versions.
Use to encode repeated task shapes with {{var}} placeholders and required_vars; clockwork_task_create_from_template instantiates. clockwork_task_create is the ad-hoc alternative.
Response shape: data = {<TemplateRecord fields>} — singleton.
Example: {"id":"backend-fix","name":"Backend Fix","description":"Fix {{issue}}","kind":"agent","executor":"cli","required_vars":"[\"issue\"]"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Template id (stable across versions)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Human-readable name")),
		mcp.WithString("description", mcp.Required(), mcp.Description("Template description (supports {{var}})")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("agent|external|wait|decision|parent")),
		mcp.WithBoolean("auto_execute", mcp.Description("Default true — whether the scheduler picks up instantiated tasks")),
		mcp.WithString("executor", mcp.Description("Executor name for agent-kind tasks")),
		mcp.WithString("agent_profile", mcp.Description("Agent profile override")),
		mcp.WithString("system_prompt", mcp.Description("System prompt (supports {{var}})")),
		mcp.WithString("working_dir", mcp.Description("Task working directory (supports {{var}})")),
		mcp.WithString("tools", mcp.Description("JSON array of tool names")),
		mcp.WithString("permissions", mcp.Description("JSON object of permissions")),
		mcp.WithString("environment", mcp.Description("JSON object of env vars (values support {{var}})")),
		mcp.WithString("on_done", mcp.Description("close|review|notify (default review)")),
		mcp.WithString("on_fail", mcp.Description("retry|block|escalate|notify (default retry)")),
		mcp.WithString("on_review", mcp.Description("pause|notify|auto-approve (default pause)")),
		mcp.WithString("on_done_merge", mcp.Description("none|auto|pr|auto-resolve (default none)")),
		mcp.WithString("escalation_chain", mcp.Description("JSON array of escalation-target names")),
		mcp.WithString("quality_gates", mcp.Description("JSON array of gate names")),
		mcp.WithString("deliverables", mcp.Description("JSON array of Deliverable objects")),
		mcp.WithString("checkpoint_mode", mcp.Description("none|blocking|non_blocking (default none)")),
		mcp.WithString("on_checkpoint_response", mcp.Description("resume|review|custom (default resume)")),
		mcp.WithString("metadata_template", mcp.Description("JSON object; string leaves support {{var}}")),
		mcp.WithString("required_vars", mcp.Description("JSON array of variable names enforced at instantiate")),
		mcp.WithString("tags", mcp.Description("JSON array of tag names")),
	), a.handleTemplateCreate)

	a.server.AddTool(mcp.NewTool("clockwork_template_get",
		mcp.WithDescription(`Fetch a template by id (and optional version). When version is omitted, returns the latest non-archived version.
Use when you have the id; clockwork_template_list for browsing, clockwork_task_create_from_template when you want to instantiate not inspect.
Response shape: data = {<TemplateRecord fields>} — singleton.
Example: {"id":"backend-fix"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Template id (stable across versions)")),
		mcp.WithString("version", mcp.Description("Optional integer; omit for latest non-archived")),
	), a.handleTemplateGet)

	a.server.AddTool(mcp.NewTool("clockwork_template_update",
		mcp.WithDescription(`Append a new version with merged changes; prior versions remain queryable.
Use for forward-only template evolution; clockwork_template_archive retires a version, clockwork_template_delete wipes all versions (rejected if referenced).
Response shape: data = {<TemplateRecord fields>} — singleton, the new version.
Example: {"id":"backend-fix","description":"Fix {{issue}} in {{component}}"}`),
		mcp.WithString("id", mcp.Required()),
		mcp.WithString("name", mcp.Description("New name")),
		mcp.WithString("description", mcp.Description("New description")),
		mcp.WithString("kind", mcp.Description("New kind")),
		mcp.WithBoolean("auto_execute"),
		mcp.WithString("executor"),
		mcp.WithString("agent_profile"),
		mcp.WithString("system_prompt"),
		mcp.WithString("working_dir"),
		mcp.WithString("tools"),
		mcp.WithString("permissions"),
		mcp.WithString("environment"),
		mcp.WithString("on_done"),
		mcp.WithString("on_fail"),
		mcp.WithString("on_review"),
		mcp.WithString("on_done_merge"),
		mcp.WithString("escalation_chain"),
		mcp.WithString("quality_gates"),
		mcp.WithString("deliverables"),
		mcp.WithString("checkpoint_mode"),
		mcp.WithString("on_checkpoint_response"),
		mcp.WithString("metadata_template"),
		mcp.WithString("required_vars"),
		mcp.WithString("tags"),
	), a.handleTemplateUpdate)

	a.server.AddTool(mcp.NewTool("clockwork_template_archive",
		mcp.WithDescription(`Soft-remove one (id, version) from the live catalog; the row stays queryable with include_archived=true.
Use to retire an old template version while keeping audit; clockwork_template_delete when you want to hard-wipe all versions.
Response shape: data = {id, version, archived: true}.
Example: {"id":"backend-fix","version":"1"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Template id")),
		mcp.WithString("version", mcp.Required(), mcp.Description("Template version (integer; pass as string)")),
	), a.handleTemplateArchive)

	a.server.AddTool(mcp.NewTool("clockwork_template_delete",
		mcp.WithDescription(`Hard-delete every version of a template. Rejected with error.code=conflict if any task still references it.
Use sparingly — prefer clockwork_template_archive to retire. Has no version arg because it wipes all versions.
Response shape: data = {id, deleted: true}.
Example: {"id":"backend-fix"}`),
		mcp.WithString("id", mcp.Required(), mcp.Description("Template id")),
	), a.handleTemplateDelete)

	a.server.AddTool(mcp.NewTool("clockwork_template_list",
		mcp.WithDescription(`List templates, optionally filtered by kind; include_archived=true surfaces retired rows.
Use for template discovery; clockwork_template_get when you know the id. Default brief shape excludes the description body; pass verbose="true" for full records (description MAY include {{var}} placeholders).
Response shape: data = {items: [<briefTemplate or TemplateRecord>...], meta: {truncated, returned, limit, hint?}}.
Example: {"kind":"agent"}`),
		mcp.WithBoolean("include_archived", mcp.Description("Surface retired rows (default false)")),
		mcp.WithString("kind", mcp.Description("Filter: agent|external|wait|decision|parent")),
		mcp.WithString("verbose", mcp.Description("Return full records (incl. description body) instead of brief (string 'true'/'false', default false)")),
	), a.handleTemplateList)

	a.server.AddTool(mcp.NewTool("clockwork_task_create_from_template",
		mcp.WithDescription(`Instantiate a template into a new task; validates required_vars and resolves {{var}} placeholders in description/prompts/metadata.
Use when a matching template exists; clockwork_task_create for ad-hoc tasks, clockwork_plan_create for multi-phase plans. Missing required_vars return error.code=arg_invalid.
Response shape: data = {<TaskRecord fields>, Tags[]} — singleton, the new task.
Example: {"template_id":"backend-fix","title":"Fix auth","vars":"{\"issue\":\"auth-42\"}"}`),
		mcp.WithString("template_id", mcp.Required(), mcp.Description("Template id to instantiate")),
		mcp.WithString("template_version", mcp.Description("Optional integer; omit for latest non-archived")),
		mcp.WithString("title", mcp.Required(), mcp.Description("Title for the new task")),
		mcp.WithString("description", mcp.Description("Optional description override")),
		mcp.WithString("vars", mcp.Description("JSON object of variable values")),
		mcp.WithString("overrides", mcp.Description("JSON object of task-field overrides (last-wins)")),
		mcp.WithString("sprint_id", mcp.Description("Optional sprint assignment")),
		mcp.WithString("project_id", mcp.Description("Optional project assignment")),
		mcp.WithString("epic_id", mcp.Description("Optional epic assignment")),
		mcp.WithString("tags", mcp.Description("JSON array of extra tag names")),
	), a.handleTaskCreateFromTemplate)
}

func (a *Adapter) handleTemplateCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	in := service.TemplateCreateInput{
		ID:                   reqStr(req, "id"),
		Name:                 reqStr(req, "name"),
		Description:          reqStr(req, "description"),
		Kind:                 reqStr(req, "kind"),
		Executor:             reqStr(req, "executor"),
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
	if _, ok := req.GetArguments()["auto_execute"]; ok {
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
	if raw := reqStr(req, "tags"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Tags); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
		}
	}

	tpl, err := a.svc.Template.Create(in)
	if err != nil {
		return errFromService(err)
	}
	return okResult(tpl)
}

func (a *Adapter) handleTemplateGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	version := reqInt(req, "version")
	tpl, err := a.svc.Template.Get(id, version)
	if err != nil {
		return errFromService(err)
	}
	return okResult(tpl)
}

func (a *Adapter) handleTemplateUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	args := req.GetArguments()
	if _, ok := args["auto_execute"]; ok {
		v := reqBool(req, "auto_execute")
		in.AutoExecute = &v
	}
	if _, ok := args["executor"]; ok {
		v := reqStr(req, "executor")
		in.Executor = &v
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
	if raw := reqStr(req, "tags"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Tags); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
		}
	}

	tpl, err := a.svc.Template.Update(id, in)
	if err != nil {
		return errFromService(err)
	}
	return okResult(tpl)
}

func (a *Adapter) handleTemplateArchive(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

func (a *Adapter) handleTemplateDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := reqStr(req, "id")
	if err := a.svc.Template.Delete(id); err != nil {
		return errFromService(err)
	}
	return okResult(map[string]any{
		"id":      id,
		"deleted": true,
	})
}

func (a *Adapter) handleTemplateList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

func (a *Adapter) handleTaskCreateFromTemplate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	if raw := reqStr(req, "tags"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Tags); err != nil {
			return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
		}
	}

	task, err := a.svc.Template.Instantiate(in)
	if err != nil {
		return errFromService(err)
	}
	return a.taskResult(task)
}

package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
)

const (
	taskBundleRoot       = "tasks"
	taskBundleReadmePath = "tasks/README.md"
)

type plantedTaskContext struct {
	TaskID       string            `json:"task_id"`
	Title        string            `json:"title,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	Status       string            `json:"status,omitempty"`
	Priority     int               `json:"priority,omitempty"`
	Description  string            `json:"description,omitempty"`
	ProjectID    string            `json:"project_id,omitempty"`
	ParentID     string            `json:"parent_id,omitempty"`
	SprintID     string            `json:"sprint_id,omitempty"`
	EpicID       string            `json:"epic_id,omitempty"`
	DependsOn    []string          `json:"depends_on,omitempty"`
	RunID        int64             `json:"run_id,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	AgentProfile string            `json:"agent_profile,omitempty"`
	Role         string            `json:"role,omitempty"`
	WorkRoot     string            `json:"work_root,omitempty"`
	RepoRoot     string            `json:"repo_root,omitempty"`
	LoopbackURL  string            `json:"loopback_url,omitempty"`
	SessionMeta  map[string]string `json:"session_meta,omitempty"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
}

func taskContextNativeFiles(in buildLaunchPlanInput) []agentlaunch.NativeFile {
	ctx := taskContextFromLaunch(in)
	if ctx.TaskID == "" {
		return nil
	}
	jsonBody, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return nil
	}
	taskDir := taskBundleRoot + "/" + ctx.TaskID
	return []agentlaunch.NativeFile{
		{
			Kind:    agentlaunch.NativeFileRaw,
			RelPath: taskBundleReadmePath,
			Content: renderTaskBundleReadme(ctx),
		},
		{
			Kind:    agentlaunch.NativeFileRaw,
			RelPath: taskDir + "/task.md",
			Content: renderTaskContextMarkdown(ctx),
		},
		{
			Kind:    agentlaunch.NativeFileRaw,
			RelPath: taskDir + "/task.json",
			Content: string(jsonBody) + "\n",
		},
		{
			Kind:    agentlaunch.NativeFileRaw,
			RelPath: taskDir + "/process.md",
			Content: renderTaskProcessMarkdown(ctx),
		},
	}
}

func taskContextFromLaunch(in buildLaunchPlanInput) plantedTaskContext {
	opts := in.Options
	return plantedTaskContext{
		TaskID:       opts.TaskID,
		Title:        opts.TaskTitle,
		Kind:         opts.TaskKind,
		Status:       opts.TaskStatus,
		Priority:     opts.TaskPriority,
		Description:  opts.Description,
		ProjectID:    opts.ProjectID,
		ParentID:     opts.ParentID,
		SprintID:     opts.SprintID,
		EpicID:       opts.EpicID,
		DependsOn:    append([]string(nil), opts.DependsOn...),
		RunID:        opts.RunID,
		SessionID:    in.SessionID,
		AgentProfile: in.AgentProfile,
		Role:         in.Role,
		WorkRoot:     opts.Workdir,
		RepoRoot:     resolveRepoRoot(opts),
		LoopbackURL:  in.LoopbackURL,
		SessionMeta:  copyStringMap(opts.SessionMeta),
		Metadata:     opts.Metadata,
	}
}

func renderTaskContextMarkdown(ctx plantedTaskContext) string {
	var b strings.Builder
	b.WriteString("# Assigned Task\n\n")
	b.WriteString("Use this planted file as the local source of truth for task identity. Do not call MCP only to rediscover these IDs; use MCP when you need fresh state or need to update the task.\n\n")
	b.WriteString("## IDs\n\n")
	writeMarkdownKV(&b, "task_id", ctx.TaskID)
	writeMarkdownKV(&b, "run_id", fmt.Sprintf("%d", ctx.RunID))
	writeMarkdownKV(&b, "session_id", ctx.SessionID)
	writeMarkdownKV(&b, "project_id", ctx.ProjectID)
	writeMarkdownKV(&b, "parent_id", ctx.ParentID)
	writeMarkdownKV(&b, "sprint_id", ctx.SprintID)
	writeMarkdownKV(&b, "epic_id", ctx.EpicID)
	writeMarkdownList(&b, "depends_on", ctx.DependsOn)
	b.WriteString("\n## Task\n\n")
	writeMarkdownKV(&b, "title", ctx.Title)
	writeMarkdownKV(&b, "kind", ctx.Kind)
	writeMarkdownKV(&b, "status_at_boot", ctx.Status)
	if ctx.Priority != 0 {
		writeMarkdownKV(&b, "priority", fmt.Sprintf("%d", ctx.Priority))
	}
	if strings.TrimSpace(ctx.Description) != "" {
		b.WriteString("\n### Description\n\n")
		b.WriteString(strings.TrimSpace(ctx.Description))
		b.WriteString("\n")
	}
	b.WriteString("\n## Runtime\n\n")
	writeMarkdownKV(&b, "agent_profile", ctx.AgentProfile)
	writeMarkdownKV(&b, "role", ctx.Role)
	writeMarkdownKV(&b, "work_root", ctx.WorkRoot)
	writeMarkdownKV(&b, "repo_root", ctx.RepoRoot)
	writeMarkdownKV(&b, "loopback_url", ctx.LoopbackURL)
	writeProjectContextMarkdown(&b, ctx.Metadata)
	b.WriteString("\nMachine-readable copy: `task.json`. Worker process: `process.md`.\n")
	return b.String()
}

func renderTaskBundleReadme(ctx plantedTaskContext) string {
	var b strings.Builder
	b.WriteString("# Planted Torque Tasks\n\n")
	b.WriteString("Torque planted the task bundle for this boot so you can start without discovery calls. Read the assigned task files first; use MCP only for fresh state, checkpoints, or task updates.\n\n")
	b.WriteString("## Assigned\n\n")
	b.WriteString("- `")
	b.WriteString(ctx.TaskID)
	b.WriteString("/task.md`\n")
	b.WriteString("- `")
	b.WriteString(ctx.TaskID)
	b.WriteString("/task.json`\n")
	b.WriteString("- `")
	b.WriteString(ctx.TaskID)
	b.WriteString("/process.md`\n")
	return b.String()
}

func renderTaskProcessMarkdown(ctx plantedTaskContext) string {
	var b strings.Builder
	b.WriteString("# Worker Process\n\n")
	b.WriteString("Keep the process minimal:\n\n")
	b.WriteString("1. Read `task.md` and inspect the relevant project files in `work_root`.\n")
	b.WriteString("2. Make the smallest complete change for the assigned task.\n")
	b.WriteString("3. Run focused verification that matches the change.\n")
	b.WriteString("4. Use the `torque_loopback` MCP task-scoped tools to report the result. Do not pass a `task_id`; this boot is already bound to `")
	b.WriteString(ctx.TaskID)
	b.WriteString("`.\n\n")
	b.WriteString("Useful local facts:\n\n")
	writeMarkdownKV(&b, "task_id", ctx.TaskID)
	writeMarkdownKV(&b, "run_id", fmt.Sprintf("%d", ctx.RunID))
	writeMarkdownKV(&b, "session_id", ctx.SessionID)
	writeMarkdownKV(&b, "work_root", ctx.WorkRoot)
	writeMarkdownKV(&b, "repo_root", ctx.RepoRoot)
	writeMarkdownKV(&b, "loopback_url", ctx.LoopbackURL)
	b.WriteString("\nCompletion guidance:\n\n")
	b.WriteString("- If the implementation is ready for review, call `torque_task_review` with a concise summary and evidence.\n")
	b.WriteString("- If blocked, call `torque_task_blocked` with the blocker and next needed action.\n")
	b.WriteString("- If you need to record progress without transitioning, use the task-scoped summary/checkpoint tools available on `torque_loopback`.\n")
	return b.String()
}

func writeProjectContextMarkdown(b *strings.Builder, metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}
	raw, ok := metadata["project_context"].(map[string]any)
	if !ok || len(raw) == 0 {
		return
	}
	b.WriteString("\n## Project Context\n\n")
	writeMarkdownKV(b, "project_name", stringFromAny(raw["name"]))
	writeMarkdownKV(b, "project_repo_path", stringFromAny(raw["repo_path"]))
	writeMarkdownKV(b, "project_agent_path", stringFromAny(raw["agent_path"]))
	writeMarkdownList(b, "read_paths", stringSliceFromAny(raw["read_paths"]))
	writeMarkdownList(b, "write_paths", stringSliceFromAny(raw["write_paths"]))
	writeMarkdownList(b, "context_paths", stringSliceFromAny(raw["context_paths"]))
	writeMarkdownList(b, "rules", stringSliceFromAny(raw["rules"]))
	writeArtifactPathsMarkdown(b, raw["artifacts"])
}

func writeArtifactPathsMarkdown(b *strings.Builder, raw any) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		artifact, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if path := stringFromAny(artifact["file_path"]); path != "" {
			values = append(values, path)
		}
	}
	writeMarkdownList(b, "artifact_paths", values)
}

func writeMarkdownKV(b *strings.Builder, key, value string) {
	if strings.TrimSpace(value) == "" || value == "0" {
		return
	}
	b.WriteString("- ")
	b.WriteString(key)
	b.WriteString(": `")
	b.WriteString(value)
	b.WriteString("`\n")
}

func writeMarkdownList(b *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		return
	}
	b.WriteString("- ")
	b.WriteString(key)
	b.WriteString(": ")
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("`")
		b.WriteString(value)
		b.WriteString("`")
	}
	b.WriteString("\n")
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return s
}

func stringSliceFromAny(raw any) []string {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

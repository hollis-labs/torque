package scheduler

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// CW-20260503-0019 (S2.3) — Reviewer end-agent V1.
//
// When a kind=agent executor task transitions to `review`, the lifecycle
// manager enqueues a kind=internal end-agent task that audits the
// target's disposition. The agent profile, template path, and
// target_task_id are wired in at enqueue time; cliexec runs the
// internal task like any other CLI agent and the spawned reviewer uses
// MCP loopback to inspect + comment on the target.
//
// V1 is disposition audit only. Code-review-with-fix is V2 of
// CW-20260417-0483. The orchestrator-as-reviewer model (D4) means
// every kind=agent task gets exactly one end-agent enqueued per
// review-transition; cycles re-fire as the target re-enters review.
const (
	// EndAgentProfile is the agent_profile name the lifecycle hook
	// stamps on the spawned internal task. Operators can register a
	// matching entry in profiles.yaml; the substrate ships a sample.
	EndAgentProfile = "reviewer-end-agent"

	// EndAgentTemplateEnvVar overrides the default template-search dir.
	// When unset the resolver looks in $HOME/.clockwork/end-agent-templates.
	EndAgentTemplateEnvVar = "CLOCKWORK_END_AGENT_TEMPLATE_DIR"

	// DefaultEndAgentTemplate is the file name read from the resolved dir.
	// Operators replace its contents to customize the V1 checklist.
	DefaultEndAgentTemplate = "default-end-agent.md"

	// EndAgentAuthor is the comment-author prefix the end-agent uses
	// when commenting on the target. Lifecycle-side failure comments
	// share this prefix so operator audits can grep across all
	// end-agent activity.
	EndAgentAuthor = "[system/end-agent]"

	// endAgentDefaultBudget is the cost ceiling stamped on a freshly
	// enqueued end-agent. Separate from the target task's budget so
	// reviewer cost can't starve the executor's allotment (AC6).
	endAgentDefaultBudget = 0.50
)

//go:embed templates/default-end-agent.md
var embeddedEndAgentTemplate string

// endAgentMetadata is the JSON shape stamped on the internal task's
// metadata column under the `end_agent` key. Operators inspecting the
// task can see exactly which template ran and which target it audited.
type endAgentMetadata struct {
	Template     string `json:"template"`
	TargetTaskID string `json:"target_task_id"`
}

// shouldEnqueueEndAgent reports whether a transition to `review` should
// trigger an end-agent enqueue. Only kind=agent tasks qualify — kind=
// parent / plan / decision / wait don't run an executor, and kind=
// internal would recurse into its own audit.
func shouldEnqueueEndAgent(task *sqlstore.TaskRecord, newStatus string) bool {
	if newStatus != "review" {
		return false
	}
	return task.Kind == "agent"
}

// enqueueEndAgent creates and inserts a kind=internal end-agent task
// targeting `target`. Failures are logged but do NOT propagate — the
// caller's transition has already committed and we'd otherwise leave
// the target stuck at review on a transient enqueue error.
//
// Bypasses service.Task.Create on purpose: that path force-flips
// manual=true (CW-20260417-0133 safety override) and the end-agent
// must auto-dispatch. Direct store.CreateTask with audited fields is
// the only correct insertion point here.
func (lm *LifecycleManager) enqueueEndAgent(target *sqlstore.TaskRecord) {
	template, templatePath := loadEndAgentTemplate()

	endID, err := lm.store.NextTaskID()
	if err != nil {
		log.Printf("[end-agent] next task id: %v", err)
		return
	}

	meta, err := json.Marshal(map[string]any{
		"end_agent": endAgentMetadata{
			Template:     templatePath,
			TargetTaskID: target.ID,
		},
	})
	if err != nil {
		log.Printf("[end-agent] marshal metadata for %s: %v", target.ID, err)
		return
	}

	rec := &sqlstore.TaskRecord{
		ID:                   endID,
		Title:                "end-agent: " + target.ID,
		Description:          "Disposition audit for " + target.ID + ". V1 reviewer (CW-20260503-0019).",
		Status:               "todo",
		Priority:             2,
		Manual:               false,
		Executor:             "cli",
		AgentProfile:         EndAgentProfile,
		WorkingDir:           target.WorkingDir,
		SystemPrompt:         template,
		MaxRetries:           0,
		OnDone:               "close",
		OnFail:               "block",
		OnReview:             "pause",
		OnDoneMerge:          "none",
		Kind:                 "internal",
		SourceType:           "agent",
		Trust:                "normal",
		CheckpointMode:       "none",
		OnCheckpointResponse: "resume",
		Metadata:             sql.NullString{String: string(meta), Valid: true},
		CostBudget:           sql.NullFloat64{Float64: endAgentDefaultBudget, Valid: true},
		ParentID:             sql.NullString{String: target.ID, Valid: true},
		ProjectID:            target.ProjectID,
		SprintID:             target.SprintID,
		EpicID:               target.EpicID,
	}

	if err := lm.store.CreateTask(rec); err != nil {
		log.Printf("[end-agent] enqueue for target=%s: %v", target.ID, err)
		return
	}
	// applyDefaults rewrites MaxRetries=0 → 3 inside CreateTask (the
	// store can't distinguish "explicitly zero" from "use default"
	// across an int field). Stamp the real 0 back via UpdateTask, which
	// uses *int and respects pointer-to-zero. AC5: no retry on reviewer
	// failure.
	zero := 0
	if err := lm.store.UpdateTask(endID, sqlstore.TaskUpdate{MaxRetries: &zero}); err != nil {
		log.Printf("[end-agent] zero-retry override for %s: %v", endID, err)
	}
	log.Printf("[end-agent] enqueued %s for target=%s (template=%s)", endID, target.ID, templatePath)
}

// loadEndAgentTemplate resolves the V1 reviewer template content. Lookup
// order: $CLOCKWORK_END_AGENT_TEMPLATE_DIR/<DefaultEndAgentTemplate>,
// then $HOME/.clockwork/end-agent-templates/<DefaultEndAgentTemplate>,
// then the embedded fallback. Returns the content + the resolved path
// (for traceability metadata; "<embedded>" when the fallback is used).
func loadEndAgentTemplate() (string, string) {
	dir := os.Getenv(EndAgentTemplateEnvVar)
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".clockwork", "end-agent-templates")
		}
	}
	if dir != "" {
		path := filepath.Join(dir, DefaultEndAgentTemplate)
		if data, err := os.ReadFile(path); err == nil {
			return string(data), path
		}
	}
	return embeddedEndAgentTemplate, "<embedded>"
}

// commentEndAgentFailure posts the canonical `[system/end-agent] failed`
// comment to a target when the spawned reviewer task itself blocked or
// failed. Called from the lifecycle hook when an internal task with a
// parent_id transitions to one of those states. The reviewer's audit
// outcomes (pass / human-required) are NOT this hook's concern — the
// reviewer comments those itself via MCP.
func (lm *LifecycleManager) commentEndAgentFailure(internal *sqlstore.TaskRecord, reason string) {
	if !internal.ParentID.Valid || internal.ParentID.String == "" {
		return
	}
	content := EndAgentAuthor + " failed for " + internal.ID
	if reason != "" {
		content += ": " + reason
	}
	content += " — target stays at `review`; human follow-up required."
	if err := lm.store.AddComment(&sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   internal.ParentID.String,
		Author:     EndAgentAuthor,
		Content:    content,
	}); err != nil {
		log.Printf("[end-agent] add failure comment for target=%s: %v", internal.ParentID.String, err)
	}
}

// shouldCommentEndAgentFailure reports whether a transition deserves a
// failure comment. Fires on internal tasks with a parent_id that
// transition to a non-success terminal — `blocked` or `failed`. `done`
// is the success path (the reviewer succeeded — its own audit comments
// stand on their own).
func shouldCommentEndAgentFailure(task *sqlstore.TaskRecord, newStatus string) bool {
	if task.Kind != "internal" {
		return false
	}
	if !task.ParentID.Valid || task.ParentID.String == "" {
		return false
	}
	switch newStatus {
	case "blocked", "failed":
		return true
	}
	return false
}


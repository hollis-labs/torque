package scheduler

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
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
	// When unset the resolver looks in $HOME/.torque/end-agent-templates.
	EndAgentTemplateEnvVar = "TORQUE_END_AGENT_TEMPLATE_DIR"

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

	// endAgentFailureCommentTimeout bounds the best-effort failure comment path
	// so lifecycle processing cannot hang indefinitely on a stuck telemetry
	// worker.
	endAgentFailureCommentTimeout = 5 * time.Second

	// endAgentEnqueueMaxAttempts bounds the retry loop around the serialized
	// end-agent enqueue. The atomic NextTaskID+CreateTask inside one write
	// transaction already eliminates the ID-collision race that made enqueue
	// intermittent (CW-20260519-0081); the retry is belt-and-suspenders for a
	// genuinely transient store error (e.g. a checkpointer-induced
	// SQLITE_BUSY). After the last attempt fails the enqueue takes the
	// observable failure path instead of silently leaving the target stuck.
	endAgentEnqueueMaxAttempts = 3
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
// targeting `target`. Every kind=agent task that reaches `review` MUST get
// exactly one reviewer enqueued, or the orchestrator stalls indefinitely
// (CW-20260519-0081). Two properties make that deterministic:
//
//  1. Reliable enqueue. ID allocation and the INSERT run in ONE
//     writeq-serialized transaction (NextTaskID + CreateTask inside a single
//     stateWriter.Submit). The previous implementation called
//     store.NextTaskID() and store.CreateTask() as two separate auto-commit
//     statements: another concurrent task creation could claim the same
//     MAX(id)+1 between them, and the losing INSERT failed a UNIQUE
//     constraint. That error was logged and swallowed — the intermittent
//     "ph-1 reviewer fired, ph-2 did not" signature. Holding the writer lock
//     across SELECT-MAX and INSERT closes the race against every other
//     writer.
//
//  2. Observable failure. If the enqueue still cannot be persisted after a
//     bounded retry, failEndAgentEnqueue posts a `[system/end-agent] failed
//     to enqueue` comment on the target and emits an
//     `end_agent_enqueue_failed` run_event. The orchestrator greps target
//     comments for the `[system/end-agent]` prefix, so this turns a silent
//     stall into a signal it can escalate on.
//
// Failures never propagate to the caller: the target's transition to
// `review` has already committed and must stand.
//
// Bypasses service.Task.Create on purpose: that path force-flips
// manual=true (CW-20260417-0133 safety override) and the end-agent
// must auto-dispatch. A writeq-serialized CreateTask with audited fields is
// the only correct insertion point here.
func (lm *LifecycleManager) enqueueEndAgent(target *sqlstore.TaskRecord) {
	template, templatePath := loadEndAgentTemplate()

	meta, err := json.Marshal(map[string]any{
		"end_agent": endAgentMetadata{
			Template:     templatePath,
			TargetTaskID: target.ID,
		},
	})
	if err != nil {
		lm.failEndAgentEnqueue(target, "marshal metadata: "+err.Error())
		return
	}

	rec := &sqlstore.TaskRecord{
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

	var (
		endID   string
		lastErr error
	)
	for attempt := 1; attempt <= endAgentEnqueueMaxAttempts; attempt++ {
		lastErr = lm.stateWriter.Submit(context.Background(), "lifecycle_enqueue_end_agent", func(tx *sqlstore.WriteTx) error {
			id, err := tx.NextTaskID()
			if err != nil {
				return err
			}
			rec.ID = id
			if err := tx.CreateTask(rec); err != nil {
				return err
			}
			// applyDefaults inside CreateTask rewrites MaxRetries=0 → 3 (an
			// int field can't distinguish "explicitly zero" from "use
			// default"). Stamp the real 0 back in the SAME transaction so
			// AC5 (no retry on reviewer failure) holds atomically with the
			// insert.
			if err := tx.SetTaskMaxRetries(id, 0); err != nil {
				return err
			}
			endID = id
			return nil
		})
		if lastErr == nil {
			break
		}
		log.Printf("[end-agent] enqueue attempt %d/%d for target=%s: %v",
			attempt, endAgentEnqueueMaxAttempts, target.ID, lastErr)
	}
	if lastErr != nil {
		lm.failEndAgentEnqueue(target, lastErr.Error())
		return
	}
	log.Printf("[end-agent] enqueued %s for target=%s (template=%s)", endID, target.ID, templatePath)
}

// failEndAgentEnqueue is the observable failure path for an end-agent that
// could not be enqueued. It posts the canonical `[system/end-agent] failed to
// enqueue` comment on the target — the same author prefix the orchestrator
// greps for end-agent activity — and emits an `end_agent_enqueue_failed`
// run_event for postmortem + SSE observers. Without this the target would sit
// at `review` with no reviewer and no signal, and the orchestrator would
// stall until its 30-minute backstop (CW-20260519-0081 incident).
func (lm *LifecycleManager) failEndAgentEnqueue(target *sqlstore.TaskRecord, reason string) {
	log.Printf("[end-agent] ENQUEUE FAILED for target=%s: %s", target.ID, reason)

	content := EndAgentAuthor + " failed to enqueue reviewer for " + target.ID
	if reason != "" {
		content += ": " + reason
	}
	content += " — target stays at `review`; no reviewer will run. Orchestrator/human follow-up required."
	ctx, cancel := context.WithTimeout(context.Background(), endAgentFailureCommentTimeout)
	defer cancel()
	if err := lm.telemetry.AddComment(ctx, &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   target.ID,
		Author:     EndAgentAuthor,
		Content:    content,
	}); err != nil {
		log.Printf("[end-agent] add enqueue-failure comment for target=%s: %v", target.ID, err)
	}

	writeRunEvent(context.Background(), lm.telemetry, 0, target.ID, "end_agent_enqueue_failed", map[string]any{
		"target_task_id": target.ID,
		"reason":         reason,
	})
}

// loadEndAgentTemplate resolves the V1 reviewer template content. Lookup
// order: $TORQUE_END_AGENT_TEMPLATE_DIR/<DefaultEndAgentTemplate>,
// then $HOME/.torque/end-agent-templates/<DefaultEndAgentTemplate>,
// then the embedded fallback. Returns the content + the resolved path
// (for traceability metadata; "<embedded>" when the fallback is used).
func loadEndAgentTemplate() (string, string) {
	dir := os.Getenv(EndAgentTemplateEnvVar)
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".torque", "end-agent-templates")
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
	ctx, cancel := context.WithTimeout(context.Background(), endAgentFailureCommentTimeout)
	defer cancel()
	if err := lm.telemetry.AddComment(ctx, &sqlstore.CommentRecord{
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

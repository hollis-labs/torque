package scheduler

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// CW-20260503-0019 (S2.3) — Reviewer end-agent.
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

	// endAgentEnqueueBaseDelay seeds the exponential backoff between retry
	// attempts. Without it the tight 3-attempt loop can burn the entire
	// retry budget in microseconds and miss a transient SQLITE_BUSY that
	// would have cleared in milliseconds (Copilot review on PR #77). The
	// per-attempt delay doubles each time and is capped at maxDelay; jitter
	// is drawn from [0, baseDelay) so concurrent retriers don't synchronise
	// onto the same retry instant. Worst-case total backoff before falling
	// to the observable failure path is bounded by (attempts-1) × maxDelay,
	// so the budget can't deadlock lifecycle processing.
	endAgentEnqueueBaseDelay = 10 * time.Millisecond
	endAgentEnqueueMaxDelay  = 200 * time.Millisecond
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

func endAgentReviewPolicy(task *sqlstore.TaskRecord) (service.ReviewPolicy, error) {
	return service.EffectiveReviewPolicyFromJSON(task.Metadata)
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
		Description:          endAgentDescription(target.ID),
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
		if attempt < endAgentEnqueueMaxAttempts {
			// Backoff between retries with exponential progression + jitter.
			// Gives the writer lock a chance to clear instead of burning the
			// budget on back-to-back failures (Copilot review on PR #77).
			backoff := endAgentEnqueueBaseDelay << (attempt - 1)
			if backoff > endAgentEnqueueMaxDelay {
				backoff = endAgentEnqueueMaxDelay
			}
			backoff += time.Duration(rand.Int63n(int64(endAgentEnqueueBaseDelay)))
			time.Sleep(backoff)
		}
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

// endAgentDescription is an end-agent task's description. It names no
// reviewer version: the template stamped as the task's system prompt
// carries the protocol (V2 today, and an operator's own template can
// differ), and a description that said "V1 reviewer" while the template
// said V2 had a reviewer stop to ask which one to follow instead of
// auditing (CW-20261001-0187).
func endAgentDescription(targetID string) string {
	return "Disposition audit for " + targetID + " (reviewer end-agent)."
}

// loadEndAgentTemplate resolves the reviewer template content. Lookup
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

// endAgentClosedTag is the tag the end-agent's clean-audit path puts on its
// target (default-end-agent.md, Step 1).
const endAgentClosedTag = "agent-closed"

// isEndAgentCommentAuthor reports whether a comment's author is the
// end-agent's. The stored author has two shapes. Through the run's loopback
// it is stored as given, `[system/end-agent]`, which is also the author of
// Torque's own lifecycle comments. Through mux it is caller-suffixed,
// `[system/end-agent]-<8 hex>` (every audit comment since 2026-09-14 on the
// live DB: 46 of 46), and those comments' content does not carry the prefix
// ("Audit complete — …"). Only the author prefix matches both
// (CW-20261001-0195).
func isEndAgentCommentAuthor(author string) bool {
	return strings.HasPrefix(author, EndAgentAuthor)
}

// hasEndAgentMetadata reports whether a task's metadata carries the
// `end_agent` key enqueueEndAgent stamps on every end-agent task.
func hasEndAgentMetadata(meta sql.NullString) bool {
	if !meta.Valid {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(meta.String), &m) != nil {
		return false
	}
	_, ok := m["end_agent"]
	return ok
}

// shouldCheckEndAgentAudit reports whether a transition is an end-agent task
// finishing as `done`, the one outcome that says nothing about whether the
// audit happened (CW-20261001-0195).
func shouldCheckEndAgentAudit(task *sqlstore.TaskRecord, newStatus string) bool {
	if newStatus != "done" || task.Kind != "internal" {
		return false
	}
	if !task.ParentID.Valid || task.ParentID.String == "" {
		return false
	}
	return hasEndAgentMetadata(task.Metadata)
}

// endAgentAuditSkipped reports whether an end-agent task that finished `done`
// left its target's audit undone: the target is still in `review`, carries no
// `agent-closed` tag, and has no comment by the end-agent posted at or after
// the end-agent task was created. Every path of the template ends in one of
// those: a clean audit tags and closes the target, and findings leave a
// comment trail. A comment from an earlier review round does not count.
func endAgentAuditSkipped(endAgent, target *sqlstore.TaskRecord, targetTags []string, targetComments []sqlstore.CommentRecord) bool {
	if target.Status != "review" || slices.Contains(targetTags, endAgentClosedTag) {
		return false
	}
	for _, c := range targetComments {
		if isEndAgentCommentAuthor(c.Author) && !c.CreatedAt.Before(endAgent.CreatedAt) {
			return false
		}
	}
	return true
}

// commentSkippedEndAgentAudit posts a `[system/end-agent]` comment on the
// target of an end-agent that finished `done` without auditing it: such a run
// (a reviewer that answered with a question and made no tool calls, run 1194)
// left its target in `review` with nothing to show the audit never happened.
// Best effort, like commentEndAgentFailure: a read error is logged and
// nothing is posted, so a clean audit is never flagged on a failed read. The
// comment's own author counts as an end-agent comment on a later check, so a
// target is flagged once.
func (lm *LifecycleManager) commentSkippedEndAgentAudit(endAgent *sqlstore.TaskRecord) {
	targetID := endAgent.ParentID.String
	target, err := lm.store.GetTask(targetID)
	if err != nil {
		log.Printf("[end-agent] audit check for target=%s: read target: %v", targetID, err)
		return
	}
	if target.Status != "review" {
		return
	}
	tags, err := lm.store.ListTaskTags(targetID)
	if err != nil {
		log.Printf("[end-agent] audit check for target=%s: read tags: %v", targetID, err)
		return
	}
	slugs := make([]string, 0, len(tags))
	for _, t := range tags {
		slugs = append(slugs, t.Slug)
	}
	comments, err := lm.store.ListComments(targetID)
	if err != nil {
		log.Printf("[end-agent] audit check for target=%s: read comments: %v", targetID, err)
		return
	}
	if !endAgentAuditSkipped(endAgent, target, slugs, comments) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), endAgentFailureCommentTimeout)
	defer cancel()
	if err := lm.telemetry.AddComment(ctx, &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   targetID,
		Author:     EndAgentAuthor,
		Content: EndAgentAuthor + " " + endAgent.ID + " finished without recording an audit" +
			" — target stays at `review`; human follow-up required.",
	}); err != nil {
		log.Printf("[end-agent] add skipped-audit comment for target=%s: %v", targetID, err)
	}
}

// shouldCommentEndAgentFailure reports whether a transition deserves a
// failure comment. Fires on internal tasks with a parent_id that
// transition to a non-success terminal — `blocked` or `failed`. `done`
// is the success path (the reviewer succeeded — its own audit comments
// stand on their own, which commentSkippedEndAgentAudit checks it left).
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

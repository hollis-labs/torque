package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// Resolver handles agent-assisted merge conflict resolution.
// When auto-resolve encounters a conflict, it gathers context from all
// related tasks, creates a resolution task at P1 priority, and evaluates
// the result against a confidence threshold.
type Resolver struct {
	store    *sqlstore.Store
	mergeCfg config.MergeConfig
}

// ResolutionInput is the input for building a resolution request.
type ResolutionInput struct {
	SourceTaskID  string
	SourceBranch  string
	TargetBranch  string
	WorktreePath  string
	ConflictFiles []string
	ProjectID     string
}

// EvaluationResult is the verdict on a resolution attempt.
type EvaluationResult struct {
	Accept bool
	Reason string
}

// NewResolver creates a merge conflict resolver.
func NewResolver(store *sqlstore.Store, mergeCfg config.MergeConfig) *Resolver {
	return &Resolver{
		store:    store,
		mergeCfg: mergeCfg,
	}
}

// BuildResolutionRequest gathers context from the source task and all
// related tasks that touched the same project, then assembles a
// ResolutionRequest with everything the resolution agent needs.
func (r *Resolver) BuildResolutionRequest(ctx context.Context, input ResolutionInput) (*ResolutionRequest, error) {
	// Get the source task for its description and context.
	sourceTask, err := r.store.GetTask(input.SourceTaskID)
	if err != nil {
		return nil, fmt.Errorf("get source task: %w", err)
	}

	// Find related tasks — tasks in the same project that are done or doing.
	relatedTasks, err := r.store.ListTasks(sqlstore.TaskFilter{
		ProjectID: input.ProjectID,
	})
	if err != nil {
		return nil, fmt.Errorf("list related tasks: %w", err)
	}

	var relatedIDs []string
	var relatedBranches []string
	relatedDescs := make(map[string]string)

	for _, t := range relatedTasks {
		if t.ID == input.SourceTaskID {
			continue
		}
		if t.Status == "done" || t.Status == "doing" || t.Status == "review" {
			relatedIDs = append(relatedIDs, t.ID)
			relatedBranches = append(relatedBranches, "clockwork/"+t.ID)
			relatedDescs[t.ID] = t.Description
		}
	}

	return &ResolutionRequest{
		SourceTaskID:        input.SourceTaskID,
		SourceBranch:        input.SourceBranch,
		TargetBranch:        input.TargetBranch,
		WorktreePath:        input.WorktreePath,
		ConflictFiles:       input.ConflictFiles,
		RelatedTaskIDs:      relatedIDs,
		RelatedBranches:     relatedBranches,
		SourceDescription:   sourceTask.Description,
		RelatedDescriptions: relatedDescs,
		ConfidenceThreshold: r.mergeCfg.ConfidenceThreshold,
	}, nil
}

// CreateResolutionTask creates a new task in the system that will be
// dispatched immediately (P1 priority) to resolve the merge conflict.
// The task has OnFail=block to prevent infinite recursion, and
// OnDoneMerge=none to avoid re-triggering the merge pipeline.
func (r *Resolver) CreateResolutionTask(ctx context.Context, req *ResolutionRequest) (*sqlstore.TaskRecord, error) {
	id, err := r.store.NextTaskID()
	if err != nil {
		return nil, fmt.Errorf("generate task ID: %w", err)
	}

	// Build the description with full context.
	description := r.buildResolutionDescription(req)

	// Build the system prompt with conflict context.
	systemPrompt := r.buildResolutionSystemPrompt(req)

	// Marshal conflict files for the Files field.
	filesJSON, _ := json.Marshal(req.ConflictFiles)

	// Build metadata with resolution context.
	metadata := map[string]interface{}{
		"resolution_for":       req.SourceTaskID,
		"related_tasks":        req.RelatedTaskIDs,
		"confidence_threshold": req.ConfidenceThreshold,
		"conflict_files":       req.ConflictFiles,
		"source_branch":        req.SourceBranch,
		"target_branch":        req.TargetBranch,
	}
	metadataJSON, _ := json.Marshal(metadata)

	// Build deliverables: diff and test-results required.
	deliverables := `[{"type":"diff","required":true},{"type":"test-results","required":true}]`

	record := &sqlstore.TaskRecord{
		ID:           id,
		Title:        fmt.Sprintf("Resolve merge conflicts: %s -> %s", req.SourceTaskID, req.TargetBranch),
		Description:  description,
		Status:       "todo",
		Priority:     1, // P1 — queue jump for immediate dispatch
		Executor:     r.mergeCfg.ResolutionExecutor,
		AgentProfile: r.mergeCfg.ResolutionAgent,
		WorkingDir:   req.WorktreePath,
		SystemPrompt: systemPrompt,
		OnDone:       "close", // Auto-close on success
		OnFail:       "block", // Block on failure — no infinite recursion
		OnReview:     "pause",
		OnDoneMerge:  "none", // Resolution task does not re-trigger merge
		MaxRetries:   r.mergeCfg.MaxResolutionAttempts,
	}

	record.Files.String = string(filesJSON)
	record.Files.Valid = true
	record.Metadata.String = string(metadataJSON)
	record.Metadata.Valid = true
	record.Deliverables.String = deliverables
	record.Deliverables.Valid = true

	if err := r.store.CreateTask(record); err != nil {
		return nil, fmt.Errorf("create resolution task: %w", err)
	}

	// Attach default tags so merge-resolution tasks can be filtered/triaged.
	// Uses CreateTagIfNotExists so we don't collide with any previously-created
	// versions of these tags.
	defaultTagSlugs := []string{"merge-resolution", "auto-generated"}
	defaultTagNames := map[string]string{
		"merge-resolution": "Merge Resolution",
		"auto-generated":   "Auto Generated",
	}
	for _, slug := range defaultTagSlugs {
		if err := r.store.CreateTagIfNotExists(&sqlstore.TagRecord{
			Slug:  slug,
			Name:  defaultTagNames[slug],
			Color: "zinc",
		}); err != nil {
			return nil, fmt.Errorf("ensure resolution tag %q: %w", slug, err)
		}
	}
	if err := r.store.SetTaskTags(id, defaultTagSlugs); err != nil {
		return nil, fmt.Errorf("link resolution task tags: %w", err)
	}

	return r.store.GetTask(id)
}

// EvaluateResolution determines whether to accept a resolution result.
// The criteria are: resolution succeeded, confidence >= threshold, and tests passed.
func (r *Resolver) EvaluateResolution(result ResolutionResult) EvaluationResult {
	if !result.Success {
		return EvaluationResult{
			Accept: false,
			Reason: "resolution failed",
		}
	}

	if !result.TestsPassed {
		return EvaluationResult{
			Accept: false,
			Reason: "tests failed after resolution",
		}
	}

	if result.Confidence < r.mergeCfg.ConfidenceThreshold {
		return EvaluationResult{
			Accept: false,
			Reason: fmt.Sprintf("confidence %.2f below threshold %.2f", result.Confidence, r.mergeCfg.ConfidenceThreshold),
		}
	}

	return EvaluationResult{
		Accept: true,
	}
}

// buildResolutionDescription creates a detailed prompt for the resolution agent.
func (r *Resolver) buildResolutionDescription(req *ResolutionRequest) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Resolve merge conflicts when merging branch `%s` into `%s`.\n\n", req.SourceBranch, req.TargetBranch))

	sb.WriteString("## Conflicting Files\n\n")
	for _, f := range req.ConflictFiles {
		sb.WriteString(fmt.Sprintf("- `%s`\n", f))
	}

	sb.WriteString(fmt.Sprintf("\n## Original Task Intent (%s)\n\n", req.SourceTaskID))
	sb.WriteString(req.SourceDescription)
	sb.WriteString("\n")

	if len(req.RelatedTaskIDs) > 0 {
		sb.WriteString("\n## Related Tasks (same project, recent)\n\n")
		for _, id := range req.RelatedTaskIDs {
			desc := req.RelatedDescriptions[id]
			sb.WriteString(fmt.Sprintf("### %s\n%s\n\n", id, desc))
		}
	}

	sb.WriteString("\n## Instructions\n\n")
	sb.WriteString("1. Examine the conflict markers in each file\n")
	sb.WriteString("2. Understand the intent of both the source and target changes\n")
	sb.WriteString("3. Resolve conflicts preserving both intents where possible\n")
	sb.WriteString("4. Run tests to verify the resolution\n")
	sb.WriteString("5. Report your confidence level (0.0-1.0) in the resolution\n")

	return sb.String()
}

// buildResolutionSystemPrompt creates the system prompt with conflict context.
func (r *Resolver) buildResolutionSystemPrompt(req *ResolutionRequest) string {
	var sb strings.Builder

	sb.WriteString("You are a merge conflict resolution agent. Your job is to resolve git merge conflicts.\n\n")
	sb.WriteString("IMPORTANT RULES:\n")
	sb.WriteString("- Preserve the intent of BOTH sets of changes when possible\n")
	sb.WriteString("- If the changes are incompatible, prefer the source branch changes (the new feature)\n")
	sb.WriteString("- Run ALL tests after resolution\n")
	sb.WriteString("- Report confidence as a float between 0.0 and 1.0\n")
	sb.WriteString(fmt.Sprintf("- Confidence must be >= %.2f for auto-acceptance\n", req.ConfidenceThreshold))
	sb.WriteString("- If you cannot confidently resolve, report low confidence so a human can review\n")
	sb.WriteString("\nWhen complete, post your resolution summary + confidence via the clockwork_task_summary MCP tool.\n")

	return sb.String()
}

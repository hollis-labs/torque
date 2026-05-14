package scheduler

import (
	"context"

	"github.com/hollis-labs/torque/internal/concurrency"
	"github.com/hollis-labs/torque/internal/worktree"
)

// ConcurrencyHooks defines the integration points between the scheduler
// and the concurrency/worktree subsystems. The scheduler calls these
// at the appropriate lifecycle moments.
type ConcurrencyHooks struct {
	WorktreeManager *worktree.Manager
	Merger          *worktree.Merger
	Resolver        *worktree.Resolver
	WriteBuffer     concurrency.WriteBuffer
}

// OnRunStarted is called when a task transitions to doing and a run begins.
// It creates a worktree for the execution if the task has a WorkingDir.
func (h *ConcurrencyHooks) OnRunStarted(ctx context.Context, taskID string, runID int64, projectID string, repoPath string) (*worktree.WorktreeRecord, error) {
	if repoPath == "" {
		return nil, nil // No repo — no worktree needed
	}
	return h.WorktreeManager.Create(ctx, worktree.CreateRequest{
		TaskID:    taskID,
		RunID:     runID,
		ProjectID: projectID,
		RepoPath:  repoPath,
	})
}

// OnRunCompleted is called when execution finishes. It handles merge policy.
func (h *ConcurrencyHooks) OnRunCompleted(ctx context.Context, taskID string, mergePolicy worktree.MergePolicy, repoPath string, cleanupPolicy worktree.CleanupPolicy) (*worktree.MergeResult, error) {
	wt, err := h.WorktreeManager.Get(ctx, taskID)
	if err != nil {
		return nil, nil // No worktree — nothing to merge
	}

	// Execute merge policy
	result, err := h.Merger.Execute(ctx, worktree.MergeRequest{
		Policy:       mergePolicy,
		RepoPath:     repoPath,
		SourceBranch: wt.Branch,
		TargetBranch: "main",
		WorktreePath: wt.Path,
	})
	if err != nil {
		return nil, err
	}

	// Handle cleanup based on policy
	if result.Success && cleanupPolicy == worktree.CleanupOnMerge {
		_ = h.WorktreeManager.Cleanup(ctx, taskID, repoPath)
	}

	// If auto-resolve and conflicts, create resolution task
	if !result.Success && result.NeedsResolution && mergePolicy == worktree.MergePolicyAutoResolve && h.Resolver != nil {
		resReq, err := h.Resolver.BuildResolutionRequest(ctx, worktree.ResolutionInput{
			SourceTaskID:  taskID,
			SourceBranch:  wt.Branch,
			TargetBranch:  "main",
			WorktreePath:  wt.Path,
			ConflictFiles: result.ConflictFiles,
			ProjectID:     wt.ProjectID,
		})
		if err == nil {
			_, _ = h.Resolver.CreateResolutionTask(ctx, resReq)
		}
	}

	return result, nil
}

// OnTaskDone is called when a task reaches done status.
// Handles on_done cleanup policy.
func (h *ConcurrencyHooks) OnTaskDone(ctx context.Context, taskID string, repoPath string, cleanupPolicy worktree.CleanupPolicy) {
	if cleanupPolicy == worktree.CleanupOnDone {
		_ = h.WorktreeManager.Cleanup(ctx, taskID, repoPath)
	}
}

// BufferEvent pushes a high-frequency event to the write buffer.
func (h *ConcurrencyHooks) BufferEvent(ctx context.Context, event concurrency.BufferedEvent) error {
	if h.WriteBuffer == nil {
		return nil
	}
	return h.WriteBuffer.Push(ctx, event)
}

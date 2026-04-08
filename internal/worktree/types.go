package worktree

import (
	"database/sql"
	"time"
)

// MergePolicy determines what happens to a task's branch when it completes.
type MergePolicy string

const (
	MergePolicyNone        MergePolicy = "none"         // Leave branch, human decides
	MergePolicyAuto        MergePolicy = "auto"         // Attempt merge, block on conflict
	MergePolicyPR          MergePolicy = "pr"           // Create PR automatically
	MergePolicyAutoResolve MergePolicy = "auto-resolve" // Attempt merge, spawn resolution agent on conflict
)

// CleanupPolicy determines when worktrees are removed.
type CleanupPolicy string

const (
	CleanupOnMerge CleanupPolicy = "on_merge" // Clean up after successful merge
	CleanupOnDone  CleanupPolicy = "on_done"  // Clean up when task reaches done status
	CleanupManual  CleanupPolicy = "manual"   // Never auto-cleanup
)

// WorktreeRecord tracks an active or historical worktree in the database.
type WorktreeRecord struct {
	ID        int64
	TaskID    string
	RunID     sql.NullInt64
	ProjectID string
	Branch    string
	Path      string
	Status    string // "active", "merged", "cleaned", "conflict"
	CreatedAt time.Time
	CleanedAt *time.Time
}

// WorktreeConfig configures worktree behavior for the manager.
type WorktreeConfig struct {
	// MaxPerProject is the max concurrent worktrees per project. Default: 3.
	MaxPerProject int

	// CleanupPolicy determines when worktrees are removed. Default: on_merge.
	CleanupPolicy CleanupPolicy

	// DefaultMergePolicy is the merge policy for tasks without one set. Default: none.
	DefaultMergePolicy MergePolicy

	// WorktreeBaseDir is where worktrees are created, relative to project root.
	// Default: ".clockwork/worktrees"
	WorktreeBaseDir string
}

// DefaultWorktreeConfig returns sensible defaults.
func DefaultWorktreeConfig() WorktreeConfig {
	return WorktreeConfig{
		MaxPerProject:      3,
		CleanupPolicy:      CleanupOnMerge,
		DefaultMergePolicy: MergePolicyNone,
		WorktreeBaseDir:    ".clockwork/worktrees",
	}
}

// MergeResult captures the outcome of a merge attempt.
type MergeResult struct {
	Success         bool
	ConflictFiles   []string
	MergedCommit    string
	ErrorMessage    string
	NeedsResolution bool
}

// ResolutionRequest is created when auto-resolve encounters a conflict.
// It contains everything the resolution agent needs.
type ResolutionRequest struct {
	// SourceTaskID is the task whose branch has conflicts.
	SourceTaskID string

	// SourceBranch is the branch that failed to merge.
	SourceBranch string

	// TargetBranch is the branch being merged into (e.g., "main").
	TargetBranch string

	// WorktreePath is the worktree path with the conflict state.
	WorktreePath string

	// ConflictFiles lists the files with merge conflicts.
	ConflictFiles []string

	// RelatedTaskIDs are tasks that touched the same project in this cycle.
	RelatedTaskIDs []string

	// RelatedBranches are the branches of related tasks.
	RelatedBranches []string

	// SourceDescription is the original task's description (for intent).
	SourceDescription string

	// RelatedDescriptions maps taskID -> description for related tasks.
	RelatedDescriptions map[string]string

	// ConfidenceThreshold is the minimum confidence for auto-accept.
	ConfidenceThreshold float64
}

// ResolutionResult captures the outcome of an agent-assisted resolution.
type ResolutionResult struct {
	Success      bool
	Confidence   float64
	TaskID       string   // ID of the resolution task
	TestsPassed  bool
	ErrorMessage string
	Artifacts    []string // Artifact IDs attached
}

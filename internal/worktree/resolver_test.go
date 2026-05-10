package worktree_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/testutil/sqlitetest"
	"github.com/hollis-labs/clockwork-manifold/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupResolverStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	return sqlitetest.OpenStore(t)
}

func TestBuildResolutionRequest(t *testing.T) {
	store := setupResolverStore(t)

	// Set up source task
	store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260407-0001",
		Title:       "Add user validation",
		Description: "Validate email format in user registration endpoint",
		Executor:    "cli",
		WorkingDir:  "/projects/myapp",
		ProjectID:   sql.NullString{String: "proj-1", Valid: true},
	})

	// Set up related task (same project, recent)
	store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260407-0002",
		Title:       "Add user avatar upload",
		Description: "Allow users to upload profile images",
		Executor:    "cli",
		WorkingDir:  "/projects/myapp",
		ProjectID:   sql.NullString{String: "proj-1", Valid: true},
		Status:      "done",
	})

	mergeCfg := config.MergeConfig{
		ResolutionExecutor:    "cli",
		ResolutionAgent:       "default",
		ConfidenceThreshold:   0.8,
		MaxResolutionAttempts: 1,
		NotifyOnConflict:      true,
	}

	resolver := worktree.NewResolver(store, mergeCfg)
	ctx := context.Background()

	req, err := resolver.BuildResolutionRequest(ctx, worktree.ResolutionInput{
		SourceTaskID:  "CW-20260407-0001",
		SourceBranch:  "clockwork/CW-20260407-0001",
		TargetBranch:  "main",
		WorktreePath:  "/projects/myapp/.clockwork/worktrees/CW-20260407-0001",
		ConflictFiles: []string{"pkg/user/validate.go", "pkg/user/handler.go"},
		ProjectID:     "proj-1",
	})
	require.NoError(t, err)

	assert.Equal(t, "CW-20260407-0001", req.SourceTaskID)
	assert.Equal(t, "clockwork/CW-20260407-0001", req.SourceBranch)
	assert.Equal(t, "main", req.TargetBranch)
	assert.Equal(t, []string{"pkg/user/validate.go", "pkg/user/handler.go"}, req.ConflictFiles)
	assert.Equal(t, 0.8, req.ConfidenceThreshold)
	assert.Contains(t, req.SourceDescription, "Validate email format")
	assert.Contains(t, req.RelatedTaskIDs, "CW-20260407-0002")
}

func TestCreateResolutionTask(t *testing.T) {
	store := setupResolverStore(t)

	// Source task
	store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260407-0001",
		Title:       "Fix validation",
		Description: "Fix the email validation logic",
		Executor:    "cli",
		WorkingDir:  "/projects/myapp",
		ProjectID:   sql.NullString{String: "proj-1", Valid: true},
	})

	mergeCfg := config.MergeConfig{
		ResolutionExecutor:    "cli",
		ResolutionAgent:       "resolver-profile",
		ConfidenceThreshold:   0.9,
		MaxResolutionAttempts: 1,
		NotifyOnConflict:      true,
	}

	resolver := worktree.NewResolver(store, mergeCfg)
	ctx := context.Background()

	resReq := &worktree.ResolutionRequest{
		SourceTaskID:        "CW-20260407-0001",
		SourceBranch:        "clockwork/CW-20260407-0001",
		TargetBranch:        "main",
		WorktreePath:        "/projects/myapp/.clockwork/worktrees/CW-20260407-0001",
		ConflictFiles:       []string{"pkg/user/validate.go"},
		RelatedTaskIDs:      []string{},
		SourceDescription:   "Fix the email validation logic",
		ConfidenceThreshold: 0.9,
	}

	task, err := resolver.CreateResolutionTask(ctx, resReq)
	require.NoError(t, err)

	assert.Contains(t, task.ID, "CW-")
	assert.Contains(t, task.Title, "Resolve merge conflicts")
	assert.Contains(t, task.Title, "CW-20260407-0001")
	assert.Equal(t, 1, task.Priority) // P1 — queue jump
	assert.Equal(t, "todo", task.Status)
	assert.Equal(t, "cli", task.Executor)
	assert.Equal(t, "resolver-profile", task.AgentProfile)
	assert.Equal(t, "block", task.OnFail)     // No infinite recursion
	assert.Equal(t, "none", task.OnDoneMerge) // Resolution task does not re-trigger merge
	assert.Contains(t, task.Description, "pkg/user/validate.go")
	assert.Contains(t, task.Description, "CW-20260407-0001")

	// Default tags should be attached so merge-resolution tasks can be filtered/triaged
	linked, err := store.ListTaskTags(task.ID)
	require.NoError(t, err)
	slugs := make([]string, len(linked))
	for i, tg := range linked {
		slugs[i] = tg.Slug
	}
	assert.ElementsMatch(t, []string{"merge-resolution", "auto-generated"}, slugs)
}

func TestEvaluateResolution(t *testing.T) {
	resolver := worktree.NewResolver(nil, config.MergeConfig{
		ConfidenceThreshold: 0.8,
	})

	// Confident + tests pass -> accept
	result := resolver.EvaluateResolution(worktree.ResolutionResult{
		Success:     true,
		Confidence:  0.95,
		TestsPassed: true,
	})
	assert.True(t, result.Accept)
	assert.Empty(t, result.Reason)

	// Confident but tests fail -> reject
	result = resolver.EvaluateResolution(worktree.ResolutionResult{
		Success:     true,
		Confidence:  0.95,
		TestsPassed: false,
	})
	assert.False(t, result.Accept)
	assert.Contains(t, result.Reason, "tests failed")

	// Not confident -> reject
	result = resolver.EvaluateResolution(worktree.ResolutionResult{
		Success:     true,
		Confidence:  0.6,
		TestsPassed: true,
	})
	assert.False(t, result.Accept)
	assert.Contains(t, result.Reason, "confidence")

	// Resolution failed entirely -> reject
	result = resolver.EvaluateResolution(worktree.ResolutionResult{
		Success: false,
	})
	assert.False(t, result.Accept)
	assert.Contains(t, result.Reason, "failed")
}

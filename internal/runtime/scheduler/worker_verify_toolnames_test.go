package scheduler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tool-name spellings below are the ones the runtimes actually put on the
// wire, not guesses:
//   - claude: tallied from Torque's own stream.jsonl sidecars
//     (~/.torque/workspaces/*/*/logs) — Bash, Edit, Write, Read, Grep, …
//   - codex: the same sidecars; codex_events.go already emits Bash / Edit
//   - opencode: OpenCode's session store (part.data.tool) — bash, edit,
//     write, apply_patch, read, todowrite, grep, glob, list
func TestCanonicalToolName(t *testing.T) {
	cases := []struct {
		runtime string
		name    string
		want    string
	}{
		{"claude", "Edit", "Edit"},
		{"claude", "Write", "Write"},
		{"claude", "Bash", "Bash"},
		{"claude", "MultiEdit", "MultiEdit"},
		{"claude", "NotebookEdit", "NotebookEdit"},
		{"codex", "Bash", "Bash"},
		{"codex", "Edit", "Edit"},
		{"opencode", "bash", "Bash"},
		{"opencode", "edit", "Edit"},
		{"opencode", "write", "Write"},
		{"opencode", "apply_patch", "Edit"},
		{"any", "multiedit", "MultiEdit"},
		{"any", "BASH", "Bash"},

		// Not editing tools: returned unchanged, never folded onto one.
		{"claude", "Read", "Read"},
		{"opencode", "read", "read"},
		{"opencode", "todowrite", "todowrite"},
		{"opencode", "grep", "grep"},
		{"opencode", "vanta_memory_write", "vanta_memory_write"},
		{"claude", "mcp__loopback__torque_task_update", "mcp__loopback__torque_task_update"},
	}
	for _, tc := range cases {
		t.Run(tc.runtime+"/"+tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, canonicalToolName(tc.name))
		})
	}
}

func TestReadToolHistogram_CanonicalizesRuntimeSpellings(t *testing.T) {
	logDir := writeStreamJSONL(t, []toolUseEntry{
		{Tool: "edit"}, {Tool: "Edit"}, {Tool: "apply_patch"},
		{Tool: "bash"}, {Tool: "write"}, {Tool: "todowrite"}, {Tool: "read"},
	})
	hist, ok := readToolHistogram(logDir)
	assert.True(t, ok)
	assert.Equal(t, map[string]int{"Edit": 3, "Bash": 1, "Write": 1, "todowrite": 1, "read": 1}, hist)
}

// An OpenCode worker that edited files but never committed reads as
// edits-without-commits, same as a claude worker: the worktree diff decides,
// and the reason carries the canonical tool names.
func TestVerifyWorkerCompletion_OpenCodeEditsWithoutCommits(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	dirtyWorktree(t, worktreePath)
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "read"}, {Tool: "edit"}, {Tool: "bash"}})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictFailedNoCommitsWithEdits, verdict.Kind)
	assert.Contains(t, verdict.Reason, "Edit=1")
}

// An OpenCode worker that only read and kept a todo list left a clean
// worktree: nothing to commit, and its tool activity counts as action.
func TestVerifyWorkerCompletion_OpenCodeReadOnlyCleanTree(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "read"}, {Tool: "todowrite"}, {Tool: "grep"}})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	assert.Equal(t, VerdictPassedNoEditsExpected, verdict.Kind)
}

package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every Contains below asserts that a value passed IN reached the generated
// output — an interpolation check, which is behaviour. Three hand-typed prose
// assertions were removed 2026-09-11 (`work_root` is the authoritative
// writable workspace, different `working_dir` or `repo_root`, and Do not pass
// a `task_id`): they pinned the template's wording, so a legitimate rewording
// went red and an improvement was indistinguishable from a deletion. Nothing
// they covered is unverified — the interpolations below still prove the
// template rendered with the right values.
//
// If a claim about the WORDING of planted guidance seems worth asserting, the
// wording is not the thing under test. See agent-setup's
// what-a-check-may-assert.md.
func TestTaskContextNativeFiles_PlantsMarkdownAndJSON(t *testing.T) {
	files := taskContextNativeFiles(taskContextInput{
		AgentProfileName: "implementer",
		Role:             "worker",
		SessionID:        "sess-1",
		LoopbackURL:      "http://127.0.0.1:1234/mcp",
		Options: Options{
			TaskID:       "CW-T-1",
			TaskTitle:    "Plant task context",
			TaskKind:     "agent",
			TaskStatus:   "doing",
			TaskPriority: 2,
			ProjectID:    "PRJ-1",
			ParentID:     "CW-PARENT",
			DependsOn:    []string{"CW-DEP"},
			RunID:        99,
			Workdir:      "/work",
			RepoRoot:     "/repo",
			Description:  "Do the assigned work.",
			SessionMeta:  map[string]string{"plan_id": "CW-PLAN"},
			Metadata: map[string]any{
				"origin": "test",
				"secret": "do-not-plant",
				"project_context": map[string]any{
					"name":          "Torque",
					"repo_path":     "/repo",
					"context_paths": []any{"docs/runtime.md"},
					"artifacts": []any{
						map[string]any{
							"file_path": "docs/overview.md",
							"metadata":  map[string]any{"secret": "do-not-plant"},
						},
					},
				},
			},
		},
	})

	require.Len(t, files, 4)
	assert.Equal(t, taskBundleReadmePath, files[0].RelPath)
	assert.Contains(t, files[0].Content, "`CW-T-1/task.md`")
	assert.Equal(t, "tasks/CW-T-1/task.md", files[1].RelPath)
	assert.Contains(t, files[1].Content, "task_id: `CW-T-1`")
	assert.Contains(t, files[1].Content, "project_id: `PRJ-1`")
	assert.Contains(t, files[1].Content, "context_paths: `docs/runtime.md`")
	assert.Contains(t, files[1].Content, "artifact_paths: `docs/overview.md`")
	assert.Equal(t, "tasks/CW-T-1/task.json", files[2].RelPath)
	assert.Equal(t, "tasks/CW-T-1/process.md", files[3].RelPath)
	assert.Contains(t, files[3].Content, "Authoritative workspace: `/work`")
	assert.Contains(t, files[3].Content, "$TORQUE_WORK_ROOT")
	assert.Contains(t, files[3].Content, "torque_task_review")

	var decoded plantedTaskContext
	require.NoError(t, json.Unmarshal([]byte(files[2].Content), &decoded))
	assert.Equal(t, "CW-T-1", decoded.TaskID)
	assert.Equal(t, "Plant task context", decoded.Title)
	assert.Equal(t, int64(99), decoded.RunID)
	assert.Equal(t, "sess-1", decoded.SessionID)
	assert.Equal(t, "/work", decoded.WorkRoot)
	assert.Equal(t, "/repo", decoded.RepoRoot)
	assert.Equal(t, []string{"CW-DEP"}, decoded.DependsOn)
	assert.Equal(t, "CW-PLAN", decoded.SessionMeta["plan_id"])
	assert.Equal(t, "Torque", decoded.ProjectContext["name"])
	assert.NotContains(t, decoded.ProjectContext, "permissions")
	assert.NotContains(t, decoded.ProjectContext, "metadata")
	assert.NotContains(t, string(files[2].Content), "do-not-plant")
}

func TestTaskContextNativeFiles_SkipsWhenNoTask(t *testing.T) {
	files := taskContextNativeFiles(taskContextInput{
		AgentProfileName: "planner",
		Options:          Options{Workdir: "/work"},
	})
	assert.Nil(t, files)
}

func TestTaskContextNativeFiles_SanitizesTaskIDPathSegment(t *testing.T) {
	files := taskContextNativeFiles(taskContextInput{
		AgentProfileName: "implementer",
		Options: Options{
			TaskID:  "../bad/task",
			Workdir: "/work",
		},
	})

	require.Len(t, files, 4)
	assert.Equal(t, "tasks/"+safeTaskBundleSegment("../bad/task")+"/task.md", files[1].RelPath)
	assert.Contains(t, files[0].Content, "task_id: `../bad/task`")
	assert.Contains(t, files[1].Content, "task_id: `../bad/task`")
}

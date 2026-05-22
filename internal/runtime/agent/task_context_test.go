package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskContextNativeFiles_PlantsMarkdownAndJSON(t *testing.T) {
	files := taskContextNativeFiles(buildLaunchPlanInput{
		AgentProfile: "implementer",
		Role:         "worker",
		SessionID:    "sess-1",
		LoopbackURL:  "http://127.0.0.1:1234/mcp",
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
				"project_context": map[string]any{
					"name":          "Torque",
					"repo_path":     "/repo",
					"context_paths": []any{"docs/runtime.md"},
					"artifacts": []any{
						map[string]any{"file_path": "docs/overview.md"},
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
	assert.Contains(t, files[1].Content, "Machine-readable copy: `task.json`")
	assert.Equal(t, "tasks/CW-T-1/task.json", files[2].RelPath)
	assert.Equal(t, "tasks/CW-T-1/process.md", files[3].RelPath)
	assert.Contains(t, files[3].Content, "torque_task_review")
	assert.Contains(t, files[3].Content, "Do not pass a `task_id`")

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
	assert.Equal(t, "test", decoded.Metadata["origin"])
}

func TestTaskContextNativeFiles_SkipsWhenNoTask(t *testing.T) {
	files := taskContextNativeFiles(buildLaunchPlanInput{
		AgentProfile: "planner",
		Options:      Options{Workdir: "/work"},
	})
	assert.Nil(t, files)
}

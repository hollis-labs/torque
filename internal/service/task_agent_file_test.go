package service_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

func writeAgentYAML(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// CW-20260417-0082: Create rejects a task whose agent_file points at a file
// that doesn't exist so bad references surface at mutation time.
func TestTaskCreate_AgentFileMissingRejected(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:     "with-missing-agent-file",
		AgentFile: "/nonexistent/path/to/agent.yaml",
	})
	require.Error(t, err)
	var ve *service.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "agent_file", ve.Field)
}

func TestTaskCreate_AgentFileAbsolutePersisted(t *testing.T) {
	svc := setupService(t)

	dir := t.TempDir()
	abs := writeAgentYAML(t, dir, "agent.yaml", "system_prompt: hello\n")

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:     "with-agent-file",
		AgentFile: abs,
	})
	require.NoError(t, err)
	require.Equal(t, abs, task.AgentFile)
}

func TestTaskCreate_AgentFileRelativeResolvesAgainstWorkingDir(t *testing.T) {
	svc := setupService(t)

	dir := t.TempDir()
	writeAgentYAML(t, dir, "agent.yaml", "system_prompt: hello\n")

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:      "relative-agent-file",
		WorkingDir: dir,
		AgentFile:  "agent.yaml",
	})
	require.NoError(t, err)
	require.Equal(t, "agent.yaml", task.AgentFile, "store preserves the raw relative path; resolve happens at dispatch")
}

func TestTaskCreate_AgentFileRelativeWithoutWorkingDirRejected(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:     "bad-relative",
		AgentFile: "agents/backend.yaml",
	})
	require.Error(t, err)
	var ve *service.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "agent_file", ve.Field)
}

func TestTaskCreate_EmptyAgentFileNoOp(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title: "no-agent-file",
	})
	require.NoError(t, err)
	assert.Equal(t, "", task.AgentFile)
}

// Update rejects when the new agent_file doesn't exist on disk. The existing
// row is left untouched.
func TestTaskUpdate_AgentFileMissingRejected(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "t"})
	require.NoError(t, err)

	newVal := "/definitely/missing.yaml"
	upd := service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{AgentFile: &newVal}}
	err = svc.Task.Update(task.ID, upd)
	require.Error(t, err)
	var ve *service.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "agent_file", ve.Field)

	// Row must still have the old (empty) value.
	got, err := svc.Task.Get(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got.AgentFile)
}

func TestTaskUpdate_AgentFileValidAccepted(t *testing.T) {
	svc := setupService(t)

	dir := t.TempDir()
	abs := writeAgentYAML(t, dir, "agent.yaml", "system_prompt: hi\n")

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "t"})
	require.NoError(t, err)

	upd := service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{AgentFile: &abs}}
	require.NoError(t, svc.Task.Update(task.ID, upd))

	got, err := svc.Task.Get(task.ID)
	require.NoError(t, err)
	assert.Equal(t, abs, got.AgentFile)
}

// Update resolves a newly-set relative path against the pre-existing
// working_dir column so callers don't have to re-send working_dir just to
// change agent_file.
func TestTaskUpdate_RelativeAgentFileUsesExistingWorkingDir(t *testing.T) {
	svc := setupService(t)

	dir := t.TempDir()
	writeAgentYAML(t, dir, "agent.yaml", "system_prompt: hi\n")

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:      "t",
		WorkingDir: dir,
	})
	require.NoError(t, err)

	rel := "agent.yaml"
	upd := service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{AgentFile: &rel}}
	require.NoError(t, svc.Task.Update(task.ID, upd))

	got, err := svc.Task.Get(task.ID)
	require.NoError(t, err)
	assert.Equal(t, rel, got.AgentFile)
}

// Clearing the agent_file by passing an explicit empty string must succeed
// even if the old value was valid — callers disable the feature this way.
func TestTaskUpdate_AgentFileClearing(t *testing.T) {
	svc := setupService(t)

	dir := t.TempDir()
	abs := writeAgentYAML(t, dir, "agent.yaml", "system_prompt: hi\n")

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "t", AgentFile: abs})
	require.NoError(t, err)
	require.Equal(t, abs, task.AgentFile)

	empty := ""
	upd := service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{AgentFile: &empty}}
	require.NoError(t, svc.Task.Update(task.ID, upd))

	got, err := svc.Task.Get(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got.AgentFile)
}

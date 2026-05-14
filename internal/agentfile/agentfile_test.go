package agentfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/agentfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestLoad_MinimalRequiresSystemPrompt(t *testing.T) {
	p := writeYAML(t, `system_prompt: "hello"
`)
	af, err := agentfile.Load(p)
	require.NoError(t, err)
	assert.Equal(t, "hello", af.SystemPrompt)
	assert.Empty(t, af.Name)
	assert.Empty(t, af.Model)
	assert.Nil(t, af.Tools)
	assert.Nil(t, af.Environment)
}

func TestLoad_FullSpec(t *testing.T) {
	p := writeYAML(t, `name: backend-refactorer
description: Go refactoring specialist
system_prompt: |
  You are a Go refactoring specialist.
  Work in small diffs.
tools: [Read, Edit, Bash]
model: claude-opus-4-7
environment:
  GOFLAGS: -race
  FOO: bar
`)
	af, err := agentfile.Load(p)
	require.NoError(t, err)
	assert.Equal(t, "backend-refactorer", af.Name)
	assert.Equal(t, "Go refactoring specialist", af.Description)
	assert.Contains(t, af.SystemPrompt, "You are a Go refactoring specialist.")
	assert.Contains(t, af.SystemPrompt, "Work in small diffs.")
	assert.Equal(t, []string{"Read", "Edit", "Bash"}, af.Tools)
	assert.Equal(t, "claude-opus-4-7", af.Model)
	assert.Equal(t, map[string]string{"GOFLAGS": "-race", "FOO": "bar"}, af.Environment)
}

// Only system_prompt is required; missing it should be an error so callers
// don't silently ship an empty prompt.
func TestLoad_MissingSystemPromptErrors(t *testing.T) {
	p := writeYAML(t, `name: empty-agent
`)
	_, err := agentfile.Load(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "system_prompt")
}

func TestLoad_InvalidYAMLErrors(t *testing.T) {
	p := writeYAML(t, "system_prompt: [unterminated\n")
	_, err := agentfile.Load(p)
	require.Error(t, err)
}

func TestLoad_FileMissingErrors(t *testing.T) {
	_, err := agentfile.Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.Error(t, err)
}

// Resolve turns a task's agent_file (absolute or working_dir-relative) into
// an absolute path. An absolute path is returned as-is; a relative one is
// joined against the given working_dir. An empty agent_file returns "" with
// no error (caller's signal that no agent file is set).
func TestResolve_Absolute(t *testing.T) {
	abs := "/tmp/foo/agent.yaml"
	out, err := agentfile.Resolve(abs, "/some/working/dir")
	require.NoError(t, err)
	assert.Equal(t, abs, out)
}

func TestResolve_RelativeJoinsWorkingDir(t *testing.T) {
	out, err := agentfile.Resolve("agents/backend.yaml", "/srv/app")
	require.NoError(t, err)
	assert.Equal(t, "/srv/app/agents/backend.yaml", out)
}

func TestResolve_RelativeWithoutWorkingDirErrors(t *testing.T) {
	_, err := agentfile.Resolve("agents/backend.yaml", "")
	require.Error(t, err, "relative path with no working_dir should error")
}

func TestResolve_EmptyPath(t *testing.T) {
	out, err := agentfile.Resolve("", "/srv/app")
	require.NoError(t, err)
	assert.Equal(t, "", out)
}

// Validate is the create/update-time check: path resolves, file exists, is
// readable. It does NOT parse contents — that's deferred to dispatch.
func TestValidate_ExistingReadable(t *testing.T) {
	p := writeYAML(t, `system_prompt: "x"
`)
	require.NoError(t, agentfile.Validate(p, ""))
}

func TestValidate_Missing(t *testing.T) {
	err := agentfile.Validate(filepath.Join(t.TempDir(), "missing.yaml"), "")
	require.Error(t, err)
}

func TestValidate_RelativeResolvesAgainstWorkingDir(t *testing.T) {
	dir := t.TempDir()
	rel := "agent.yaml"
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte("system_prompt: x\n"), 0o600))

	require.NoError(t, agentfile.Validate(rel, dir))

	err := agentfile.Validate(rel, "")
	require.Error(t, err, "relative path with no working_dir must error")
}

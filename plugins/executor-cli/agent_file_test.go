package executorcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeAgent(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// CW-20260417-0082: a task whose agent_file points at a missing path at
// dispatch time must produce a blocked run with a helpful reason — not a
// system error. The operator fixes the file and re-runs the task.
func TestRun_AgentFileMissingBlocksRun(t *testing.T) {
	pm := profiles("default", shellProfile(`echo hi`))
	e := New(pm)

	j := job("default")
	j.AgentFile = "/definitely/not/here/agent.yaml"

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "blocked", result.Status)
	assert.Contains(t, result.Reason, "agent")
}

// Agent file environment vars reach the subprocess. Task-level env vars
// override agent-level ones when the same key is set in both (per-task wins).
func TestRun_AgentFileEnvMergedWithTaskEnv(t *testing.T) {
	dir := t.TempDir()
	agent := writeAgent(t, dir, "agent.yaml", `system_prompt: hello
environment:
  AGENT_ONLY: from-agent
  OVERLAPPING: from-agent
`)

	// Script prints the three env vars so we can assert propagation and
	// precedence ordering.
	script := `echo "AGENT_ONLY=${AGENT_ONLY}"
echo "OVERLAPPING=${OVERLAPPING}"
echo "TASK_ONLY=${TASK_ONLY}"
echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)

	j := job("default")
	j.AgentFile = agent
	j.Environment = map[string]string{
		"TASK_ONLY":   "from-task",
		"OVERLAPPING": "from-task",
	}

	var lines []string
	_, err := e.Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		if ev.Type == executor.EventLog {
			lines = append(lines, ev.Content)
		}
	})
	require.NoError(t, err)

	out := strings.Join(lines, "\n")
	assert.Contains(t, out, "AGENT_ONLY=from-agent", "agent-only var should reach subprocess")
	assert.Contains(t, out, "TASK_ONLY=from-task", "task-only var should reach subprocess")
	assert.Contains(t, out, "OVERLAPPING=from-task", "task env must win on key conflict")
	assert.NotContains(t, out, "OVERLAPPING=from-agent")
}

// Agent file without an Environment block should not regress the existing
// "task env still reaches subprocess" contract.
func TestRun_AgentFileWithNoEnvDoesNotBreakTaskEnv(t *testing.T) {
	dir := t.TempDir()
	agent := writeAgent(t, dir, "agent.yaml", "system_prompt: hi\n")

	script := `echo "TASK=${TASK}"
echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)

	j := job("default")
	j.AgentFile = agent
	j.Environment = map[string]string{"TASK": "yes"}

	var lines []string
	_, err := e.Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		if ev.Type == executor.EventLog {
			lines = append(lines, ev.Content)
		}
	})
	require.NoError(t, err)
	assert.Contains(t, strings.Join(lines, "\n"), "TASK=yes")
}

// Relative agent_file resolves against job.WorkingDir.
func TestRun_AgentFileRelativeResolvesAgainstWorkingDir(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "backend.yaml", "system_prompt: hi\n")

	script := `echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)

	j := job("default")
	j.WorkingDir = dir
	j.AgentFile = "backend.yaml"

	result, err := e.Run(context.Background(), j, nil)
	require.NoError(t, err)
	assert.Equal(t, "done", result.Status)
}

// Secrets in agent.environment are filtered just like secrets in task env.
func TestRun_AgentFileSecretStillFiltered(t *testing.T) {
	dir := t.TempDir()
	agent := writeAgent(t, dir, "agent.yaml", `system_prompt: hi
environment:
  OPENAI_API_KEY: sk-leaked-from-agent
`)

	script := `echo "key=${OPENAI_API_KEY}"`
	pm := profiles("default", config.AgentProfile{
		Command:      "sh",
		Args:         []string{"-c", script},
		OutputFormat: "print",
	})
	e := New(pm)

	j := job("default")
	j.AgentFile = agent

	var lines []string
	_, err := e.Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		if ev.Type == executor.EventLog {
			lines = append(lines, ev.Content)
		}
	})
	require.NoError(t, err)
	assert.NotContains(t, strings.Join(lines, "\n"), "sk-leaked-from-agent",
		"secret-looking keys from agent file must be stripped, same as task env")
}

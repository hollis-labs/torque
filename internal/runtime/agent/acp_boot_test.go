package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// The servers an ACP session is meant to get are the loopback and mux
// entries a native boot dir plants, under the same names.
func TestACPMCPServers(t *testing.T) {
	deps := &Dependencies{MuxCommand: "/usr/bin/mux", MuxArgs: []string{"mcp"}, MuxEnv: []string{"MUX_TOKEN=x"}}
	got := acpMCPServers("http://127.0.0.1:4321/mcp", deps)
	assert.Equal(t, []provider.MCPServerSpec{
		{Name: "loopback", HTTPURL: "http://127.0.0.1:4321/mcp"},
		{Name: "mux", Command: "/usr/bin/mux", Args: []string{"mcp"}, Env: []string{"MUX_TOKEN=x"}},
	}, got)

	assert.Empty(t, acpMCPServers("", &Dependencies{}), "no loopback and no mux: nothing to send")
	assert.Equal(t, []provider.MCPServerSpec{{Name: "loopback", HTTPURL: "http://l/mcp"}}, acpMCPServers("http://l/mcp", nil))
}

func TestACPKickoff(t *testing.T) {
	opts := Options{TaskID: "CW-1", Workdir: "/work", Description: "do the thing"}
	bundle := taskContextNativeFiles(taskContextInput{Options: opts, SessionID: "S", Role: "worker", LoopbackURL: "http://l/mcp"})
	require.NotEmpty(t, bundle)

	got := acpKickoff(opts, "worker", bundle, false)
	assert.True(t, strings.HasPrefix(got, kickoffHeader(opts, "worker")), "same header as the planted boot.md")
	assert.Contains(t, got, "# Assigned Task")
	assert.Contains(t, got, "# Worker Process")
	assert.NotContains(t, got, "# Planted Torque Tasks", "the README describes planted files, not this turn")
	assert.NotContains(t, got, `"task_id"`, "task.json is not inlined; task.md carries the same facts")
	assert.NotContains(t, got, kickoffLoopbackTools)
	assert.Contains(t, got, "Torque's task-scoped MCP tools are not available in this session")
	assert.True(t, strings.HasSuffix(got, kickoffFirstTurn(opts)))

	withTools := acpKickoff(opts, "worker", bundle, true)
	assert.Contains(t, withTools, kickoffLoopbackTools)
	assert.NotContains(t, withTools, "not available in this session")

	noTask := acpKickoff(Options{Description: "hi"}, "worker", nil, false)
	assert.NotContains(t, noTask, "task bundle follows")
}

// Every Validate job is a task run, so pi is refused at enqueue as a
// permanent error rather than spending a run on a worker that cannot report.
func TestExecutorValidate_RefusesPiTaskRuns(t *testing.T) {
	deps := &Dependencies{Profiles: config.ProfileMap{
		"pi-worker":      {Executor: "cli", Provider: "pi"},
		"copilot-worker": {Executor: "cli", Provider: "copilot"},
	}}
	e := NewExecutor(deps)

	err := e.Validate(&executor.ExecutionJob{TaskID: "CW-1", AgentProfile: "pi-worker"})
	require.Error(t, err)
	var perm *executor.PermanentError
	assert.True(t, errors.As(err, &perm), "a runtime that cannot report is permanent, not retryable: %v", err)
	assert.Contains(t, err.Error(), "pi-acp cannot reach Torque's loopback MCP")

	assert.NoError(t, e.Validate(&executor.ExecutionJob{TaskID: "CW-2", AgentProfile: "copilot-worker"}))
}

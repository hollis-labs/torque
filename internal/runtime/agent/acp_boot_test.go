package agent

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// The servers an ACP session gets are the loopback and mux entries a native
// boot dir plants, under the same names.
func TestACPMCPServers(t *testing.T) {
	deps := &Dependencies{MuxCommand: "/usr/bin/mux", MuxArgs: []string{"mcp"}, MuxEnv: []string{"MUX_TOKEN=x"}}
	got := acpMCPServers("http://127.0.0.1:4321/mcp", deps)
	assert.Equal(t, []acp.MCPServer{
		{Name: "loopback", URL: "http://127.0.0.1:4321/mcp"},
		{Name: "mux", Command: "/usr/bin/mux", Args: []string{"mcp"}, Env: map[string]string{"MUX_TOKEN": "x"}},
	}, got)
	_, _, err := acp.SessionMCPServers(got, true)
	require.NoError(t, err, "the wrapper accepts the set as built")

	assert.Empty(t, acpMCPServers("", &Dependencies{}), "no loopback and no mux: nothing to send")
	assert.Equal(t, []acp.MCPServer{{Name: "loopback", URL: "http://l/mcp"}}, acpMCPServers("http://l/mcp", nil))
}

// The loopback-dropped signal comes from go-agent-wrapper's own diagnostic
// for an agent without mcpCapabilities.http, read the way the wrapper
// writes it.
func TestACPDiagnostics_LoopbackDroppedAndLogged(t *testing.T) {
	var reported []acp.Diagnostic
	acp.ReportSkippedMCPServers(func(d acp.Diagnostic) { reported = append(reported, d) }, []string{"loopback", "other"})
	require.Len(t, reported, 1)
	assert.Equal(t, []string{"loopback", "other"}, acpSkippedMCPServers(reported[0]))

	var log strings.Builder
	d := &acpDiagnostics{w: &log}
	d.observe(acp.NewDiagnostic(acp.DiagnosticStderr, "ACP child stderr", "warming up"))
	assert.False(t, d.loopbackDropped.Load(), "stderr is logged, not read as a drop")
	d.observe(reported[0])
	assert.True(t, d.loopbackDropped.Load())
	assert.Equal(t,
		"acp stderr: ACP child stderr: warming up\n"+
			"acp protocol: agent does not accept HTTP MCP servers (no mcpCapabilities.http); not sent: loopback, other\n",
		log.String())

	other := &acpDiagnostics{w: io.Discard}
	acp.ReportSkippedMCPServers(other.observe, []string{"notes"})
	assert.False(t, other.loopbackDropped.Load(), "only the loopback's drop matters")
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

// Every Validate job is a task run, so pi, whose bridge never passes MCP
// servers on, is refused at enqueue as a permanent error. Other ACP agents
// are judged at launch from the capability they report, so a Copilot task
// run validates in either mode.
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

	assert.NoError(t, e.Validate(&executor.ExecutionJob{TaskID: "CW-2", Kind: "agent", AgentProfile: "copilot-worker"}))
	assert.NoError(t, e.Validate(&executor.ExecutionJob{TaskID: "CW-3", AgentProfile: "copilot-worker"}))
}

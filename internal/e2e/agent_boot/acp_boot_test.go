package agent_boot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// Copilot and Pi run only over ACP; these boot them through agent.Boot on
// the production path (go-agent-wrapper, RuntimeFactory nil) against the
// go-providers fakes, which replay captured ACP transcripts
// (CW-20261001-0097).

type acpTestLoopback struct{ url string }

func (l acpTestLoopback) URL() string                    { return l.url }
func (l acpTestLoopback) Shutdown(context.Context) error { return nil }

func composeACPDeps(t *testing.T, provider string) *composedDeps {
	t.Helper()
	cd := composeDeps(t, fakeRuntimeConfig{}, provider)
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: provider}}
	cd.Deps.Loopback = func(string, string) (agent.LoopbackHandle, error) {
		return acpTestLoopback{url: "http://127.0.0.1:9/mcp"}, nil
	}
	return cd
}

// acpPrompts returns the text of each session/prompt the fake received.
func acpPrompts(t *testing.T, call providertest.Call) []string {
	t.Helper()
	var out []string
	for _, line := range call.Stdin {
		var frame struct {
			Method string `json:"method"`
			Params struct {
				Prompt []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(line), &frame) != nil || frame.Method != "session/prompt" {
			continue
		}
		var text []string
		for _, block := range frame.Params.Prompt {
			text = append(text, block.Text)
		}
		out = append(out, strings.Join(text, ""))
	}
	return out
}

// acpSessionNewServers returns the mcpServers the fake received in
// session/new.
func acpSessionNewServers(t *testing.T, call providertest.Call) []map[string]any {
	t.Helper()
	for _, line := range call.Stdin {
		var frame struct {
			Method string `json:"method"`
			Params struct {
				MCPServers []map[string]any `json:"mcpServers"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(line), &frame) == nil && frame.Method == "session/new" {
			return frame.Params.MCPServers
		}
	}
	t.Fatalf("no session/new in the fake's stdin")
	return nil
}

// acpSessionLog reads the one session.log under the deps' workspaces root.
func acpSessionLog(t *testing.T, cd *composedDeps) string {
	t.Helper()
	logs, err := filepath.Glob(filepath.Join(cd.Deps.WorkspacesRoot, "*", "*", "logs", "session.log"))
	require.NoError(t, err)
	require.Len(t, logs, 1, "one session, one session.log")
	b, err := os.ReadFile(logs[0])
	require.NoError(t, err)
	return string(b)
}

// TestBootCopilotACP_OneShot: a scheduler-dispatched copilot run boots over
// acp-stdio, session/new carries the loopback (Copilot advertises
// mcpCapabilities.http), and its single turn carries the kickoff with the
// task bundle inline, since an ACP session has no boot dir to plant it in.
func TestBootCopilotACP_OneShot(t *testing.T) {
	fake := providertest.New(t, runtimes.Copilot, providertest.Replay("copilot/acp_turn"))
	fake.Install()
	cd := composeACPDeps(t, "copilot")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-ACP-ONESHOT", TaskTitle: "Say hello", RunID: 7, AgentProfile: "worker",
		Workdir: t.TempDir(), Mode: agent.ModeOneShot, Description: "say hello",
	})
	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, sess.Status, "the replayed copilot turn must complete the one-shot run")
	assert.Equal(t, string(runtimes.ModeACPStdio), sess.RuntimeKind)
	assert.Empty(t, sess.BootDir, "an ACP session plants no boot dir")

	calls := fake.Calls()
	require.Len(t, calls, 1, "the wrapper spawns copilot once")
	assert.True(t, calls[0].HasArg("--acp"), "argv: %v", calls[0].Args)
	prompts := acpPrompts(t, calls[0])
	require.Len(t, prompts, 1)
	first := prompts[0]
	assert.Contains(t, first, "**Task ID:** `CW-ACP-ONESHOT`")
	assert.Contains(t, first, "# Assigned Task", "task.md rides the first turn")
	assert.Contains(t, first, "# Worker Process", "process.md rides the first turn")
	assert.Contains(t, first, "## First turn\n\nsay hello")
	assert.NotContains(t, first, "already planted under the boot dir")
	assert.Contains(t, first, "Use the `loopback` MCP server's task-scoped tools", "the session has the loopback, so the kickoff points at it")
	assert.Equal(t, []map[string]any{
		{"type": "http", "name": "loopback", "url": "http://127.0.0.1:9/mcp", "headers": []any{}},
	}, acpSessionNewServers(t, calls[0]))
}

// TestBootCopilotACP_MuxOnlyUnderBypass is CW-20261001-0120's posture gate,
// extending CW-20261001-0110 to ACP: an ACP session is offered the daemon's
// mux MCP server only under bypassPermissions. Every other posture, unset
// included, gets the run's loopback alone.
func TestBootCopilotACP_MuxOnlyUnderBypass(t *testing.T) {
	loopback := map[string]any{"type": "http", "name": "loopback", "url": "http://127.0.0.1:9/mcp", "headers": []any{}}
	mux := map[string]any{"name": "mux", "command": "/usr/local/bin/mux", "args": []any{"mcp"}, "env": []any{map[string]any{"name": "MUX_TOKEN", "value": "x"}}}
	for _, tc := range []struct {
		permissionMode string
		want           []map[string]any
	}{
		{"", []map[string]any{loopback}},
		{"default", []map[string]any{loopback}},
		{"acceptEdits", []map[string]any{loopback}},
		{"plan", []map[string]any{loopback}},
		{"bypassPermissions", []map[string]any{loopback, mux}},
	} {
		t.Run("mode="+tc.permissionMode, func(t *testing.T) {
			fake := providertest.New(t, runtimes.Copilot, providertest.Replay("copilot/acp_turn"))
			fake.Install()
			cd := composeACPDeps(t, "copilot")
			cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "copilot", PermissionMode: tc.permissionMode}}
			cd.Deps.MuxCommand = "/usr/local/bin/mux"
			cd.Deps.MuxArgs = []string{"mcp"}
			cd.Deps.MuxEnv = []string{"MUX_TOKEN=x"}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			sess, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID: "CW-ACP-MUX", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeOneShot, Description: "say hello",
			})
			require.NoError(t, err)
			assert.Equal(t, agent.StatusDone, sess.Status)
			require.Len(t, fake.Calls(), 1)
			assert.Equal(t, tc.want, acpSessionNewServers(t, fake.Calls()[0]))
		})
	}
}

// A profile's mux_servers narrows the mux an ACP session gets under
// bypassPermissions, and never gets it outside bypassPermissions
// (CW-20261001-0226).
func TestBootCopilotACP_MuxServersNarrowUnderBypass(t *testing.T) {
	for _, tc := range []struct {
		permissionMode string
		wantMuxArgs    []string // nil: no mux
	}{
		{"bypassPermissions", []string{"mcp", "--proxy", "--servers", "tesseract", "--token", "local-dev"}},
		{"acceptEdits", nil},
	} {
		t.Run("mode="+tc.permissionMode, func(t *testing.T) {
			fake := providertest.New(t, runtimes.Copilot, providertest.Replay("copilot/acp_turn"))
			fake.Install()
			cd := composeACPDeps(t, "copilot")
			cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "copilot", PermissionMode: tc.permissionMode, MuxServers: []string{"tesseract"}}}
			cd.Deps.MuxCommand = "/usr/local/bin/mux"
			cd.Deps.MuxArgs = []string{"mcp", "--proxy", "--servers", "vanta,torque,cerberus", "--token", "local-dev"}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID: "CW-ACP-MUX-NARROW", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeOneShot, Description: "say hello",
			})
			require.NoError(t, err)
			require.Len(t, fake.Calls(), 1)
			var muxArgv []any
			for _, srv := range acpSessionNewServers(t, fake.Calls()[0]) {
				if srv["name"] == "mux" {
					muxArgv, _ = srv["args"].([]any)
				}
			}
			if tc.wantMuxArgs == nil {
				assert.Empty(t, muxArgv, "outside bypassPermissions mux is not offered")
				return
			}
			got := make([]string, len(muxArgv))
			for i, a := range muxArgv {
				got[i], _ = a.(string)
			}
			assert.Equal(t, tc.wantMuxArgs, got)
		})
	}
}

// TestBootCopilotACP_LongLivedSendTurn: a long-lived copilot session gets
// its kickoff as the first prompt, and a later SendTurn reaches the same
// ACP session as a second session/prompt.
func TestBootCopilotACP_LongLivedSendTurn(t *testing.T) {
	steps := providertest.FixtureSteps(t, "copilot/acp_turn")
	require.GreaterOrEqual(t, len(steps), 2)
	steps = steps[:len(steps)-2] // the captured eof + exit
	sid := "00000000-0000-4000-8000-000000000001"
	steps = append(steps,
		providertest.Recv(`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"`+sid+`","prompt":[{"type":"text","text":"second"}]}}`),
		providertest.Send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"`+sid+`","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ok"}}}}`),
		providertest.Send(`{"jsonrpc":"2.0","id":4,"result":{"stopReason":"end_turn"}}`),
		providertest.AwaitEOF(),
		providertest.Exit(0),
	)
	fake := providertest.New(t, runtimes.Copilot, providertest.Script(steps...))
	fake.Install()
	cd := composeACPDeps(t, "copilot")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-ACP-LONG", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
	})
	require.NoError(t, err)
	assert.Equal(t, string(runtimes.ModeACPStdio), sess.RuntimeKind)

	// ACP takes one prompt at a time; the kickoff turn has to finish first.
	require.Eventually(t, func() bool {
		return cd.Manager.SendTurn(ctx, sess, "second turn") == nil
	}, 10*time.Second, 50*time.Millisecond, "SendTurn must reach the ACP session once the kickoff turn ends")
	require.Eventually(t, func() bool {
		calls := fake.Calls()
		return len(calls) == 1 && len(acpPrompts(t, calls[0])) == 2
	}, 10*time.Second, 50*time.Millisecond, "the fake must receive the second session/prompt")

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	require.NoError(t, cd.Manager.Stop(stopCtx, sess.ID))

	prompts := acpPrompts(t, fake.Calls()[0])
	assert.Contains(t, prompts[0], "**Task ID:** `CW-ACP-LONG`", "the kickoff is the first prompt")
	assert.Equal(t, "second turn", prompts[1], "a later turn is sent as given")
}

// TestBootPiACP_RefusedForTaskRun: pi-acp drops the MCP servers it is given,
// so a scheduler-dispatched worker could never reach the loopback to report.
// Boot refuses the run before spawning anything.
func TestBootPiACP_RefusedForTaskRun(t *testing.T) {
	fake := providertest.New(t, runtimes.Pi)
	fake.Install()
	cd := composeACPDeps(t, "pi")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-PI-RUN", RunID: 42, AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeOneShot,
		Description: "say hello",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, agent.ErrAdapterNotFound), "a runtime/dispatch mismatch is a configuration error: %v", err)
	assert.Contains(t, err.Error(), "pi-acp cannot reach Torque's loopback MCP (mcpCapabilities.http=false); a task worker could not comment or signal review")
	assert.Empty(t, fake.Calls(), "nothing is spawned for a refused run")
}

// TestBootCopilotACP_LongLivedTaskRunRefusedWithoutHTTPMCP: an agent that
// does not advertise mcpCapabilities.http is not sent the loopback, so a
// long-lived task worker on it could not signal review. Boot stops it once
// session/new has shown that, and the dropped server is in session.log.
func TestBootCopilotACP_LongLivedTaskRunRefusedWithoutHTTPMCP(t *testing.T) {
	fake := providertest.New(t, runtimes.Copilot, providertest.Script(
		providertest.Recv(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`),
		providertest.Send(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":false,"mcpCapabilities":{"http":false,"sse":false}},"authMethods":[]}}`),
		providertest.Recv(`{"jsonrpc":"2.0","id":2,"method":"session/new"}`),
		providertest.Send(`{"jsonrpc":"2.0","id":2,"result":{"sessionId":"no-http"}}`),
		providertest.AwaitEOF(),
		providertest.Exit(0),
	))
	fake.Install()
	cd := composeACPDeps(t, "copilot")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-ACP-NO-HTTP", RunID: 43, AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, agent.ErrAdapterNotFound), "%v", err)
	assert.Contains(t, err.Error(), "copilot does not accept HTTP MCP servers (no mcpCapabilities.http), so the loopback was not sent and a long-lived task worker could not signal review")

	calls := fake.Calls()
	require.Len(t, calls, 1)
	assert.Empty(t, acpSessionNewServers(t, calls[0]), "the wrapper sends an agent without http no HTTP server")
	assert.Empty(t, acpPrompts(t, calls[0]), "a refused run gets no kickoff")
	assert.Contains(t, acpSessionLog(t, cd), "acp protocol: agent does not accept HTTP MCP servers (no mcpCapabilities.http); not sent: loopback")
}

// TestBootPiACP_ManualSession: without a dispatching run, pi boots over
// ACP and completes a turn.
func TestBootPiACP_ManualSession(t *testing.T) {
	fake := providertest.New(t, runtimes.Pi, providertest.Replay("pi/acp_turn"))
	fake.Install()
	cd := composeACPDeps(t, "pi")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeOneShot, Description: "say hi",
	})
	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, sess.Status)
	assert.Equal(t, string(runtimes.ModeACPStdio), sess.RuntimeKind)
	require.Len(t, fake.Calls(), 1)
	prompts := acpPrompts(t, fake.Calls()[0])
	require.Len(t, prompts, 1)
	assert.Contains(t, prompts[0], "## First turn\n\nsay hi")
	// pi-acp advertises no mcpCapabilities.http: the loopback is not sent,
	// the kickoff says so, and session.log records the drop.
	assert.Contains(t, prompts[0], "Torque's task-scoped MCP tools are not available in this session")
	assert.Contains(t, acpSessionLog(t, cd), "not sent: loopback")
}

// acpPermissionAnswers returns the outcome of each answer the fake received
// to a session/request_permission it sent (go-providers' copilot/
// acp_permission fixture asks once, as request id 0).
func acpPermissionAnswers(t *testing.T, call providertest.Call) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range call.Stdin {
		var frame struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
			Result struct {
				Outcome map[string]any `json:"outcome"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &frame) != nil || frame.Method != "" || frame.ID == nil || string(*frame.ID) != "0" {
			continue
		}
		out = append(out, frame.Result.Outcome)
	}
	return out
}

// TestBootCopilotACP_PermissionRequestFollowsPosture is CW-20261001-0113:
// Copilot asks before running a shell command (`execute`). Torque answers
// from the profile's posture: declined (reject_once) under the default
// posture, allowed once under bypassPermissions, and each decision is in
// session.log.
func TestBootCopilotACP_PermissionRequestFollowsPosture(t *testing.T) {
	for _, tc := range []struct {
		permissionMode string
		wantOption     string
		wantLog        string
	}{
		{"", "reject_once", `acp permission: kind=execute tool="Run shell command: pwd" posture=default option=reject_once (execute is not granted under default)`},
		{"bypassPermissions", "allow_once", `acp permission: kind=execute tool="Run shell command: pwd" posture=yolo option=allow_once (execute is granted under yolo)`},
	} {
		t.Run("mode="+tc.permissionMode, func(t *testing.T) {
			fake := providertest.New(t, runtimes.Copilot, providertest.Replay("copilot/acp_permission"))
			fake.Install()
			cd := composeACPDeps(t, "copilot")
			cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "copilot", PermissionMode: tc.permissionMode}}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			sess, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID: "CW-ACP-PERMISSION", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeOneShot, Description: "Run pwd",
			})
			require.NoError(t, err)
			assert.Equal(t, agent.StatusDone, sess.Status)
			require.Len(t, fake.Calls(), 1)
			assert.Equal(t, []map[string]any{{"outcome": "selected", "optionId": tc.wantOption}}, acpPermissionAnswers(t, fake.Calls()[0]))
			assert.Contains(t, acpSessionLog(t, cd), tc.wantLog)
		})
	}
}

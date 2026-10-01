package agent_boot

import (
	"context"
	"encoding/json"
	"errors"
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

// TestBootCopilotACP_OneShot: a scheduler-dispatched copilot run boots over
// acp-stdio, and its single turn carries the kickoff with the task bundle
// inline, since an ACP session has no boot dir to plant it in.
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
	// The wrapper cannot hand the loopback to session/new yet, so the
	// kickoff must not send the worker looking for its tools.
	assert.Contains(t, first, "Torque's task-scoped MCP tools are not available in this session")
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

// TestBootCopilotACP_RefusedForLongLivedTaskRun: until go-agent-wrapper
// hands session/new Torque's MCP servers, a long-lived task worker on any
// ACP runtime could not signal review, so Boot refuses the run. One-shot
// runs (TestBootCopilotACP_OneShot) and manual sessions
// (TestBootCopilotACP_LongLivedSendTurn) still launch.
func TestBootCopilotACP_RefusedForLongLivedTaskRun(t *testing.T) {
	fake := providertest.New(t, runtimes.Copilot)
	fake.Install()
	cd := composeACPDeps(t, "copilot")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-ACP-LONG-RUN", RunID: 43, AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, agent.ErrAdapterNotFound), "%v", err)
	assert.Contains(t, err.Error(), "a long-lived task worker could not signal review; one-shot runs and manual sessions are allowed")
	assert.Empty(t, fake.Calls(), "nothing is spawned for a refused run")
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
}

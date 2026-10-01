package agent_boot

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// TestBootAntigravityThroughLaunchSelect boots an `agy` profile end to end
// on the production path (go-agent-wrapper, RuntimeFactory nil):
// launch.Select picks Antigravity's per-turn adapter (CW-20260930-0134),
// providerplant plants its boot dir, and the wrapper spawns the fake agy,
// which replays a captured turn. Before the registry-driven Select, Torque
// had no Antigravity adapter and refused the provider.
func TestBootAntigravityThroughLaunchSelect(t *testing.T) {
	fake := providertest.New(t, runtimes.Antigravity, providertest.Replay("antigravity/print_turn1"))
	fake.Install()

	cd := composeDeps(t, fakeRuntimeConfig{}, "agy")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "agy", Model: "probe-model", PermissionMode: "plan"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-AGY-SELECT", AgentProfile: "worker", Workdir: t.TempDir(),
		Mode: agent.ModeOneShot, Description: "say hello",
	})
	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, sess.Status, "the replayed agy turn must complete the one-shot run")
	assert.Equal(t, string(runtimes.ModeSubprocessPerTurn), sess.RuntimeKind)

	calls := fake.Calls()
	require.NotEmpty(t, calls, "the wrapper must spawn agy")
	args := calls[0].Args
	assert.Equal(t, "agy", filepath.Base(fake.Path))
	assert.True(t, slices.Contains(args, "stream-json"), "agy runs with --output-format stream-json: %v", args)
	// The profile's model and permission posture reach agy through the
	// adapter Torque configured (applyProfileOptions), which is what
	// providerplant plants the launch from.
	assert.Equal(t, "probe-model", argAfter(args, "--model"), "argv: %v", args)
	assert.Equal(t, "plan", argAfter(args, "--mode"), "argv: %v", args)
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// A denied-tool turn completes the one-shot run: agy's replayed turn, whose
// tool call was denied, still ends the run as done. Since agentkit v0.14.0
// the denial also writes a `[permission_denied:…]` marker to the byte
// Fanout and go-agent-wrapper v0.17.0 emits agent.permission_denied;
// TestWrapperSink_IgnoresUntranslatedKinds pins that the sink tolerates the
// kind (CW-20261001-0094).
func TestBootAntigravityToolDeniedTurnCompletes(t *testing.T) {
	fake := providertest.New(t, runtimes.Antigravity, providertest.Replay("antigravity/print_tool_denied"))
	fake.Install()

	cd := composeDeps(t, fakeRuntimeConfig{}, "agy")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "agy", Model: "probe-model", PermissionMode: "plan"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-AGY-DENIED", AgentProfile: "worker", Workdir: t.TempDir(),
		Mode: agent.ModeOneShot, Description: "write a file",
	})
	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, sess.Status, "a turn with a denied tool must still complete the run")
	require.Len(t, fake.Calls(), 1)
}

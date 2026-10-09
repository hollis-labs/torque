package agent_boot

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/harness/adapters/providertest"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// Since agentkit v0.17.0 the launch plan's Provider.Permission is a
// go-permission Mode, which PrepareExecution maps onto the provider's own
// flags (CW-20261001-0157). Torque sets it for claude-code only, so a claude
// launch carries `--permission-mode <claude's spelling>`, matching the
// planted settings.json's permissions.defaultMode, while codex, opencode and
// agy launches carry no posture flags or environment, as before: codex keeps
// go-providers' non-interactive `never` / `workspace-write` default.
func TestBoot_PermissionPostureReachesOnlyClaude(t *testing.T) {
	claude := []struct {
		name    string
		profile config.AgentProfile
		want    string
	}{
		{"unset is acceptEdits", config.AgentProfile{Executor: "cli", Provider: "claude-code"}, "acceptEdits"},
		{"default", config.AgentProfile{Executor: "cli", Provider: "claude-code", PermissionMode: "default"}, "default"},
		{"plan", config.AgentProfile{Executor: "cli", Provider: "claude-code", PermissionMode: "plan"}, "plan"},
		{"bypassPermissions", config.AgentProfile{Executor: "cli", Provider: "claude-code", PermissionMode: "bypassPermissions"}, "bypassPermissions"},
		{"developer mode", config.AgentProfile{Executor: "cli", Provider: "claude-code", Args: []string{"--dangerously-skip-permissions"}}, "bypassPermissions"},
	}
	for _, tc := range claude {
		t.Run("claude "+tc.name, func(t *testing.T) {
			args, _ := bootRecorded(t, runtimes.Claude, providertest.Script(providertest.AwaitEOF()), tc.profile)
			at := slices.Index(args, "--permission-mode")
			require.NotEqual(t, -1, at, "argv: %q", args)
			assert.Equal(t, tc.want, args[at+1])
			assert.Equal(t, 1, strings.Count(strings.Join(args, " "), "--permission-mode"), "one posture flag: %q", args)
		})
	}

	others := []struct {
		name    string
		id      runtimes.ID
		run     providertest.Run
		profile config.AgentProfile
	}{
		{"codex exec", runtimes.Codex, providertest.Replay("codex/exec_turn1"), config.AgentProfile{Executor: "cli", Provider: "codex", RuntimeKind: "subprocess-per-turn", PermissionMode: "acceptEdits"}},
		{"opencode run", runtimes.OpenCode, providertest.Replay("opencode/run_turn1"), config.AgentProfile{Executor: "cli", Provider: "opencode", PermissionMode: "plan"}},
		{"agy", runtimes.Antigravity, providertest.Replay("antigravity/print_turn1"), config.AgentProfile{Executor: "cli", Provider: "agy", PermissionMode: "acceptEdits"}},
	}
	for _, tc := range others {
		t.Run(tc.name+" gets no posture", func(t *testing.T) {
			args, env := bootRecorded(t, tc.id, tc.run, tc.profile)
			joined := strings.Join(args, " ")
			for _, flag := range []string{"--permission-mode", "sandbox_mode", "approval_policy"} {
				assert.NotContains(t, joined, flag, "argv: %q", args)
			}
			for _, kv := range env {
				assert.False(t, strings.HasPrefix(kv, "OPENCODE_PERMISSION="), "no posture environment: %s", kv)
			}
		})
	}
}

// bootRecorded boots profile long-lived on the production path against a
// recording fake and returns the first invocation's argv and environment.
func bootRecorded(t *testing.T, id runtimes.ID, run providertest.Run, profile config.AgentProfile) ([]string, []string) {
	t.Helper()
	fake := providertest.New(t, id, run)
	fake.ExpectErrors()
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, string(id))
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": profile}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-POSTURE", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 10*time.Second, 20*time.Millisecond)
	c := fake.Call(0)
	return c.Args, c.Env
}

// go-agent-wrapper before v0.21.1 could panic the host with "send on closed
// channel" when an ACP agent exited during launch (CW-20261001-0129): about
// one boot in four with an immediately exiting copilot. The ACP session runs
// in Torque's own process, so the panic took the daemon down. The boot must
// fail with an error instead; run it with -count=20 -race.
func TestBootCopilotACP_AgentExitingAtLaunchFailsTheBoot(t *testing.T) {
	fake := providertest.New(t, runtimes.Copilot, providertest.Script(providertest.Exit(1)))
	fake.ExpectErrors()
	fake.Install()
	cd := composeACPDeps(t, "copilot")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-ACP-EXIT", AgentProfile: "worker", Workdir: t.TempDir(),
		Mode: agent.ModeOneShot, Description: "say hello",
	})
	require.Error(t, err, "a copilot that exits during launch fails the boot")
}

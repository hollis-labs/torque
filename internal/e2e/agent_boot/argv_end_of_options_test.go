package agent_boot

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// Since go-providers v0.34.1 a launch that carries the turn's prompt in argv
// ends `-- <prompt>`, so untrusted turn text is never parsed as a flag
// (CW-20261001-0069). These boot each runtime Torque launches through its
// real (wrapper) path against a fake CLI that records argv, and check that
// every flag Torque or the profile contributes sits before the `--` and the
// prompt is the last argument (CW-20261001-0064).
func TestBoot_ArgvKeepsTorqueFlagsBeforeEndOfOptions(t *testing.T) {
	cases := []struct {
		name    string
		id      runtimes.ID
		fixture string
		profile config.AgentProfile
		// flags are arguments that must appear before any "--".
		flags []string
		// prompt reports whether this launch carries the prompt in argv.
		prompt bool
	}{
		{
			name: "codex exec", id: runtimes.Codex, fixture: "codex/exec_turn1",
			profile: config.AgentProfile{Executor: "cli", Provider: "codex", RuntimeKind: "subprocess-per-turn"},
			flags:   []string{"exec", "--json"},
			prompt:  true,
		},
		{
			name: "opencode run", id: runtimes.OpenCode, fixture: "opencode/run_turn1",
			profile: config.AgentProfile{Executor: "cli", Provider: "opencode", Model: "opencode/test-model"},
			flags:   []string{"run", "--agent", "--model", "opencode/test-model"},
			prompt:  true,
		},
		{
			name: "claude streaming", id: runtimes.Claude, fixture: "claude/stream_two_turns",
			profile: config.AgentProfile{Executor: "cli", Provider: "claude-code", Model: "claude-test-model", Args: []string{"--max-turns", "7"}},
			flags:   []string{"--max-turns", "7", "--model", "claude-test-model", "--input-format"},
			prompt:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := providertest.New(t, tc.id, providertest.Replay(tc.fixture))
			fake.ExpectErrors() // replay mismatches are not what this test checks
			fake.Install()
			cd := composeDeps(t, fakeRuntimeConfig{}, string(tc.id))
			cd.Deps.RuntimeFactory = nil
			cd.Deps.Profiles = config.ProfileMap{"worker": tc.profile}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-ARGV", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

			require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 10*time.Second, 20*time.Millisecond, "the fake %s was never launched", tc.id)
			args := fake.Call(0).Args
			t.Logf("argv: %q", args)
			end := slices.Index(args, "--")
			if tc.prompt {
				require.NotEqual(t, -1, end, "a launch carrying the prompt must end options with --: %q", args)
				require.Equal(t, len(args)-2, end, "the prompt must be the only argument after --: %q", args)
			}
			for _, flag := range tc.flags {
				i := slices.Index(args, flag)
				require.NotEqual(t, -1, i, "argv lacks %q: %q", flag, args)
				if end >= 0 {
					require.Less(t, i, end, "%q must come before --: %q", flag, args)
				}
			}
		})
	}
}

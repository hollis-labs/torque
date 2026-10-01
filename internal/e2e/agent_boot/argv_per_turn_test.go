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

// Before agentkit v0.13.0 a prepared launch ran every turn from the frozen
// first-turn argv: a subprocess-per-turn runtime (codex exec, opencode run)
// re-ran the kickoff on every later turn, so SendTurn text never reached the
// CLI (CW-20260930-0114 comment 6047), and codex exec dropped the profile's
// model and args (CW-20261001-0081). Each turn now resolves the prepared
// launch template. These boot each runtime through Torque's real (wrapper)
// path against a fake CLI that records argv, send a second turn, and check
// that it carries its own prompt, last and after `--`, with the session the
// first turn reported and the profile's flags, and that the command is not
// repeated (CW-20261001-0094).
func TestBoot_LaterTurnsCarryTheirOwnPrompt(t *testing.T) {
	cases := []struct {
		name     string
		id       runtimes.ID
		fixtures [2]string
		profile  config.AgentProfile
		// command is the subcommand that must appear exactly once.
		command string
		// flags must appear before "--" on every turn.
		flags []string
		// resume is the session id turn 1 reported, which turn 2 must carry;
		// resumeAfter, when set, is a flag turn 2 must carry before the resume.
		resume      string
		resumeAfter string
		// kickoff is what turn 1's prompt must contain: the `@./boot.md`
		// pointer where the CLI runs in the boot dir, boot.md's own content
		// where it runs in the project dir (CW-20261001-0104).
		kickoff string
	}{
		{
			name: "codex exec", id: runtimes.Codex, fixtures: [2]string{"codex/exec_turn1", "codex/exec_turn2_resume"},
			profile: config.AgentProfile{Executor: "cli", Provider: "codex", RuntimeKind: "subprocess-per-turn", Model: "codex-test-model", Args: []string{"--enable", "probe_feature"}},
			command: "exec",
			flags:   []string{`model="codex-test-model"`, "--enable", "probe_feature", "--json"},
			kickoff: "Boot @./boot.md",
			// go-providers v0.41.0: codex exec resumes its thread from turn 2,
			// `exec … --cd <dir> resume <thread> -- <prompt>`, with --cd in front
			// of `resume` (codex refuses it after the subcommand). That is in a
			// live session, in its own CODEX_HOME, where the thread exists.
			resume:      fixtureSessionID(t, "codex/exec_turn1"),
			resumeAfter: "--cd",
		},
		{
			name: "opencode run", id: runtimes.OpenCode, fixtures: [2]string{"opencode/run_turn1", "opencode/run_turn2_resume"},
			profile: config.AgentProfile{Executor: "cli", Provider: "opencode", Model: "opencode/test-model", Args: []string{"--log-level", "WARN"}},
			command: "run",
			flags:   []string{"--agent", "--model", "opencode/test-model", "--log-level", "WARN"},
			resume:  fixtureSessionID(t, "opencode/run_turn1"),
			kickoff: "**Task ID:** `CW-TEST-TURNS`",
		},
	}
	const turnTwo = "--turn-two: report status"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := providertest.New(t, tc.id, providertest.Replay(tc.fixtures[0]), providertest.Replay(tc.fixtures[1]))
			fake.Install()
			cd := composeDeps(t, fakeRuntimeConfig{}, string(tc.id))
			cd.Deps.RuntimeFactory = nil
			cd.Deps.Profiles = config.ProfileMap{"worker": tc.profile}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-TURNS", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
			require.Eventually(t, func() bool { return len(fake.Calls()) >= 1 && fake.Call(0).Exited }, 10*time.Second, 20*time.Millisecond, "turn 1 never ran to completion")

			require.NoError(t, cd.Manager.SendTurn(ctx, sess, turnTwo))
			require.Eventually(t, func() bool { return len(fake.Calls()) >= 2 }, 10*time.Second, 20*time.Millisecond, "turn 2 never launched the CLI")

			first, second := fake.Call(0).Args, fake.Call(1).Args
			t.Logf("turn 1: %.300q", first)
			t.Logf("turn 2: %q", second)
			require.Contains(t, first[len(first)-1], tc.kickoff, "turn 1 must carry the kickoff: %q", first)
			require.Equal(t, turnTwo, second[len(second)-1], "turn 2 must run its own prompt: %q", second)
			require.NotEqual(t, first[len(first)-1], second[len(second)-1], "turn 2 re-ran turn 1's prompt")
			if tc.resume != "" {
				require.Contains(t, second, tc.resume, "turn 2 must resume the session turn 1 reported: %q", second)
			}
			if tc.resumeAfter != "" {
				after, at := slices.Index(second, tc.resumeAfter), slices.Index(second, "resume")
				require.NotEqual(t, -1, after, "turn 2 lacks %q: %q", tc.resumeAfter, second)
				require.NotEqual(t, -1, at, "turn 2 lacks the resume subcommand: %q", second)
				require.Less(t, after, at, "%q must come before `resume`: %q", tc.resumeAfter, second)
			}
			for i, args := range [][]string{first, second} {
				end := slices.Index(args, "--")
				require.Equal(t, len(args)-2, end, "turn %d: the prompt must be the only argument after --: %q", i+1, args)
				require.Equal(t, 1, countArg(args, tc.command), "turn %d: the command must not repeat: %q", i+1, args)
				for _, flag := range tc.flags {
					at := slices.Index(args, flag)
					require.NotEqual(t, -1, at, "turn %d lacks %q: %q", i+1, flag, args)
					require.Less(t, at, end, "turn %d: %q must come before --: %q", i+1, flag, args)
				}
			}
		})
	}
}

// Claude streaming takes every turn, the first included, as a stdin frame.
// agentkit v0.13.0 sends a prepared launch's boot prompt as that first frame
// unless the caller fires its own, and one-shot Torque sends the kickoff
// itself after the session is up, so without bootWrapper clearing the
// prepared boot delivery a one-shot session got its kickoff twice
// (CW-20261001-0094). A long-lived session gets the kickoff once and then
// each later turn as its own frame, all on one process.
func TestBoot_ClaudeStreamingTurnsReachStdinOnce(t *testing.T) {
	profile := config.AgentProfile{Executor: "cli", Provider: "claude-code", Model: "claude-test-model", Args: []string{"--max-turns", "7"}}
	boot := func(t *testing.T, mode agent.Mode, description string) (*providertest.Fake, *composedDeps, *agent.Session) {
		// Answer the first frame with a result so a one-shot Boot sees its
		// turn complete; the fake records every later frame regardless.
		fake := providertest.New(t, runtimes.Claude, providertest.Script(
			providertest.RecvLine(),
			providertest.Send(`{"type":"result","subtype":"success","is_error":false,"session_id":"00000000-0000-4000-8000-0000000000aa","result":"ok"}`),
			providertest.AwaitEOF(),
		))
		fake.Install()
		cd := composeDeps(t, fakeRuntimeConfig{}, string(runtimes.Claude))
		cd.Deps.RuntimeFactory = nil
		cd.Deps.Profiles = config.ProfileMap{"worker": profile}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-STDIN", AgentProfile: "worker", Workdir: t.TempDir(), Mode: mode, Description: description})
		require.NoError(t, err)
		t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
		return fake, cd, sess
	}
	// settle gives a stray frame time to arrive before the count is checked.
	settle := func(fake *providertest.Fake, n int) []string {
		require.Eventually(t, func() bool { return len(fake.Calls()) == 1 && len(fake.Call(0).Stdin) >= n }, 10*time.Second, 20*time.Millisecond)
		time.Sleep(300 * time.Millisecond)
		return fake.Call(0).Stdin
	}

	t.Run("one-shot", func(t *testing.T) {
		fake, _, _ := boot(t, agent.ModeOneShot, "")
		stdin := settle(fake, 1)
		require.Len(t, stdin, 1, "the kickoff must reach stdin exactly once: %q", stdin)
	})

	// Claude runs in its boot dir, so a one-shot with a description sends
	// the description alone, as before agentkit v0.13.0.
	t.Run("one-shot with a description", func(t *testing.T) {
		fake, _, _ := boot(t, agent.ModeOneShot, "write the quarterly report")
		stdin := settle(fake, 1)
		require.Len(t, stdin, 1, "%q", stdin)
		require.Equal(t, `{"type":"user","message":{"role":"user","content":"write the quarterly report"}}`, stdin[0])
	})

	t.Run("long-lived", func(t *testing.T) {
		fake, cd, sess := boot(t, agent.ModeLongLived, "")
		settle(fake, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, cd.Manager.SendTurn(ctx, sess, "turn two"))
		stdin := settle(fake, 2)
		require.Len(t, stdin, 2, "kickoff, then turn two: %q", stdin)
		require.Contains(t, stdin[0], "Boot @./boot.md", "claude runs in its boot dir and follows the pointer")
		require.Contains(t, stdin[1], `"content":"turn two"`)
		require.Len(t, fake.Calls(), 1, "streaming turns share one process")
		args := fake.Call(0).Args
		for _, flag := range []string{"--model", "claude-test-model", "--max-turns", "7", "--settings"} {
			require.Contains(t, args, flag, "argv: %q", args)
		}
	})
}

func countArg(args []string, want string) int {
	n := 0
	for _, a := range args {
		if a == want {
			n++
		}
	}
	return n
}

// OpenCode runs in the project dir, so a one-shot opencode run gets boot.md's
// content as its prompt, description included, not the description alone:
// the briefing (task bundle under $OPENCODE_CONFIG_DIR, work-root rule, task
// framing) is otherwise out of its reach. On main every opencode run turn
// carried it, as the prepared argv's prompt (CW-20261001-0094 review).
func TestBoot_OpencodeOneShotCarriesItsBriefing(t *testing.T) {
	fake := providertest.New(t, runtimes.OpenCode, providertest.Replay("opencode/run_turn1"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, string(runtimes.OpenCode))
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode", Model: "opencode/test-model"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: "CW-TEST-ONESHOT", AgentProfile: "worker", Workdir: t.TempDir(),
		Mode: agent.ModeOneShot, Description: "write the quarterly report",
	})
	require.NoError(t, err)
	require.Len(t, fake.Calls(), 1)
	args := fake.Call(0).Args
	end := slices.Index(args, "--")
	require.Equal(t, len(args)-2, end, "the prompt is the only argument after --: %.300q", args)
	prompt := args[len(args)-1]
	for _, want := range []string{
		"**Task ID:** `CW-TEST-ONESHOT`",
		"$OPENCODE_CONFIG_DIR/tasks/",
		"Workspace rule:",
		"## First turn\n\nwrite the quarterly report",
	} {
		require.Contains(t, prompt, want, "the one-shot prompt must carry the briefing")
	}
}

package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMode_String round-trips the public Mode names. These strings land in
// log output + DB metadata, so changing them is a behavior change the
// implementer must opt into.
func TestMode_String(t *testing.T) {
	cases := map[Mode]string{
		ModeLongLived:  "long_lived",
		ModeOneShot:    "one_shot",
		ModeResume:     "resume",
		ModeSubagent:   "subagent",
		ModeBackground: "background",
		Mode(99):       "unknown",
	}
	for m, want := range cases {
		assert.Equal(t, want, m.String())
	}
}

// TestOptions_Validate exercises the per-Mode constraint matrix. The
// validator is called on every Boot call; corner cases here matter for
// rejecting malformed launch requests before any tempdir or DB row work.
func TestOptions_Validate(t *testing.T) {
	t.Run("happy path long-lived", func(t *testing.T) {
		err := Options{
			Mode:         ModeLongLived,
			AgentProfile: "default",
			Workdir:      "/tmp/x",
		}.Validate()
		assert.NoError(t, err)
	})

	t.Run("missing AgentProfile", func(t *testing.T) {
		err := Options{Workdir: "/tmp/x"}.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AgentProfile")
	})

	t.Run("missing Workdir", func(t *testing.T) {
		err := Options{AgentProfile: "default"}.Validate()
		assert.ErrorIs(t, err, ErrWorkdirRequired)
	})

	t.Run("Subagent requires ParentSessionID", func(t *testing.T) {
		err := Options{
			Mode:         ModeSubagent,
			AgentProfile: "default",
			Workdir:      "/tmp/x",
		}.Validate()
		assert.ErrorIs(t, err, ErrParentSessionRequired)
	})

	t.Run("Subagent with ParentSessionID", func(t *testing.T) {
		err := Options{
			Mode:            ModeSubagent,
			AgentProfile:    "default",
			Workdir:         "/tmp/x",
			ParentSessionID: "SES-PARENT",
		}.Validate()
		assert.NoError(t, err)
	})

	t.Run("Resume requires ResumeFromCheckpoint", func(t *testing.T) {
		err := Options{
			Mode:         ModeResume,
			AgentProfile: "default",
			Workdir:      "/tmp/x",
		}.Validate()
		assert.ErrorIs(t, err, ErrResumeCheckpointRequired)
	})

	t.Run("Resume with ResumeFromCheckpoint", func(t *testing.T) {
		err := Options{
			Mode:                 ModeResume,
			AgentProfile:         "default",
			Workdir:              "/tmp/x",
			ResumeFromCheckpoint: "SCP-X",
		}.Validate()
		assert.NoError(t, err)
	})

	t.Run("OneShot + Background only need profile + workdir", func(t *testing.T) {
		for _, m := range []Mode{ModeOneShot, ModeBackground} {
			err := Options{
				Mode:         m,
				AgentProfile: "default",
				Workdir:      "/tmp/x",
			}.Validate()
			assert.NoError(t, err, "Mode=%s should validate without extra fields", m)
		}
	})
}

// TestStatus_Terminal locks the `crashed` value into the terminal set.
// Dashboards and the orphan sweep rely on Terminal() returning true for
// any value that should stop appearing in "running" rollups.
func TestStatus_Terminal(t *testing.T) {
	for _, s := range []Status{StatusDone, StatusFailed, StatusCrashed} {
		assert.True(t, s.Terminal(), "%s should be terminal", s)
	}
	for _, s := range []Status{StatusLaunching, StatusRunning} {
		assert.False(t, s.Terminal(), "%s should NOT be terminal", s)
	}
}

// TestShouldUsePTY locks the per-Mode + per-provider + per-profile Caps.PTY
// decision matrix. Default (profile.PTY=nil) is subprocess-per-turn for all
// providers — programmatic auto-fire against PTY/TUI claude is unproven (the
// kickoff lands in the TUI input box but doesn't submit; mux's claudecode is
// human-driven), so long-lived state goes through claude's `--resume` chain
// across subprocess turns instead. profile.PTY=true is the experiment escape
// hatch. ModeOneShot is always subprocess. opts.SubprocessPerTurnOverride is
// a hard override.
func TestShouldUsePTY(t *testing.T) {
	bptr := func(b bool) *bool { return &b }
	cases := []struct {
		name       string
		mode       Mode
		provider   string
		profilePTY *bool
		override   bool
		want       bool
	}{
		// Matrix defaults (profile.PTY=nil) — subprocess-per-turn everywhere.
		{"claude long-lived → no PTY (TUI auto-fire unproven)", ModeLongLived, "claude", nil, false, false},
		{"claude subagent → no PTY", ModeSubagent, "claude", nil, false, false},
		{"claude resume → no PTY", ModeResume, "claude", nil, false, false},
		{"claude background → no PTY", ModeBackground, "claude", nil, false, false},
		{"claude OneShot → no PTY (single turn)", ModeOneShot, "claude", nil, false, false},
		{"codex long-lived → no PTY (not yet probed)", ModeLongLived, "codex", nil, false, false},
		{"opencode long-lived → no PTY", ModeLongLived, "opencode", nil, false, false},
		{"gemini long-lived → no PTY", ModeLongLived, "gemini", nil, false, false},
		{"copilot long-lived → no PTY", ModeLongLived, "copilot", nil, false, false},

		// opts.SubprocessPerTurnOverride beats everything.
		{"override forces no PTY even when profile.PTY=true", ModeLongLived, "claude", bptr(true), true, false},

		// Explicit profile.PTY override (experiment escape hatch).
		{"profile.PTY=true forces PTY on claude long-lived", ModeLongLived, "claude", bptr(true), false, true},
		{"profile.PTY=true forces PTY on codex long-lived", ModeLongLived, "codex", bptr(true), false, true},
		{"profile.PTY=false explicit (matches default)", ModeLongLived, "claude", bptr(false), false, false},
		{"profile.PTY=true on OneShot still subprocess (single-turn constraint)", ModeOneShot, "claude", bptr(true), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldUsePTY(tc.mode, tc.provider, tc.profilePTY, tc.override)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSanitizeTaskID covers the boot-dir naming sanitizer. Anything outside
// the forensic-friendly alphabet collapses to '_'.
func TestSanitizeTaskID(t *testing.T) {
	cases := map[string]string{
		"CW-20260508-0001":      "CW-20260508-0001",
		"task_with_underscores": "task_with_underscores",
		"task with spaces":      "task_with_spaces",
		"task/with/slashes":     "task_with_slashes",
		"task@with#symbols":     "task_with_symbols",
		"":                      "no-task",
	}
	for in, want := range cases {
		assert.Equal(t, want, sanitizeTaskID(in), "input=%q", in)
	}
}

// TestSubstituteTemplates covers the {{.BootDir}} / {{.ProjectDir}} expansion
// applied to the lib's BootDirSpec.EnvAmendments.
func TestSubstituteTemplates(t *testing.T) {
	in := []string{
		"OPENCODE_CONFIG_DIR={{.BootDir}}",
		"PROJECT_PATH={{.ProjectDir}}",
		"NESTED={{.BootDir}}/{{.ProjectDir}}/x",
		"PASSTHROUGH=plain",
	}
	out := substituteTemplates(in, "/tmp/boot", "/repo/x")
	assert.Equal(t, []string{
		"OPENCODE_CONFIG_DIR=/tmp/boot",
		"PROJECT_PATH=/repo/x",
		"NESTED=/tmp/boot//repo/x/x",
		"PASSTHROUGH=plain",
	}, out)

	assert.Nil(t, substituteTemplates(nil, "x", "y"))
}

// TestTokenizeArg verifies the lib's ProjectDirArg template is split into a
// pre-tokenized arg slice. Empty inputs yield nil so BuildArgs.append
// stays a no-op.
func TestTokenizeArg(t *testing.T) {
	assert.Equal(t, []string{"--add-dir", "/repo"},
		tokenizeArg("--add-dir {{.ProjectDir}}", "/tmp/boot", "/repo"))
	assert.Equal(t, []string{"--cd", "/repo"},
		tokenizeArg("--cd {{.ProjectDir}}", "/tmp/boot", "/repo"))
	assert.Nil(t, tokenizeArg("", "/tmp/boot", "/repo"))
	assert.Nil(t, tokenizeArg("--add-dir {{.ProjectDir}}", "/tmp/boot", ""), "empty projectDir → nil")
}

// TestKickoffPayload covers the user-message body Boot fires (or plants as
// FirstTurnPayload) for the session's first turn.
func TestKickoffPayload(t *testing.T) {
	assert.Equal(t, "Boot @./boot.md", kickoffPayload("boot.md"))
	assert.Equal(t, "Boot @./boot.md", kickoffPayload(""), "empty defaults to boot.md")
	assert.Equal(t, "Boot @./custom.md", kickoffPayload("custom.md"))
}

// TestKickoffMarkdown verifies the planted boot.md content carries the
// task framing the LLM needs on its first turn (and after compaction
// when re-reading the file).
func TestKickoffMarkdown(t *testing.T) {
	body := kickoffMarkdown(Options{
		AgentProfile:  "orchestrator",
		TaskID:        "CW-PLAN-001",
		Workdir:       "/repo/x",
		OneShotPrompt: "Walk the plan.",
		SessionMeta:   map[string]string{"plan_id": "CW-PLAN-001"},
	}, "orchestrator")

	assert.Contains(t, body, "`orchestrator`")
	assert.Contains(t, body, "CW-PLAN-001")
	assert.Contains(t, body, "/repo/x")
	assert.Contains(t, body, "Walk the plan.")
	assert.Contains(t, body, "clockwork_loopback")

	// Empty role falls back to AgentProfile.
	body = kickoffMarkdown(Options{AgentProfile: "planner"}, "")
	assert.Contains(t, body, "`planner`")

	// No task id → "(no task_id)".
	body = kickoffMarkdown(Options{AgentProfile: "x"}, "x")
	assert.Contains(t, body, "(no task_id)")
}

// TestProfileIsDevMode locks the --dangerously-skip-permissions detection,
// shared with the legacy cliexec.ProfileIsDevMode helper.
func TestProfileIsDevMode(t *testing.T) {
	assert.True(t, ProfileIsDevMode(config.AgentProfile{
		Args: []string{"--dangerously-skip-permissions"},
	}))
	assert.True(t, ProfileIsDevMode(config.AgentProfile{
		Args: []string{"--other", "--dangerously-skip-permissions"},
	}))
	assert.False(t, ProfileIsDevMode(config.AgentProfile{
		Args: []string{"--other"},
	}))
	assert.False(t, ProfileIsDevMode(config.AgentProfile{}))
}

// TestAdapterFor_ClaudeMatrix locks the dev × pty constructor-selection
// matrix for the claude provider. Subprocess-per-turn (non-PTY) paths use
// the v0.9.1 bare-mode constructors (Bare=true) — bare mode skips
// auto-discovery of operator config (~/.claude/settings.json, ~/.claude.json,
// hooks/plugins/MCP/OAuth/keychain/CLAUDE.md auto-find) and obsoletes the
// operator-config-bleed-through class for bare consumers (CW-20260508-0019).
// PTY paths stay non-bare (the TUI shape doesn't accept --bare; bare is
// print-mode-focused per Anthropic's docs).
func TestAdapterFor_ClaudeMatrix(t *testing.T) {
	cases := []struct {
		name              string
		args              []string // profile.Args (drives dev-mode detection)
		pty               bool
		wantBare          bool
		wantPTY           bool
		wantSkipPermsTrue bool
	}{
		{
			name:              "non-dev + non-pty → bare (subprocess-per-turn)",
			args:              nil,
			pty:               false,
			wantBare:          true,
			wantPTY:           false,
			wantSkipPermsTrue: false,
		},
		{
			name:              "dev + non-pty → dev-bare (subprocess-per-turn)",
			args:              []string{"--dangerously-skip-permissions"},
			pty:               false,
			wantBare:          true,
			wantPTY:           false,
			wantSkipPermsTrue: true,
		},
		{
			name:              "non-dev + pty → pty (non-bare)",
			args:              nil,
			pty:               true,
			wantBare:          false,
			wantPTY:           true,
			wantSkipPermsTrue: false,
		},
		{
			name:              "dev + pty → dev-pty (non-bare)",
			args:              []string{"--dangerously-skip-permissions"},
			pty:               true,
			wantBare:          false,
			wantPTY:           true,
			wantSkipPermsTrue: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := config.AgentProfile{Provider: "claude", Args: tc.args}
			adapter, caps, err := adapterFor(profile, "claude", tc.pty)
			require.NoError(t, err)
			assert.True(t, caps.BinaryRequired)
			assert.True(t, caps.ProviderSessionID)
			assert.True(t, caps.CheckpointResume)

			ca, ok := adapter.(*provider.ClaudeAdapter)
			require.True(t, ok, "adapter should be *provider.ClaudeAdapter, got %T", adapter)
			assert.Equal(t, tc.wantBare, ca.Bare, "Bare")
			assert.Equal(t, tc.wantPTY, ca.PTY, "PTY")
			assert.Equal(t, tc.wantSkipPermsTrue, ca.SkipPermissions, "SkipPermissions")

			// Bare-mode adapters returned by adapterFor are pre-injection:
			// the four explicit-injection fields are populated post-plant
			// in agent.Boot, not by the constructor.
			if tc.wantBare {
				assert.Empty(t, ca.MCPConfigPath, "MCPConfigPath must be empty pre-plant")
				assert.Empty(t, ca.AppendSystemPromptFile, "AppendSystemPromptFile must be empty pre-plant")
				assert.Empty(t, ca.SettingsPath, "SettingsPath must be empty pre-plant")
				assert.Empty(t, ca.ProjectDir, "ProjectDir must be empty pre-plant")
			}
		})
	}
}

// TestClaudeBareAdapter_AddDirSingleEmit verifies that a bare-mode claude
// adapter populated via BareInjectionPaths emits exactly one `--add-dir`
// in BuildArgs. Combined with the buildArgs closure in boot.go skipping
// layout.ProjectDirArg for bare-mode claude (skipProjectDirArg=true), the
// final spawn argv contains a single `--add-dir <projectDir>`.
func TestClaudeBareAdapter_AddDirSingleEmit(t *testing.T) {
	a := provider.NewClaudeAdapterDevBare()
	inj := a.BareInjectionPaths("/tmp/boot", "/repo/x")
	a.MCPConfigPath = inj.MCPConfigPath
	a.AppendSystemPromptFile = inj.AppendSystemPromptFile
	a.SettingsPath = inj.SettingsPath
	a.ProjectDir = inj.ProjectDir

	args := a.BuildArgs("hello", "ignored-in-bare", "")

	addDirCount := 0
	for _, arg := range args {
		if arg == "--add-dir" {
			addDirCount++
		}
	}
	assert.Equal(t, 1, addDirCount, "bare-mode BuildArgs should emit --add-dir exactly once; argv=%v", args)
	assert.Contains(t, args, "/repo/x", "argv should carry projectDir as the --add-dir value")
	assert.Contains(t, args, "--bare")
	assert.Contains(t, args, "--mcp-config")
	assert.Contains(t, args, "--append-system-prompt-file")
	assert.Contains(t, args, "--settings")
}

// TestProfileArgsExcludingDevFlag verifies the dev flag is stripped before
// re-prepending profile.Args (the flag is consumed by adapterFor →
// NewClaudeAdapterDev*; passing it through profile.Args would double-add).
func TestProfileArgsExcludingDevFlag(t *testing.T) {
	out := profileArgsExcludingDevFlag(config.AgentProfile{
		Args: []string{"--dangerously-skip-permissions", "--debug", "--verbose"},
	})
	assert.Equal(t, []string{"--debug", "--verbose"}, out)

	assert.Nil(t, profileArgsExcludingDevFlag(config.AgentProfile{}))
	assert.Nil(t, profileArgsExcludingDevFlag(config.AgentProfile{
		Args: []string{},
	}))
}

// TestComposeEnv exercises the env composition order: filtered OS env →
// CLOCKWORK_TASK_ID/RUN_ID → opts.Env. Agent-file env was dropped from the
// per-call surface for V1 (caller can stamp via opts.Env directly).
//
// Also asserts the CW-20260509-0011 provider-auth-passthrough contract:
// ANTHROPIC_API_KEY (and the other portfolio provider auth env vars listed
// in providerAuthEnvVars) MUST survive composeEnv. Pre-fix, they were
// stripped by LooksLikeSecret because their names contain "API_KEY" /
// "TOKEN" — bare-mode claude in the daemon-spawned subprocess then failed
// with "Not logged in".
func TestComposeEnv(t *testing.T) {
	t.Setenv("CLOCKWORK_TEST_MARKER", "yes")
	t.Setenv("ANTHROPIC_API_KEY", "secret-should-survive")

	out := composeEnv(config.AgentProfile{}, Options{
		TaskID: "CW-1",
		RunID:  42,
		Env: map[string]string{
			"FOO": "bar",
		},
	}, nil)

	asMap := map[string]string{}
	for _, kv := range out {
		i := strings.Index(kv, "=")
		if i < 0 {
			continue
		}
		asMap[kv[:i]] = kv[i+1:]
	}
	assert.Equal(t, "CW-1", asMap["CLOCKWORK_TASK_ID"])
	assert.Equal(t, "42", asMap["CLOCKWORK_RUN_ID"])
	assert.Equal(t, "bar", asMap["FOO"])
	assert.Equal(t, "yes", asMap["CLOCKWORK_TEST_MARKER"])
	assert.Equal(t, "secret-should-survive", asMap["ANTHROPIC_API_KEY"],
		"ANTHROPIC_API_KEY must survive composeEnv per CW-20260509-0011 (provider-auth passthrough)")
}

// TestResolveTimeout exercises the priority chain: metadata override →
// profile.TimeoutSeconds → defaultExecutionTimeout (5min).
func TestResolveTimeout(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		got := resolveTimeout(config.AgentProfile{}, Options{})
		assert.Equal(t, defaultExecutionTimeout, got)
	})

	t.Run("profile timeout wins over default", func(t *testing.T) {
		got := resolveTimeout(config.AgentProfile{TimeoutSeconds: 600}, Options{})
		assert.Equal(t, int64(600), int64(got.Seconds()))
	})

	t.Run("metadata override wins over profile", func(t *testing.T) {
		got := resolveTimeout(
			config.AgentProfile{TimeoutSeconds: 600},
			Options{Metadata: map[string]any{"timeout_seconds_override": 1200}},
		)
		assert.Equal(t, int64(1200), int64(got.Seconds()))
	})

	t.Run("metadata override out of range falls back to profile", func(t *testing.T) {
		got := resolveTimeout(
			config.AgentProfile{TimeoutSeconds: 600},
			Options{Metadata: map[string]any{"timeout_seconds_override": 30}}, // below min 60
		)
		assert.Equal(t, int64(600), int64(got.Seconds()))
	})

	t.Run("metadata override accepts float64", func(t *testing.T) {
		got := resolveTimeout(
			config.AgentProfile{},
			Options{Metadata: map[string]any{"timeout_seconds_override": float64(900)}},
		)
		assert.Equal(t, int64(900), int64(got.Seconds()))
	})
}

// TestNewManager constructs a Manager and exercises the lifecycle facade
// methods that don't require a live spawned process. Confirms the deps
// pointer is captured (not snapshotted) so the AgentDeps two-step
// construction works.
func TestNewManager(t *testing.T) {
	deps := &Dependencies{}
	mgr := NewManager(deps)
	require.NotNil(t, mgr)
	assert.Equal(t, deps, mgr.deps)

	// LivePID for an unknown session returns 0 — no panic on missing inner.
	assert.Equal(t, 0, mgr.LivePID("nonexistent"))

	// checkStopped returns nil before Shutdown.
	assert.NoError(t, mgr.checkStopped())
}

// TestManager_Sweep_NilStore is a graceful no-op (matches the rest of the
// runtime stack's nil-sink convention). Caller (bootstrap) always wires a
// real store; this branch is the test path.
func TestManager_Sweep_NilStore(t *testing.T) {
	mgr := NewManager(&Dependencies{})
	swept, err := mgr.Sweep()
	assert.NoError(t, err)
	assert.Equal(t, 0, swept)
}

// TestBootDir_NamingConvention confirms the cross-app forensic discoverability
// pattern: clockwork-boot-<provider>-<taskID>-r<runID>-XXXXXX. Tests run
// against a tempdir-only path so they don't pollute the real $TMPDIR.
func TestBootDir_NamingConvention(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	bootDir, err := makeBootDir("claude", "CW-20260508-0001", 42)
	require.NoError(t, err)
	defer func() { _ = removeAll(bootDir) }()

	assert.Contains(t, bootDir, "clockwork-boot-claude-CW-20260508-0001-r42-")
	assert.True(t, strings.HasPrefix(bootDir, tmp), "bootDir under $TMPDIR")
}

// removeAll is a thin wrapper around os.RemoveAll for test cleanup.
func removeAll(path string) error {
	return os.RemoveAll(path)
}

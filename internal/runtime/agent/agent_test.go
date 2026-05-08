package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
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

// TestShouldUsePTY locks the per-Mode + per-provider Caps.PTY decision
// matrix. Today only claude on long-lived modes opts into PTY; everything
// else stays subprocess-per-turn until probed. ModeOneShot is always
// subprocess regardless of provider (single-turn semantics).
func TestShouldUsePTY(t *testing.T) {
	cases := []struct {
		name     string
		mode     Mode
		provider string
		override bool
		want     bool
	}{
		{"claude long-lived → PTY", ModeLongLived, "claude", false, true},
		{"claude subagent → PTY", ModeSubagent, "claude", false, true},
		{"claude resume → PTY", ModeResume, "claude", false, true},
		{"claude background → PTY", ModeBackground, "claude", false, true},
		{"claude OneShot → no PTY (single turn)", ModeOneShot, "claude", false, false},
		{"override forces no PTY even on claude long-lived", ModeLongLived, "claude", true, false},
		{"codex long-lived → no PTY (not yet probed)", ModeLongLived, "codex", false, false},
		{"opencode long-lived → no PTY", ModeLongLived, "opencode", false, false},
		{"gemini long-lived → no PTY", ModeLongLived, "gemini", false, false},
		{"copilot long-lived → no PTY", ModeLongLived, "copilot", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldUsePTY(tc.mode, tc.provider, tc.override)
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

// TestProfileArgsExcludingDevFlag verifies the dev flag is stripped before
// re-prepending profile.Args (the flag is consumed by adapterFor →
// NewClaudeAdapterDev; passing it through profile.Args would double-add).
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

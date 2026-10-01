package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/testutil/testenv"
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

// TestResolveRepoRoot verifies the repo_root resolution Boot threads into
// WorkspaceLayout.RepoRoot: Options.RepoRoot wins when set (the scheduler
// supplies it alongside the per-run worktree work_root), with a fallback to
// Options.Workdir that preserves shared-mode behaviour (work_root == repo_root).
func TestResolveRepoRoot(t *testing.T) {
	t.Run("RepoRoot unset → falls back to Workdir (shared mode)", func(t *testing.T) {
		got := resolveRepoRoot(Options{Workdir: "/repo/canonical"})
		assert.Equal(t, "/repo/canonical", got)
	})

	t.Run("RepoRoot set → wins over Workdir (worktree mode)", func(t *testing.T) {
		// Worktree mode: Workdir is the per-run worktree, RepoRoot is the
		// canonical checkout. The two must stay distinct.
		got := resolveRepoRoot(Options{
			Workdir:  "/repo/canonical-worktrees/run-7",
			RepoRoot: "/repo/canonical",
		})
		assert.Equal(t, "/repo/canonical", got)
		assert.NotEqual(t, "/repo/canonical-worktrees/run-7", got,
			"worktree mode: repo_root must not collapse onto work_root")
	})
}

// TestStatus_Terminal locks the `crashed` value into the terminal set.
// Dashboards and the orphan sweep rely on Terminal() returning true for
// any value that should stop appearing in "running" rollups.
func TestStatus_Terminal(t *testing.T) {
	for _, s := range []Status{StatusDone, StatusFailed, StatusCanceled, StatusCrashed} {
		assert.True(t, s.Terminal(), "%s should be terminal", s)
	}
	for _, s := range []Status{StatusLaunching, StatusRunning} {
		assert.False(t, s.Terminal(), "%s should NOT be terminal", s)
	}
}

// TestSelectRuntimeKind locks the per-provider runtime-kind default matrix
// post-2026-05-13. Per the boot.go ModeOneShot turn-complete wait + the
// go-providers v0.17.1 long-lived constructors:
//
//   - codex       → JsonRpcStdio (app-server, JSON-RPC 2.0 over stdio)
//   - claude-code → StreamingStdio (NDJSON-over-stdin)
//   - claude      → Subprocess (bare-mode subprocess-per-turn)
//   - opencode    → Subprocess (no long-lived adapter)
//
// profile.RuntimeKind, when non-empty, beats the matrix. Invalid values
// surface as a validate error.
func TestSelectRuntimeKind(t *testing.T) {
	cases := []struct {
		name        string
		provider    string
		profileKind string
		want        RuntimeKind
		wantErr     bool
	}{
		// Per-runtime defaults (profileKind="" → the registry descriptor's
		// DefaultMode).
		{"codex default → jsonrpc-stdio", "codex", "", RuntimeKindJsonRpcStdio, false},
		{"claude-code default → streaming-stdio", "claude-code", "", RuntimeKindStreamingStdio, false},
		// "claude" is the registry's canonical id for the same runtime
		// (claude-code is its alias). adapterFor still refuses the retired
		// bare "claude" provider; this is only the kind that error carries.
		{"claude default → streaming-stdio", "claude", "", RuntimeKindStreamingStdio, false},
		{"opencode default → subprocess-per-turn", "opencode", "", RuntimeKindSubprocess, false},
		{"unknown provider → subprocess-per-turn, adapterFor errors", "gemini", "", RuntimeKindSubprocess, false},

		// Profile override wins.
		{"profile override: codex subprocess (escape hatch)", "codex", "subprocess", RuntimeKindSubprocess, false},
		{"profile override: claude pty (experiment)", "claude", "pty", RuntimeKindPTY, false},
		{"profile override: claude-code streaming-stdio (explicit but redundant)", "claude-code", "streaming-stdio", RuntimeKindStreamingStdio, false},
		// opencode long-lived opt-in: ServeHTTP via profile.RuntimeKind override.
		// Default for opencode stays Subprocess (line above); this is the
		// V2-pipeline multi-turn-worker opt-in path. Added 2026-05-21 with
		// go-providers v0.23.0 + go-agent-sessions v0.10.0.
		{"profile override: opencode serve-http (long-lived opt-in)", "opencode", "serve-http", RuntimeKindServeHTTP, false},

		// Current spellings, and the older ones runtimetoken maps.
		{"profile kind subprocess-per-turn", "codex", "subprocess-per-turn", RuntimeKindSubprocess, false},
		{"profile kind http-sse", "opencode", "http-sse", RuntimeKindServeHTTP, false},
		// Older spellings that were never valid in a profile stay errors
		// there (stored session rows still read them; see
		// TestParseRuntimeKind_SessionRowsAcceptEveryOlderToken).
		{"profile kind cli → error", "opencode", "cli", "", true},
		{"profile kind app-server → error", "codex", "app-server", "", true},
		{"profile kind pty-debug → error", "claude-code", "pty-debug", "", true},
		{"profile kind Serve_HTTP normalizes", "opencode", " Serve_HTTP ", RuntimeKindServeHTTP, false},

		// Invalid profile kind.
		{"invalid kind → error", "codex", "tui", "", true},
		// ACP modes (CW-20261001-0097): a profile opts Claude, Codex or
		// OpenCode into acp-stdio; Copilot and Pi default to it.
		{"profile kind acp-stdio", "codex", "acp-stdio", RuntimeKindACPStdio, false},
		{"profile kind acp-tcp", "copilot", "acp-tcp", RuntimeKindACPTCP, false},
		{"copilot default → acp-stdio", "copilot", "", RuntimeKindACPStdio, false},
		{"pi default → acp-stdio", "pi", "", RuntimeKindACPStdio, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectRuntimeKind(tc.provider, tc.profileKind)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestResolveRuntimeKind verifies the override precedence chain: per-Boot
// Options.RuntimeKindOverride > profile.RuntimeKind > per-provider default.
func TestResolveRuntimeKind(t *testing.T) {
	codexProfile := config.AgentProfile{Provider: "codex"}
	codexProfileSubproc := config.AgentProfile{Provider: "codex", RuntimeKind: "subprocess"}

	t.Run("no override, no profile field → per-provider default", func(t *testing.T) {
		got, err := resolveRuntimeKind(codexProfile, Options{})
		require.NoError(t, err)
		assert.Equal(t, RuntimeKindJsonRpcStdio, got)
	})

	t.Run("profile.RuntimeKind wins over default", func(t *testing.T) {
		got, err := resolveRuntimeKind(codexProfileSubproc, Options{})
		require.NoError(t, err)
		assert.Equal(t, RuntimeKindSubprocess, got)
	})

	t.Run("Options override wins over profile + default", func(t *testing.T) {
		got, err := resolveRuntimeKind(codexProfileSubproc, Options{RuntimeKindOverride: RuntimeKindJsonRpcStdio})
		require.NoError(t, err)
		assert.Equal(t, RuntimeKindJsonRpcStdio, got)
	})

	t.Run("invalid Options override → error", func(t *testing.T) {
		_, err := resolveRuntimeKind(codexProfile, Options{RuntimeKindOverride: RuntimeKind("nope")})
		require.Error(t, err)
	})
}

// TestKickoffPayload covers the user-message body Boot fires (or plants as
// FirstTurnPayload) for the session's first turn.
func TestKickoffPayload(t *testing.T) {
	assert.Equal(t, "Boot @./boot.md", kickoffPayload("boot.md"))
	assert.Equal(t, "Boot @./boot.md", kickoffPayload(""), "empty defaults to boot.md")
	assert.Equal(t, "Boot @./custom.md", kickoffPayload("custom.md"))
}

func TestKickoffPayloadForBootDir(t *testing.T) {
	assert.Equal(t, "Boot @./boot.md", kickoffPayloadForBootDir(""))
	assert.Equal(t, "Boot @/tmp/torque-boot/agentlaunch-bootdir-123/boot.md",
		kickoffPayloadForBootDir("/tmp/torque-boot/agentlaunch-bootdir-123"))
}

// A runtime that runs in its boot dir follows the `@./boot.md` pointer; one
// that runs elsewhere (opencode, in the project dir) gets boot.md's content,
// since the pointer would resolve against the wrong directory
// (CW-20261001-0104).
func TestFirstTurnKickoff(t *testing.T) {
	const boot, md = "/tmp/torque-boot/b1", "# Boot\n\nbriefing"
	assert.Equal(t, "Boot @./boot.md", firstTurnKickoff(boot, boot, md), "cwd is the boot dir")
	assert.Equal(t, "Boot @./boot.md", firstTurnKickoff(boot, boot+"/", md), "paths compare cleaned")
	assert.Equal(t, md, firstTurnKickoff(boot, "/work/project", md), "cwd is the project dir")
	assert.Equal(t, "Boot @./boot.md", firstTurnKickoff("", "/work/project", md), "nothing planted")
	assert.Equal(t, "Boot @./boot.md", firstTurnKickoff(boot, "/work/project", ""), "no content to inline")
}

// One-shot: the prompt alone where the runtime runs in its boot dir, as
// before; boot.md's content, which carries the prompt, where it does not.
func TestOneShotTurn(t *testing.T) {
	const boot, md = "/tmp/torque-boot/b1", "# Boot\n\n## First turn\n\nwrite the report\n"
	assert.Equal(t, "write the report", oneShotTurn("write the report", boot, boot, md), "claude: the prompt alone")
	assert.Equal(t, md, oneShotTurn("write the report", boot, "/work/project", md), "opencode: the briefing, prompt included")
	assert.Equal(t, "Boot @./boot.md", oneShotTurn("", boot, boot, md), "no prompt: the kickoff pointer")
	assert.Equal(t, md, oneShotTurn("", boot, "/work/project", md))
	assert.Equal(t, "write the report", oneShotTurn("write the report", "", "/work/project", ""), "nothing planted")
}

// boot.md's content past maxArgvKickoff cannot ride a subprocess-per-turn
// runtime's argv (MAX_ARG_STRLEN), so that turn points at the planted file by
// absolute path; a runtime that takes the turn over stdin or HTTP, a shorter
// briefing, and a turn that is not the briefing all pass through
// (CW-20261001-0121).
func TestArgvSafeTurn(t *testing.T) {
	const boot = "/tmp/torque-boot/b1"
	fits, over := strings.Repeat("x", maxArgvKickoff), strings.Repeat("x", maxArgvKickoff+1)
	assert.Equal(t, "Boot @/tmp/torque-boot/b1/boot.md", argvSafeTurn("SES-1", over, boot, over, RuntimeKindSubprocess))
	assert.Equal(t, fits, argvSafeTurn("SES-1", fits, boot, fits, RuntimeKindSubprocess), "at the bound")
	assert.Equal(t, over, argvSafeTurn("SES-1", over, boot, over, RuntimeKindServeHTTP), "opencode serve takes the turn over HTTP")
	assert.Equal(t, over, argvSafeTurn("SES-1", over, boot, over, RuntimeKindStreamingStdio), "claude takes the turn on stdin")
	assert.Equal(t, over, argvSafeTurn("SES-1", over, boot, "# Boot", RuntimeKindSubprocess), "a prompt that is not the briefing")
	assert.Equal(t, over, argvSafeTurn("SES-1", over, "", over, RuntimeKindSubprocess), "nothing planted to point at")
}

// TestKickoffMarkdown verifies the planted boot.md content carries the
// task framing the LLM needs on its first turn (and after compaction
// when re-reading the file).
func TestKickoffMarkdown(t *testing.T) {
	body := kickoffMarkdown(Options{
		AgentProfile:  "orchestrator",
		TaskID:        "CW-PLAN-001",
		Workdir:       "/repo/x",
		RepoRoot:      "/repo/source",
		OneShotPrompt: "Walk the plan.",
		SessionMeta:   map[string]string{"plan_id": "CW-PLAN-001"},
	}, "orchestrator", false)

	assert.Contains(t, body, "`orchestrator`")
	assert.Contains(t, body, "CW-PLAN-001")
	assert.Contains(t, body, "/repo/x")
	assert.Contains(t, body, "Work root")
	assert.Contains(t, body, "/repo/source")
	assert.Contains(t, body, "$TORQUE_WORK_ROOT")
	assert.Contains(t, body, "Walk the plan.")
	assert.Contains(t, body, "`loopback` MCP server", "the name go-providers plants the per-task server under")
	assert.Contains(t, body, "mcp__mux__torque_*")

	// While Torque write-protects its state, mux carries no torque tools
	// and the kickoff does not point at them (CW-20261001-0141).
	protected := kickoffMarkdown(Options{AgentProfile: "worker", TaskID: "CW-1"}, "worker", true)
	assert.Contains(t, protected, "`loopback` MCP server")
	assert.Contains(t, protected, "only Torque tools")
	assert.NotContains(t, protected, "mcp__mux__torque_")

	// A worker's loopback is bound to its task; an orchestrator-class role's is
	// the full surface and takes explicit task ids, so the kickoff must not
	// tell it no task_id is needed.
	assert.Contains(t, kickoffMarkdown(Options{TaskID: "CW-1"}, "worker", false), "no `task_id` parameter required")
	for _, role := range []string{"orchestrator", "planner", "reviewer-end-agent"} {
		for _, omits := range []bool{false, true} {
			got := kickoffMarkdown(Options{TaskID: "CW-1"}, role, omits)
			assert.Contains(t, got, "pass the task id as the tool's schema asks", "%s omits=%v", role, omits)
			assert.Contains(t, got, "`id` on some tools and `task_id` on others", "%s omits=%v", role, omits)
			assert.NotContains(t, got, "no `task_id` parameter required", "%s omits=%v", role, omits)
		}
		assert.Contains(t, kickoffMarkdown(Options{TaskID: "CW-1"}, role, true), "only Torque tools")
		assert.NotContains(t, kickoffMarkdown(Options{TaskID: "CW-1"}, role, true), "mcp__mux__torque_")
	}

	// Empty role falls back to AgentProfile.
	body = kickoffMarkdown(Options{AgentProfile: "planner"}, "", false)
	assert.Contains(t, body, "`planner`")

	// No task id → "(no task_id)".
	body = kickoffMarkdown(Options{AgentProfile: "x"}, "x", false)
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

// TestAdapterFor_OpencodeWiring verifies that adapterFor populates
// OpencodeAdapter.Agent + Model from the AgentProfile.
//
// Agent: opencode `run` requires `--agent <name>`; the profile name
// is by convention the opencode agent name. Without this wiring the
// adapter would emit `--agent ""` and opencode would error out.
//
// Model: opencode requires `--model <X>` to precede the positional
// prompt arg in argv. The adapter places it correctly only when its
// Model field is populated. The generic `--model` suffix in
// agent.Boot's BuildArgs wrapper is skipped for opencode (see the
// skipModelSuffix branch in boot.go) — it would otherwise land
// `--model` AFTER the prompt and corrupt the argv.
func TestAdapterFor_OpencodeWiring(t *testing.T) {
	cases := []struct {
		name        string
		profileName string
		model       string
		wantErr     bool
	}{
		{
			name:        "agent + model both populated",
			profileName: "executor",
			model:       "opencode/big-pickle",
			wantErr:     false,
		},
		{
			name:        "agent populated, model empty (default model used)",
			profileName: "executor",
			model:       "",
			wantErr:     false,
		},
		{
			name:        "missing profile name → error",
			profileName: "",
			model:       "opencode/big-pickle",
			wantErr:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := config.AgentProfile{Provider: "opencode", Model: tc.model}
			adapter, caps, err := adapterFor(profile, tc.profileName, RuntimeKindSubprocess)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.True(t, caps.BinaryRequired)
			assert.True(t, caps.ProviderSessionID, "opencode run reports its session id, which a resume passes back as --session <id>")
			assert.False(t, caps.CheckpointResume)

			oa, ok := adapter.(*provider.OpencodeAdapter)
			require.True(t, ok, "adapter should be *provider.OpencodeAdapter, got %T", adapter)
			assert.Equal(t, tc.profileName, oa.Agent, "Agent should be set from profileName")
			assert.Equal(t, tc.model, oa.Model, "Model should be threaded from profile.Model")
		})
	}
}

// TestAdapterFor_OpencodeServeHTTP verifies the long-lived opencode path:
// when RuntimeKind=serve-http, adapterFor returns the go-providers
// NewOpencodeAdapterServeHTTP() variant (which emits `opencode serve
// --port 0 --hostname 127.0.0.1` argv) with go-agent-sessions Caps.ServeHTTP
// set so the lib's session selector spawns serveHttpSession.
//
// Counterpart to TestAdapterFor_OpencodeWiring which covers the default
// Subprocess (`opencode run --agent <name>`) path. Added 2026-05-21
// alongside the Torque integration for the multi-provider rollout (Phase 1
// follow-up — go-providers v0.23.0 + go-agent-sessions v0.10.0).
func TestAdapterFor_OpencodeServeHTTP(t *testing.T) {
	profile := config.AgentProfile{Provider: "opencode", Model: "opencode/big-pickle"}
	adapter, caps, err := adapterFor(profile, "executor", RuntimeKindServeHTTP)
	require.NoError(t, err)

	// Caps shape: ServeHTTP=true; mutually exclusive with the other
	// lifecycle flags (StreamingStdio/JsonRpcStdio/PTY). go-agent-sessions
	// v0.10.0's Capabilities.Validate enforces the at-most-one rule.
	assert.True(t, caps.BinaryRequired, "every adapter shells out to a CLI binary")
	assert.True(t, caps.ServeHTTP, "ServeHTTP must be set so the lib spawns serveHttpSession")
	assert.True(t, caps.ProviderSessionID, "ServeHTTP runtimes carry a provider-side session id (opencode `attach -c <id>`)")
	assert.False(t, caps.StreamingStdio, "serve-http and streaming-stdio are mutually exclusive")
	assert.False(t, caps.JsonRpcStdio, "serve-http and jsonrpc-stdio are mutually exclusive")
	assert.False(t, caps.PTY, "serve-http and PTY are mutually exclusive")

	// Adapter shape: go-providers v0.23.0's NewOpencodeAdapterServeHTTP
	// returns *OpencodeAdapter (same type as the subprocess variant —
	// the runtime-mode field is internal). Agent + Model are wired the
	// same way as the subprocess path.
	oa, ok := adapter.(*provider.OpencodeAdapter)
	require.True(t, ok, "adapter should be *provider.OpencodeAdapter, got %T", adapter)
	assert.Equal(t, "executor", oa.Agent, "Agent should be set from profileName")
	assert.Equal(t, "opencode/big-pickle", oa.Model, "Model should be threaded from profile.Model")
}

// TestAdapterFor_OpencodeUnsupportedRuntimeKind locks the gate: opencode
// only supports Subprocess + ServeHTTP. Other RuntimeKinds (e.g.
// StreamingStdio, JsonRpcStdio, PTY) error with a clear message naming
// the supported set — operator-actionable rather than a silent fall-
// through.
func TestAdapterFor_OpencodeUnsupportedRuntimeKind(t *testing.T) {
	profile := config.AgentProfile{Provider: "opencode", Model: "opencode/big-pickle"}
	for _, kind := range []RuntimeKind{RuntimeKindStreamingStdio, RuntimeKindJsonRpcStdio, RuntimeKindPTY} {
		t.Run(string(kind), func(t *testing.T) {
			_, _, err := adapterFor(profile, "executor", kind)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "supported: subprocess-per-turn, http-sse",
				"error should name the supported runtime kinds for operator triage")
		})
	}
}

// TestShouldDropBootDirExtraArgs pins the opencode serve-http ExtraArgs
// suppression: bootdir-derived args (e.g. `--dir`) are dropped ONLY for
// opencode + serve-http (where `opencode serve` rejects `--dir`), and
// preserved for opencode subprocess + every other provider/runtime. Refs
// CW-20260521-0022. (Copilot PR #93.)
func TestShouldDropBootDirExtraArgs(t *testing.T) {
	cases := []struct {
		provider string
		kind     RuntimeKind
		want     bool
	}{
		{"opencode", RuntimeKindServeHTTP, true},
		{"opencode", RuntimeKindSubprocess, false},
		{"opencode", "", false},
		{"codex", RuntimeKindServeHTTP, false}, // only opencode serve-http
		{"codex", RuntimeKindJsonRpcStdio, false},
		{"claude-code", RuntimeKindStreamingStdio, false},
		{"claude-code", RuntimeKindServeHTTP, false}, // provider must also match
		{"", RuntimeKindServeHTTP, false},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+string(tc.kind), func(t *testing.T) {
			assert.Equal(t, tc.want, shouldDropBootDirExtraArgs(tc.provider, tc.kind))
		})
	}
}

// TestAdapterFor_CodexSandboxMode locks the security-sensitive sandbox map:
// codex's OS sandbox is disabled (danger-full-access) ONLY when the operator
// explicitly opted into bypassPermissions, and only on the app-server
// (jsonrpc-stdio) path. Non-bypass profiles must keep codex's default
// sandbox; the subprocess path is unaffected. Easy to break on a refactor,
// so pinned. (Copilot PR #93.)
func TestAdapterFor_CodexSandboxMode(t *testing.T) {
	t.Run("bypass → danger-full-access", func(t *testing.T) {
		profile := config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions"}
		adapter, _, err := adapterFor(profile, "executor", RuntimeKindJsonRpcStdio)
		require.NoError(t, err)
		ca, ok := adapter.(*provider.CodexAdapter)
		require.True(t, ok, "adapter should be *provider.CodexAdapter, got %T", adapter)
		assert.Equal(t, "danger-full-access", ca.SandboxMode,
			"bypassPermissions must map to codex no-sandbox so the MCP loopback is reachable")
	})

	// Non-bypass profiles keep codex's default sandbox (empty SandboxMode →
	// go-providers resolves it to workspace-write). The sandbox must NOT be
	// disabled implicitly.
	for _, pm := range []string{"", "acceptEdits", "default"} {
		t.Run("non-bypass keeps default sandbox: "+pm, func(t *testing.T) {
			profile := config.AgentProfile{Provider: "codex", PermissionMode: pm}
			adapter, _, err := adapterFor(profile, "executor", RuntimeKindJsonRpcStdio)
			require.NoError(t, err)
			ca, ok := adapter.(*provider.CodexAdapter)
			require.True(t, ok)
			assert.Empty(t, ca.SandboxMode,
				"non-bypass profile must not disable codex's sandbox")
		})
	}

	t.Run("subprocess path unaffected", func(t *testing.T) {
		profile := config.AgentProfile{Provider: "codex", PermissionMode: "bypassPermissions"}
		adapter, _, err := adapterFor(profile, "executor", RuntimeKindSubprocess)
		require.NoError(t, err)
		ca, ok := adapter.(*provider.CodexAdapter)
		require.True(t, ok)
		assert.Empty(t, ca.SandboxMode,
			"the bypass→sandbox map is only on the jsonrpc-stdio app-server path")
	})
}

// TestComposeBuildArgs_ModelComesFromTheAdapter pins where the profile's
// model reaches the per-turn argv: applyProfileOptions sets it on every
// adapter, whose convention places it before the prompt in the provider's
// own spelling, and composeBuildArgs adds no model flag of its own. A
// trailing generic --model would land after the positional prompt or after
// `-- <prompt>` (argv corruption) or duplicate the flag (CW-20261001-0094).
func TestComposeBuildArgs_ModelComesFromTheAdapter(t *testing.T) {
	cases := []struct {
		name    string
		profile config.AgentProfile
		kind    RuntimeKind
		flag    string
		value   string
	}{
		{"opencode run", config.AgentProfile{Provider: "opencode", Model: "opencode/big-pickle"}, RuntimeKindSubprocess, "--model", "opencode/big-pickle"},
		{"codex exec", config.AgentProfile{Provider: "codex", Model: "gpt-test"}, RuntimeKindSubprocess, "-c", `model="gpt-test"`},
		{"claude streaming", config.AgentProfile{Provider: "claude-code", Model: "claude-test"}, RuntimeKindStreamingStdio, "--model", "claude-test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, _, err := adapterFor(tc.profile, "executor", tc.kind)
			require.NoError(t, err)
			args := composeBuildArgs(buildArgsParams{Adapter: adapter, Profile: tc.profile, TurnPrompt: "do the thing"})

			count := 0
			for i, a := range args {
				if a == tc.flag && i+1 < len(args) && args[i+1] == tc.value {
					count++
				}
			}
			assert.Equal(t, 1, count, "argv should carry %s %s exactly once: %q", tc.flag, tc.value, args)
			if p := slices.Index(args, "do the thing"); p >= 0 {
				assert.Less(t, slices.Index(args, tc.value), p, "the model must precede the prompt: %q", args)
			}
		})
	}

	t.Run("empty model: no model flag", func(t *testing.T) {
		profile := config.AgentProfile{Provider: "opencode"}
		adapter, _, err := adapterFor(profile, "executor", RuntimeKindSubprocess)
		require.NoError(t, err)
		args := composeBuildArgs(buildArgsParams{Adapter: adapter, Profile: profile, TurnPrompt: "hello"})
		assert.NotContains(t, args, "--model")
	})
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
// TORQUE_TASK_ID/RUN_ID → opts.Env. Agent-file env was dropped from the
// per-call surface for V1 (caller can stamp via opts.Env directly).
//
// Also asserts the CW-20260509-0011 provider-auth-passthrough contract:
// ANTHROPIC_API_KEY (and the other portfolio provider auth env vars listed
// in providerAuthEnvVars) MUST survive composeEnv. Pre-fix, they were
// stripped by LooksLikeSecret because their names contain "API_KEY" /
// "TOKEN" — bare-mode claude in the daemon-spawned subprocess then failed
// with "Not logged in".
func TestComposeEnv(t *testing.T) {
	t.Setenv("TORQUE_TEST_MARKER", "yes")
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
	assert.Equal(t, "CW-1", asMap["TORQUE_TASK_ID"])
	assert.Equal(t, "42", asMap["TORQUE_RUN_ID"])
	assert.Equal(t, "bar", asMap["FOO"])
	assert.Equal(t, "yes", asMap["TORQUE_TEST_MARKER"])
	assert.Equal(t, "secret-should-survive", asMap["ANTHROPIC_API_KEY"],
		"ANTHROPIC_API_KEY must survive composeEnv per CW-20260509-0011 (provider-auth passthrough)")

	// No Workdir on this Options — the work-root pointers must be absent
	// rather than set to an empty string.
	_, hasWork := asMap["TORQUE_WORK_ROOT"]
	assert.False(t, hasWork, "TORQUE_WORK_ROOT must be omitted when Workdir is empty")
}

// TestComposeEnvWorkRoots pins the TORQUE_WORK_ROOT / TORQUE_REPO_ROOT
// pointers: an agent's process cwd is the ephemeral boot dir, so these vars
// are how a run learns where its work_root actually is. In worktree mode
// (RepoRoot set distinct from Workdir) the two vars must differ.
func TestComposeEnvWorkRoots(t *testing.T) {
	envMap := func(out []string) map[string]string {
		m := map[string]string{}
		for _, kv := range out {
			if i := strings.Index(kv, "="); i >= 0 {
				m[kv[:i]] = kv[i+1:]
			}
		}
		return m
	}

	// Shared mode: no RepoRoot → both pointers resolve to Workdir.
	shared := envMap(composeEnv(config.AgentProfile{}, Options{
		TaskID:  "CW-1",
		Workdir: "/repo/checkout",
	}, nil))
	assert.Equal(t, "/repo/checkout", shared["TORQUE_WORK_ROOT"])
	assert.Equal(t, "/repo/checkout", shared["TORQUE_REPO_ROOT"])

	// Worktree mode: Workdir is the per-run worktree, RepoRoot the canonical
	// checkout — the two pointers must be distinct.
	wt := envMap(composeEnv(config.AgentProfile{}, Options{
		TaskID:   "CW-1",
		Workdir:  "/repo/checkout-worktrees-run-7",
		RepoRoot: "/repo/checkout",
	}, nil))
	assert.Equal(t, "/repo/checkout-worktrees-run-7", wt["TORQUE_WORK_ROOT"])
	assert.Equal(t, "/repo/checkout", wt["TORQUE_REPO_ROOT"])
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
	deps := &Dependencies{WorkspacesRoot: testenv.WorkspacesRoot(t)}
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
	mgr := NewManager(&Dependencies{WorkspacesRoot: testenv.WorkspacesRoot(t)})
	swept, err := mgr.Sweep()
	assert.NoError(t, err)
	assert.Equal(t, 0, swept)
}

package agent

import (
	"fmt"

	"github.com/hollis-labs/agentkit/agentruntime/runtimebind"
	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/torque/internal/config"
)

// adapterFor maps a profile's provider name + the resolved RuntimeKind
// to a go-providers CLIAdapter and the agentsessions Runtime
// capabilities the lib should declare. The RuntimeKind selects which
// constructor variant fires (e.g. claude PTY vs Bare; codex
// subprocess vs app-server) and the capability set the Runtime
// publishes (which the lib reads to pick the session implementation).
//
// profileName is the torque agent-profile lookup key. Most adapters
// ignore it; OpencodeAdapter requires it because `opencode run`
// dispatches via `--agent <name>`, and by convention the torque
// profile name is the opencode agent name.
//
// Per-provider runtime-kind support:
//
//   - claude:      Retired (bare + PTY removed 2026-05-16). Use
//     claude-code for the streaming-stdio claude path.
//   - claude-code: StreamingStdio only. Other kinds error — claude-code
//     is a long-lived NDJSON-over-stdin shape, not a
//     print-mode subprocess.
//   - codex:       Subprocess (print-mode), JsonRpcStdio (app-server).
//     The default per selectRuntimeKind is JsonRpcStdio;
//     operators can opt back to print-mode by setting
//     profile.RuntimeKind: subprocess.
//   - opencode:    Subprocess (default, `opencode run --agent <name>`)
//     or ServeHTTP (opt-in via profile.RuntimeKind=
//     serve-http; spawns `opencode serve` and attaches
//     via the child's HTTP API for long-lived multi-
//     turn workers). The default per selectRuntimeKind
//     stays Subprocess for back-compat; operators flip
//     to ServeHTTP per-profile.
//   - gemini:      Unsupported (PTY adapter removed in go-providers
//     v0.12.0).
//   - copilot:     Unsupported (same as gemini).
func adapterFor(profile config.AgentProfile, profileName string, kind RuntimeKind) (provider.CLIAdapter, agentsessions.Capabilities, error) {
	baseCaps := capabilitiesForRuntimeKind(kind)

	switch profile.Provider {
	case "claude":
		// Bare-mode claude (subprocess-per-turn) and claude PTY were
		// retired 2026-05-16. Bare mode's unsolved problem was the
		// OAuth/"Not logged in" auth gap; claude-code (streaming-stdio)
		// does not have it and is the supported claude path. PTY driving
		// was never proven for the dogfooded providers.
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"bare claude provider retired 2026-05-16; use provider=claude-code (streaming-stdio)")

	case "claude-code":
		// StreamingStdio long-lived NDJSON-over-stdin runtime (Anthropic's
		// "Streaming Input Mode (Default & Recommended)" per the Agent SDK
		// docs). `claude -p --input-format stream-json --output-format
		// stream-json --verbose` — one long-lived process, KV-cache reused
		// across turns until stdin EOF. The go-agent-sessions streamingStdio
		// runtime owns the stdin loop, attach fan-out, and session-id handling.
		//
		// Reference shape: agent-mux v005-07 `newClaudeCodeRuntime`
		// (internal/app/service.go:171-181) using gop.NewClaudeAdapterStreamingStdio()
		// + Caps.StreamingStdio: true. Substrate unblocked since
		// go-providers v0.17.0 + go-agent-sessions v0.9.x.
		//
		// Critically: does NOT pass `--bare`. Claude's normal config
		// discovery applies — cwd-local `.claude/settings.json` (planted by
		// the same BootDirSpec the bare path uses, includes apiKeyHelper if
		// threaded) AND operator-global `~/.claude.json` keychain auth are
		// both honored. This sidesteps the bare-mode "Not logged in" gap
		// surfaced by CW-20260513-0015 smoke 2026-05-12.
		if kind != RuntimeKindStreamingStdio {
			return nil, agentsessions.Capabilities{}, fmt.Errorf(
				"claude-code provider only supports runtime kind streaming-stdio; got %q", string(kind))
		}
		caps := baseCaps
		// CheckpointResume: false — claude `--resume <id>` semantics differ
		// in streaming mode (re-injects context every turn vs KV-cache
		// reuse). Conservative default; revisit when the long-lived resume
		// path is empirically validated.
		if profileIsDevMode(profile) {
			// Dev profiles carry --dangerously-skip-permissions; the dev
			// adapter sets SkipPermissions, which go-providers plants as
			// permissions.defaultMode=bypassPermissions. Leave PermissionMode
			// unset so that back-compat path stands.
			return provider.NewClaudeAdapterDevStreamingStdio(), caps, nil
		}
		// Thread the profile's permission mode into the adapter. Since
		// go-providers v0.19.0, ClaudeAdapter.PermissionMode plants
		// permissions.defaultMode directly into the .claude/settings.json —
		// no post-Plant settings.json rewrite needed (CW-20260517-0038).
		claudeAdapter := provider.NewClaudeAdapterStreamingStdio()
		claudeAdapter.PermissionMode = string(profile.ResolvedPermissionMode())
		return claudeAdapter, caps, nil

	case "codex":
		// codex supports Subprocess (print-mode: `codex exec`) and
		// JsonRpcStdio (app-server: `codex app-server`). go-providers
		// v0.17.1 ships `NewCodexAdapterAppServer()`; the underlying
		// `codex app-server` process speaks JSON-RPC 2.0 over stdio
		// with thread persistence in memory until 30-min idle.
		// `thread/start` + `thread/resume` are JSON-RPC methods, not
		// CLI flags — so per-turn params are intentionally dropped from
		// BuildArgs. Turn delivery in JsonRpcStdio mode goes through
		// SendTurn (in this package), NOT mgr.SendInput (which is the
		// JSON-RPC raw-bytes escape hatch).
		switch kind {
		case RuntimeKindJsonRpcStdio:
			appServer := provider.NewCodexAdapterAppServer()
			policy := runtimebind.ResolveCodexPolicy(runtimebind.CodexPolicyRequest{
				Runtime: runtimekind.JSONRPCStdio,
				Bypass:  profile.ResolvedPermissionMode() == config.PermissionModeBypass,
			})
			if profile.ResolvedPermissionMode() == config.PermissionModeBypass {
				appServer.SandboxMode = policy.SandboxMode
			}
			return appServer, baseCaps, nil
		case RuntimeKindSubprocess, "":
			return provider.NewCodexAdapter(), baseCaps, nil
		default:
			return nil, agentsessions.Capabilities{}, fmt.Errorf(
				"codex provider does not support runtime kind %q; supported: subprocess, jsonrpc-stdio", string(kind))
		}

	case "gemini":
		// gemini PTY adapter dropped in go-providers v0.12.0 (unused PTY-only
		// adapter cleanup). Profiles wired to "gemini" must migrate to a
		// supported provider or restore the adapter in a future go-providers
		// release. Treated here as a permanent-error provider.
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"gemini provider not supported (PTY adapter removed in go-providers v0.12.0); migrate the profile to a supported provider")

	case "copilot":
		// copilot PTY adapter dropped in go-providers v0.12.0 (same as
		// gemini). See comment above.
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"copilot provider not supported (PTY adapter removed in go-providers v0.12.0); migrate the profile to a supported provider")

	case "opencode":
		if profileName == "" {
			return nil, agentsessions.Capabilities{}, fmt.Errorf(
				"opencode provider requires Options.AgentProfile to be set (maps to opencode --agent)")
		}
		switch kind {
		case RuntimeKindSubprocess, "":
			// Subprocess (default): one-shot `opencode run --agent <name>`
			// per turn. Suitable for bounded mechanical tasks
			// (--no-pipeline). For V2-pipeline multi-turn workers use
			// the ServeHTTP runtime instead.
			adapter := provider.NewOpencodeAdapter()
			adapter.Agent = profileName
			// Thread profile.Model through so OpencodeAdapter.BuildArgs
			// emits `--model <X>` BEFORE the positional prompt — opencode
			// requires the model flag to precede the message arg. The
			// generic `--model` suffix in agent.Boot's BuildArgs wrapper
			// is suppressed for opencode (see boot.go's skipModelSuffix
			// branch); without this assignment opencode would launch
			// with whatever default the agent's opencode.json declares,
			// ignoring the profile's Model field entirely.
			adapter.Model = profile.Model
			return adapter, baseCaps, nil
		case RuntimeKindServeHTTP:
			// Long-lived: spawn `opencode serve --port 0 --hostname
			// 127.0.0.1`; go-agent-sessions' serveHttpSession (v0.10.0)
			// captures the bound port from stdout, then attaches via
			// the child's HTTP API for session + message endpoints +
			// SSE streaming. The adapter (go-providers v0.23.0's
			// NewOpencodeAdapterServeHTTP) only owns the argv shape;
			// the I/O loop, attach fan-out, and session-id handling
			// live in the consumer runtime.
			adapter := provider.NewOpencodeAdapterServeHTTP()
			adapter.Agent = profileName
			adapter.Model = profile.Model
			return adapter, baseCaps, nil
		default:
			return nil, agentsessions.Capabilities{}, fmt.Errorf(
				"opencode provider does not support runtime kind %q; supported: subprocess, serve-http", string(kind))
		}

	case "":
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"profile has empty provider; agent.Boot requires a go-providers-known provider name (claude|claude-code|codex|gemini|copilot|opencode)")

	default:
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"unknown provider %q; agent.Boot accepts: claude, claude-code, codex, gemini, copilot, opencode",
			profile.Provider)
	}
}

// shouldDropBootDirExtraArgs reports whether the bootdir-derived ExtraArgs
// (providerplant's prepared.Argv[1:], e.g. opencode's `--dir <projectDir>`)
// must be suppressed for the given provider + runtime kind before they are
// spliced onto StartOptions.ExtraArgs.
//
// opencode serve-http is the only case today: `opencode serve` rejects the
// `--dir` flag (a `run`-only flag) and exits printing help-to-stderr, which
// surfaces as the go-agent-sessions "serve-http start: process exited before
// printing listen URL" failure. The projectDir is already conveyed via spawn
// cwd + OPENCODE_CONFIG_DIR, so dropping the splice is safe. Subprocess
// opencode (and every other provider/runtime) keeps its ExtraArgs.
//
// The proper substrate fix is suppressing ProjectDirArg in go-agent-launch
// providerplant when Runtime==ServeHTTP; this predicate is the Torque-side
// interim and the single place the decision lives. Refs CW-20260521-0022.
func shouldDropBootDirExtraArgs(provider string, kind RuntimeKind) bool {
	return provider == "opencode" && kind == RuntimeKindServeHTTP
}

// profileIsDevMode reports whether the profile opts into Claude's
// `--dangerously-skip-permissions` developer-mode flag. Forked from
// cliexec.ProfileIsDevMode (which is being deleted in P6).
func profileIsDevMode(profile config.AgentProfile) bool {
	for _, a := range profile.Args {
		if a == "--dangerously-skip-permissions" {
			return true
		}
	}
	return false
}

// ProfileIsDevMode is exported so external callers (e.g. anyone migrating
// off of cliexec.ProfileIsDevMode) can re-derive the predicate.
func ProfileIsDevMode(profile config.AgentProfile) bool {
	return profileIsDevMode(profile)
}

// profileArgsExcludingDevFlag returns profile.Args with
// --dangerously-skip-permissions stripped. The dev flag is consumed by
// adapterFor to pick NewClaudeAdapterDev; passing it through profile.Args
// would double-add the flag.
func profileArgsExcludingDevFlag(profile config.AgentProfile) []string {
	if len(profile.Args) == 0 {
		return nil
	}
	out := make([]string, 0, len(profile.Args))
	for _, a := range profile.Args {
		if a == "--dangerously-skip-permissions" {
			continue
		}
		out = append(out, a)
	}
	return out
}

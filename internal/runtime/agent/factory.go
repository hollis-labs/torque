package agent

import (
	"fmt"

	"github.com/hollis-labs/go-agent-sessions/agentsessions"
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
//   - claude:      Subprocess (bare), PTY. Bare is the production
//     default; PTY remains an operator escape hatch.
//   - claude-code: StreamingStdio only. Other kinds error — claude-code
//     is a long-lived NDJSON-over-stdin shape, not a
//     print-mode subprocess.
//   - codex:       Subprocess (print-mode), JsonRpcStdio (app-server).
//     The default per selectRuntimeKind is JsonRpcStdio;
//     operators can opt back to print-mode by setting
//     profile.RuntimeKind: subprocess.
//   - opencode:    Subprocess only. No long-lived adapter exists in
//     go-providers today.
//   - gemini:      Unsupported (PTY adapter removed in go-providers
//     v0.12.0).
//   - copilot:     Unsupported (same as gemini).
//
// Subprocess-per-turn (non-PTY/non-long-lived) claude paths use the
// v0.9.1+ bare-mode constructors. Bare mode emits --bare plus four
// explicit-injection flags (--mcp-config / --append-system-prompt-file
// / --settings / --add-dir) and skips the CLI's auto-discovery of
// operator config (~/.claude/settings.json, ~/.claude.json, hooks,
// plugins, MCP, OAuth, keychain, CLAUDE.md auto-find). This obsoletes
// the operator-config-bleed-through class for bare consumers
// (CW-20260508-0019).
//
// Bare-mode adapters returned here are pre-injection. Under the lib's
// AutoPlantBootDir, agentsessions.preparePlant clones the adapter per
// session and threads MCPConfigPath / AppendSystemPromptFile /
// SettingsPath / ProjectDir from BareInjectionPaths(<plantedBootDir>,
// opts.Workdir). The runtime-level adapter stays untouched, so
// concurrent sessions don't race on shared adapter state.
func adapterFor(profile config.AgentProfile, profileName string, kind RuntimeKind) (provider.CLIAdapter, agentsessions.Capabilities, error) {
	baseCaps := capabilitiesForRuntimeKind(kind)

	switch profile.Provider {
	case "claude":
		// claude supports Subprocess (bare) + PTY today. CheckpointResume +
		// ProviderSessionID layer on top of the base caps regardless of kind.
		caps := baseCaps
		caps.ProviderSessionID = true
		caps.CheckpointResume = true
		dev := profileIsDevMode(profile)
		switch kind {
		case RuntimeKindPTY:
			if dev {
				return provider.NewClaudeAdapterDevPTY(), caps, nil
			}
			return provider.NewClaudeAdapterPTY(), caps, nil
		case RuntimeKindSubprocess, "":
			if dev {
				return provider.NewClaudeAdapterDevBare(), caps, nil
			}
			return provider.NewClaudeAdapterBare(), caps, nil
		default:
			return nil, agentsessions.Capabilities{}, fmt.Errorf(
				"claude provider does not support runtime kind %q; supported: subprocess, pty (use provider=claude-code for streaming-stdio)", string(kind))
		}

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
			return provider.NewClaudeAdapterDevStreamingStdio(), caps, nil
		}
		return provider.NewClaudeAdapterStreamingStdio(), caps, nil

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
			return provider.NewCodexAdapterAppServer(), baseCaps, nil
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
		if kind != RuntimeKindSubprocess && kind != "" {
			return nil, agentsessions.Capabilities{}, fmt.Errorf(
				"opencode provider only supports runtime kind subprocess; got %q (no long-lived adapter in go-providers today)", string(kind))
		}
		adapter := provider.NewOpencodeAdapter()
		adapter.Agent = profileName
		// Thread profile.Model through so OpencodeAdapter.BuildArgs emits
		// `--model <X>` BEFORE the positional prompt — opencode requires
		// the model flag to precede the message arg. The generic
		// `--model` suffix in agent.Boot's BuildArgs wrapper is suppressed
		// for opencode (see boot.go's skipModelSuffix branch); without
		// this assignment opencode would launch with whatever default
		// the agent's opencode.json declares, ignoring the profile's
		// Model field entirely.
		adapter.Model = profile.Model
		return adapter, baseCaps, nil

	case "":
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"profile has empty provider; agent.Boot requires a go-providers-known provider name (claude|claude-code|codex|gemini|copilot|opencode)")

	default:
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"unknown provider %q; agent.Boot accepts: claude, claude-code, codex, gemini, copilot, opencode",
			profile.Provider)
	}
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

package agent

import (
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
)

// adapterFor maps a profile's provider name to a go-providers CLIAdapter and
// the static capability set its agentsessions Runtime should declare.
//
// profileName is the clockwork agent-profile lookup key. Most adapters ignore
// it; OpencodeAdapter requires it because `opencode run` dispatches via
// `--agent <name>`, and by convention the clockwork profile name is the
// opencode agent name.
//
// pty signals which ClaudeAdapter constructor to pick: PTY-mode emits
// interactive args (no -p / --print / --output-format / --verbose / --system-
// prompt) per go-providers v0.8.1; subprocess-per-turn emits the print-mode
// args. Non-claude providers (codex / opencode / gemini / copilot) ignore the
// argument for *adapter constructor selection* — they expose a single
// adapter type today and don't need a PTY-vs-print-mode constructor split.
// Their runtime mode is still controlled by Caps.PTY (which shouldUsePTY can
// flip per-profile via profile.PTY=true), so an operator can opt them into
// PTY runtime even though the adapter doesn't change shape. Caps.PTY itself
// is set by the caller via shouldUsePTY; this argument keeps claude's adapter
// wiring in lockstep with that decision.
//
// Subprocess-per-turn (non-PTY) claude paths use v0.9.1+ bare-mode
// constructors. Bare mode emits --bare plus four explicit-injection flags
// (--mcp-config / --append-system-prompt-file / --settings / --add-dir) and
// skips the CLI's auto-discovery of operator config (~/.claude/settings.json,
// ~/.claude.json, hooks, plugins, MCP, OAuth, keychain, CLAUDE.md auto-find).
// This obsoletes the operator-config-bleed-through class for bare consumers
// (CW-20260508-0019). PTY paths stay non-bare — bare mode is print-mode-
// focused per Anthropic's docs and the PTY/TUI shape doesn't accept --bare.
//
// v0.9.1 fixed the .mcp.json HTTP-loopback shape (CW-20260509-0003): the
// loopback entry now emits `{"type": "http", "url": "..."}` which bare-mode
// strict validation accepts.
//
// Bare-mode adapters returned here are pre-injection. Under the lib's
// AutoPlantBootDir, agentsessions.preparePlant clones the adapter per
// session and threads MCPConfigPath / AppendSystemPromptFile / SettingsPath
// / ProjectDir from BareInjectionPaths(<plantedBootDir>, opts.Workdir).
// The runtime-level adapter stays untouched, so concurrent sessions don't
// race on shared adapter state.
func adapterFor(profile config.AgentProfile, profileName string, pty bool) (provider.CLIAdapter, agentsessions.Capabilities, error) {
	switch profile.Provider {
	case "claude":
		caps := agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			CheckpointResume:  true,
		}
		dev := profileIsDevMode(profile)
		switch {
		case pty && dev:
			return provider.NewClaudeAdapterDevPTY(), caps, nil
		case pty:
			return provider.NewClaudeAdapterPTY(), caps, nil
		case dev:
			return provider.NewClaudeAdapterDevBare(), caps, nil
		default:
			return provider.NewClaudeAdapterBare(), caps, nil
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
		caps := agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			StreamingStdio:    true,
			// CheckpointResume: false — claude `--resume <id>` semantics
			// differ in streaming mode (re-injects context every turn vs
			// KV-cache reuse). Conservative default; revisit when the
			// long-lived resume path is empirically validated.
		}
		if profileIsDevMode(profile) {
			return provider.NewClaudeAdapterDevStreamingStdio(), caps, nil
		}
		return provider.NewClaudeAdapterStreamingStdio(), caps, nil

	case "codex":
		return provider.NewCodexAdapter(), agentsessions.Capabilities{
			BinaryRequired: true,
		}, nil

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
		return adapter, agentsessions.Capabilities{
			BinaryRequired: true,
		}, nil

	case "":
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"profile has empty provider; agent.Boot requires a go-providers-known provider name (claude|claude-code|codex|gemini|copilot|opencode)")

	default:
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"unknown provider %q; agent.Boot accepts: claude, claude-code, codex, gemini, copilot, opencode",
			profile.Provider)
	}
}

// shouldUsePTY returns true when the Mode + provider + profile combination
// should opt into Caps.PTY=true (long-lived PTY runtime) on go-agent-sessions
// v0.6.0.
//
// Decision priority (highest first):
//  1. opts.SubprocessPerTurnOverride forces subprocess-per-turn (escape hatch).
//  2. ModeOneShot is always subprocess-per-turn (single turn, auto-stop;
//     PTY would force the lib to keep the process alive across turns).
//  3. profile.PTY (when non-nil) is the explicit operator override:
//     `true` forces PTY (subject to #1/#2 above); `false` forces subprocess.
//  4. Per-provider matrix decides when profile.PTY is nil. All providers
//     default to subprocess-per-turn today — claude's long-lived
//     "session" semantics are delivered via `--resume <session_id>` chaining
//     across subprocess turns, NOT via PTY/TUI. Programmatic auto-fire
//     against the claude TUI is unproven (mux's claudecode is human-driven
//     with empty bootstrap.prompt_prefix; the lib's AutoFireFirstTurn
//     SendInput lands in the TUI's input box but doesn't submit, and the
//     stdin-pipe BootMode pre-write similarly stalls because claude's TUI
//     reads its raw-mode input AFTER initialization). Operators can flip
//     `pty: true` per-profile to experiment.
//
// profile.PTY is *bool so yaml can distinguish "absent" (nil → matrix) from
// "explicitly false" (force subprocess) — see the AgentProfile.PTY godoc for
// the schema rationale.
func shouldUsePTY(mode Mode, provider string, profilePTY *bool, override bool) bool {
	if override {
		return false
	}
	if mode == ModeOneShot {
		return false
	}
	if profilePTY != nil {
		return *profilePTY
	}
	// All providers (claude / codex / opencode / gemini / copilot):
	// subprocess-per-turn by default until programmatic TUI driving is solved.
	_ = provider
	return false
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

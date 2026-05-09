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
// Bare-mode adapters returned here are NOT yet ready to spawn — the four
// injection-path fields (MCPConfigPath / AppendSystemPromptFile / SettingsPath
// / ProjectDir) must be populated post-plantBootDir via
// (*provider.ClaudeAdapter).BareInjectionPaths(layout.BootDir, opts.Workdir).
// See agent.Boot for the field-population call site.
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

	case "codex":
		return provider.NewCodexAdapter(), agentsessions.Capabilities{
			BinaryRequired: true,
		}, nil

	case "gemini":
		return provider.NewGeminiAdapter(), agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			CheckpointResume:  true,
		}, nil

	case "copilot":
		return provider.NewCopilotAdapter(), agentsessions.Capabilities{
			BinaryRequired: true,
		}, nil

	case "opencode":
		if profileName == "" {
			return nil, agentsessions.Capabilities{}, fmt.Errorf(
				"opencode provider requires Options.AgentProfile to be set (maps to opencode --agent)")
		}
		adapter := provider.NewOpencodeAdapter()
		adapter.Agent = profileName
		return adapter, agentsessions.Capabilities{
			BinaryRequired: true,
		}, nil

	case "":
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"profile has empty provider; agent.Boot requires a go-providers-known provider name (claude|codex|gemini|copilot|opencode)")

	default:
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"unknown provider %q; agent.Boot accepts: claude, codex, gemini, copilot, opencode",
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

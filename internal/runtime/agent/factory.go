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
// Caps.PTY is NOT set here — Boot picks it per-Mode (claude long-lived modes
// opt into PTY=true; everything else stays subprocess-per-turn until per-
// adapter PTY shape is verified). Forked from internal/runtime/cliexec/adapter.go
// with the Caps.PTY decision moved to the call site.
func adapterFor(profile config.AgentProfile, profileName string) (provider.CLIAdapter, agentsessions.Capabilities, error) {
	switch profile.Provider {
	case "claude":
		caps := agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			CheckpointResume:  true,
		}
		if profileIsDevMode(profile) {
			return provider.NewClaudeAdapterDev(), caps, nil
		}
		return provider.NewClaudeAdapter(), caps, nil

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
//  4. Per-provider matrix decides when profile.PTY is nil. Today only claude
//     has its long-lived PTY shape verified across the portfolio (mux's
//     claudecode is the reference). Other providers stay subprocess-per-turn
//     until each adapter's PTY interactions are probed.
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
	switch provider {
	case "claude":
		return true
	default:
		// codex/opencode/gemini/copilot: subprocess-per-turn until per-
		// adapter PTY shape is verified.
		return false
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

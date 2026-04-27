package cliexec

import (
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
)

// adapterFor maps a profile's provider name to a go-providers CLIAdapter and
// the static capability set its agentsessions Runtime should declare. Returns
// an error for unknown providers; pre-launch we accept a clean break and
// require profiles to name a known go-providers adapter rather than wedging a
// generic raw-command path through the new substrate.
func adapterFor(profile config.AgentProfile) (provider.CLIAdapter, agentsessions.Capabilities, error) {
	switch profile.Provider {
	case "claude":
		caps := agentsessions.Capabilities{
			BinaryRequired:    true,
			ProviderSessionID: true,
			CheckpointResume:  true,
		}
		if devModeEnabled(profile) {
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

	// Note: opencode is dispatched by the dedicated executor-opencode plugin
	// today. Phase D (CW-20260427-0042) will fold it into cliexec once
	// go-providers cuts a tag containing the OpencodeAdapter (currently on
	// main past v0.5.1, commit 8b6e673).

	case "":
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"profile has empty provider; cliexec requires a go-providers-known provider name (claude|codex|gemini|copilot)")

	default:
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"unknown provider %q; cliexec accepts: claude, codex, gemini, copilot",
			profile.Provider)
	}
}

// devModeEnabled reports whether the profile's args contain Claude's
// developer-mode flag. The flag was historically set via profile.args; we
// honor that until profiles migrate to a typed dev-mode field.
func devModeEnabled(profile config.AgentProfile) bool {
	for _, a := range profile.Args {
		if a == "--dangerously-skip-permissions" {
			return true
		}
	}
	return false
}

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
//
// profileName is the clockwork agent-profile lookup key (job.AgentProfile).
// Most adapters ignore it; OpencodeAdapter requires it because `opencode run`
// dispatches via --agent <name>, and by convention the clockwork profile name
// is the opencode agent name (matches the legacy executor-opencode plugin).
func adapterFor(profile config.AgentProfile, profileName string) (provider.CLIAdapter, agentsessions.Capabilities, error) {
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

	case "opencode":
		if profileName == "" {
			return nil, agentsessions.Capabilities{}, fmt.Errorf(
				"opencode provider requires task.agent_profile to be set (maps to opencode --agent)")
		}
		adapter := provider.NewOpencodeAdapter()
		adapter.Agent = profileName
		// OpencodeAdapter ParseLine emits only EventDelta (no EventSessionID),
		// and `opencode run` has no --resume flag — so ProviderSessionID and
		// CheckpointResume both stay false. --model is appended by the cliexec
		// BuildArgs wrapper from profile.Model (uniform across adapters);
		// OpencodeAdapter.Model is left zero to avoid a duplicate flag.
		return adapter, agentsessions.Capabilities{
			BinaryRequired: true,
		}, nil

	case "":
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"profile has empty provider; cliexec requires a go-providers-known provider name (claude|codex|gemini|copilot|opencode)")

	default:
		return nil, agentsessions.Capabilities{}, fmt.Errorf(
			"unknown provider %q; cliexec accepts: claude, codex, gemini, copilot, opencode",
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

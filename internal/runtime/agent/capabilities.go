package agent

import (
	"strings"

	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// ProviderCapabilities returns the static capability set for a single provider
// (the per-adapter answer the cli Executor.Capabilities() can't give because
// it's whole-executor scope).
//
// Consumers: Manager.ResumeSession reads ProviderCapabilities(profile.Provider).
// SupportsResume internally to pick the resume-vs-fresh-boot branch. Callers of
// ResumeSession therefore do NOT need to consult this surface — the branch is
// encapsulated. The capability is read once per resume, never per turn, and is
// the canonical answer (sprint α decision D4: no per-call probes, no
// try-resume-then-fallback runtime detection).
//
// Resume support matrix (sprint α — CW-20260512-0059):
//
//	claude      → true  (claude --resume <session-id>; documented in claude --help)
//	claude-code → true  (streaming-stdio claude; same --resume primitive — the
//	                     bare "claude" provider was retired 2026-05-16 and
//	                     claude-code is its supported successor)
//	codex       → true  (codex resume <session-id> subcommand; documented in codex --help)
//	gemini      → false (no native resume primitive; falls back to fresh-boot)
//	copilot     → false (no native resume; gemini-style fresh-boot fallback)
//	opencode    → false (no native resume primitive; falls back to fresh-boot)
//
// Unknown provider names report zero-value capabilities (all false). The
// caller is expected to validate provider name separately via adapterFor at
// dispatch time; this function is intentionally total to keep capability
// queries cheap and side-effect-free.
//
// Provider matching is case-insensitive + space-trimmed to match clientFor's
// normalization in executor-api.
func ProviderCapabilities(provider string) executor.ExecutorCapabilities {
	caps := executor.ExecutorCapabilities{
		// The cli executor's executor-wide flags hold across providers; the
		// only per-provider axis today is SupportsResume.
		SupportsStreaming:   true,
		SupportsTools:       true,
		SupportsSandbox:     false,
		SupportsPermissions: false,
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude", "claude-code", "codex":
		caps.SupportsResume = true
	case "gemini", "copilot", "opencode":
		caps.SupportsResume = false
	default:
		// Unknown provider — zero-value capability set (everything false).
		// Re-zero rather than leak the executor defaults; an unknown name is
		// not a known-good adapter and shouldn't appear capable of anything.
		return executor.ExecutorCapabilities{}
	}
	return caps
}

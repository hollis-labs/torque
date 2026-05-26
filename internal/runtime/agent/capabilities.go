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

// GenuinelyResumableProvider reports whether threading a persisted provider
// session-id via Options.ProviderSessionIDOverride actually restores prior
// context across a process death — i.e. the adapter consumes the preset and the
// provider rehydrates its own transcript on the next turn.
//
// This is deliberately NARROWER than ProviderCapabilities(provider).SupportsResume.
// That matrix reports codex as resume-capable (the `codex resume` subcommand
// exists), but on the current go-providers/go-agent-runtime pins codex runs in
// app-server (JSON-RPC) mode whose handshake (initialize → thread/start →
// turn/start) never issues thread/resume and ignores SessionIDPreset — so a
// codex "resume" silently starts a fresh thread (see boot.go's JsonRpcStdio
// kickoff branch and Options.ProviderSessionIDOverride's doc). Only
// claude/claude-code genuinely resume today (claude --resume reads ~/.claude
// project history, which survives the prior process).
//
// Callers that thread a resume hint for fidelity ON TOP OF the provider-agnostic
// recovery pack (e.g. planstart.Redispatch) must gate on THIS, not the
// aspirational SupportsResume matrix, so they don't claim a resume that codex
// won't honor. When go-agent-runtime gains a thread/resume wireup (followups.
// torque.codex_jsonrpc_resume_wireup) codex moves here too.
func GenuinelyResumableProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude", "claude-code":
		return true
	default:
		return false
	}
}

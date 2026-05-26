package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProviderCapabilities_Resume locks down the per-adapter SupportsResume
// matrix added in CW-20260512-0059 (sprint α decision D4). The reactor
// harness reads this to decide between ResumeSession+send_input vs fresh-boot
// when handling HITL responses, stuck-task probes, etc. — changing a value
// here changes the harness's runtime behavior for that provider.
//
// Resume support matrix:
//
//	claude   → true  (claude --resume <session-id>)
//	codex    → true  (codex resume <session-id> subcommand)
//	gemini   → false (no native resume primitive)
//	copilot  → false (no native resume primitive)
//	opencode → false (no native resume primitive)
func TestProviderCapabilities_Resume(t *testing.T) {
	cases := []struct {
		provider       string
		wantResume     bool
		reasonForFalse string
	}{
		{"claude", true, ""},
		{"codex", true, ""},
		{"gemini", false, "no native --resume; PTY adapter dropped in go-providers v0.12.0"},
		{"copilot", false, "no native --resume; PTY adapter dropped in go-providers v0.12.0"},
		{"opencode", false, "opencode CLI has no native session-resume primitive"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			caps := ProviderCapabilities(tc.provider)
			assert.Equal(t, tc.wantResume, caps.SupportsResume,
				"provider %q SupportsResume mismatch (reason for false: %s)", tc.provider, tc.reasonForFalse)
		})
	}
}

// TestGenuinelyResumableProvider locks down the NARROWER resume gate used by
// callers that thread a real --resume on top of the recovery pack
// (planstart.Redispatch). Unlike SupportsResume, codex is excluded here: its
// app-server runtime ignores the session-id preset on the current pins, so a
// "resume" silently no-ops. claude/claude-code only, until the codex JSON-RPC
// thread/resume wireup lands.
func TestGenuinelyResumableProvider(t *testing.T) {
	cases := []struct {
		provider string
		want     bool
	}{
		{"claude", true},
		{"claude-code", true},
		{"CLAUDE", true}, // case-insensitive + padding, like ProviderCapabilities
		{"  claude-code ", true},
		{"codex", false}, // SupportsResume=true but runtime no-ops the preset
		{"gemini", false},
		{"copilot", false},
		{"opencode", false},
		{"", false},
		{"unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			assert.Equal(t, tc.want, GenuinelyResumableProvider(tc.provider))
		})
	}
	// Invariant: genuine resume implies the matrix also reports SupportsResume
	// (genuine is a strict subset). The reverse need not hold (codex).
	for _, p := range []string{"claude", "claude-code"} {
		assert.True(t, ProviderCapabilities(p).SupportsResume,
			"GenuinelyResumableProvider(%q) must be a subset of SupportsResume", p)
	}
}

// TestProviderCapabilities_CaseInsensitive verifies provider name matching
// tolerates the same normalization clientFor applies (mixed case, padding).
// Profile loading doesn't normalize provider strings, so this is the load-
// bearing case for downstream dispatch.
func TestProviderCapabilities_CaseInsensitive(t *testing.T) {
	cases := []string{"CLAUDE", "Claude", "  claude  ", "CODEX", " Codex "}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			assert.True(t, ProviderCapabilities(name).SupportsResume,
				"normalization broke for %q", name)
		})
	}
}

// TestProviderCapabilities_Unknown returns the zero-value capability set for
// an unknown provider — capability queries are total and side-effect-free,
// the actual unknown-provider error is surfaced by adapterFor at dispatch.
func TestProviderCapabilities_Unknown(t *testing.T) {
	caps := ProviderCapabilities("not-a-provider")
	assert.False(t, caps.SupportsStreaming)
	assert.False(t, caps.SupportsTools)
	assert.False(t, caps.SupportsSandbox)
	assert.False(t, caps.SupportsPermissions)
	assert.False(t, caps.SupportsResume)
}

// TestProviderCapabilities_CrossAxisInvariants asserts the non-resume axes
// hold across all known providers (cli-executor-level invariants — these
// only diverge by provider for SupportsResume in sprint α).
func TestProviderCapabilities_CrossAxisInvariants(t *testing.T) {
	for _, p := range []string{"claude", "codex", "gemini", "copilot", "opencode"} {
		t.Run(p, func(t *testing.T) {
			caps := ProviderCapabilities(p)
			assert.True(t, caps.SupportsStreaming, "cli executor always streams")
			assert.True(t, caps.SupportsTools, "tool-broker is provider-agnostic")
			assert.False(t, caps.SupportsSandbox, "Phase F restoration is a future sprint")
			assert.False(t, caps.SupportsPermissions, "permissions live in the API executor today")
		})
	}
}

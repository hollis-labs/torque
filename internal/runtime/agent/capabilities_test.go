package agent

import (
	"slices"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
)

// Resume is derived from the go-providers registry's per-mode capabilities
// (CW-20261001-0174). Declared follows the descriptor for every runtime and
// mode it lists; Wired is the subset Torque genuinely resumes. Changing a
// Wired value changes what ResumeSession, the HITL dispatcher and planstart
// do for that runtime.
func TestResume_FromTheRegistry(t *testing.T) {
	type want struct{ declared, wired bool }
	expect := map[runtimes.ID]map[runtimes.Mode]want{
		runtimes.Claude: {
			runtimes.ModeStreamingStdio:    {true, true},
			runtimes.ModeSubprocessPerTurn: {true, true},
			runtimes.ModePTY:               {true, false}, // Torque has no PTY launch for Claude
			runtimes.ModeACPStdio:          {false, false},
		},
		runtimes.Codex: {
			runtimes.ModeJSONRPCStdio:      {true, false}, // CW-20261001-0180
			runtimes.ModeSubprocessPerTurn: {false, false},
			runtimes.ModeACPStdio:          {false, false},
		},
		runtimes.OpenCode: {
			runtimes.ModeSubprocessPerTurn: {true, true}, // opencode run --session <id>
			runtimes.ModeHTTPSSE:           {false, false},
			runtimes.ModeACPStdio:          {true, true}, // session/load
		},
		runtimes.Copilot: {
			runtimes.ModeACPStdio: {false, false},
			runtimes.ModeACPTCP:   {false, false},
		},
		runtimes.Pi: {
			runtimes.ModeACPStdio: {true, true}, // session/load
		},
		runtimes.Antigravity: {
			runtimes.ModeSubprocessPerTurn: {true, false}, // CW-20261001-0181
		},
	}
	for _, d := range registry.All() {
		modes, ok := expect[d.ID]
		require.True(t, ok, "runtime %s is in the registry but not in this table", d.ID)
		// The name Torque launches the runtime under: the id, or for Claude
		// its claude-code alias (the bare claude provider is retired).
		name := string(d.ID)
		for _, n := range append([]string{string(d.ID)}, d.Aliases...) {
			if slices.Contains(config.LaunchableProviders(), n) {
				name = n
				break
			}
		}
		for _, ms := range d.Modes {
			w, ok := modes[ms.Mode]
			require.True(t, ok, "%s/%s is in the registry but not in this table", d.ID, ms.Mode)
			got := Resume(name, RuntimeKind(ms.Mode))
			assert.Equal(t, d.Has(ms.Mode, runtimes.CapResume), got.Declared, "%s/%s: Declared follows the descriptor", d.ID, ms.Mode)
			assert.Equal(t, w.declared, got.Declared, "%s/%s declared", d.ID, ms.Mode)
			assert.Equal(t, w.wired, got.Wired, "%s/%s wired", d.ID, ms.Mode)
			if got.Declared && !got.Wired {
				assert.NotEmpty(t, got.NotWired, "%s/%s: a declared resume Torque does not wire says why", d.ID, ms.Mode)
			}
			assert.Equal(t, got.Wired, GenuinelyResumable(name, RuntimeKind(ms.Mode)))
			assert.Equal(t, got.Wired, ProviderCapabilities(name, RuntimeKind(ms.Mode)).SupportsResume)
		}
	}
}

// An empty kind is the runtime's registry default, as Boot resolves it;
// aliases resolve to their runtime; the retired bare claude provider, which
// Boot refuses, is not resumed.
func TestResume_KindAndNameResolution(t *testing.T) {
	assert.True(t, Resume("claude-code", "").Wired, "claude-code defaults to streaming-stdio")
	assert.True(t, Resume("open-code", "").Wired, "an alias resolves to its runtime (opencode, subprocess-per-turn)")
	assert.False(t, Resume("codex", "").Wired, "codex defaults to app-server, not wired yet")
	assert.True(t, Resume("codex", "").Declared)

	bare := Resume("claude", RuntimeKindStreamingStdio)
	assert.True(t, bare.Declared)
	assert.False(t, bare.Wired, "Boot refuses the bare claude provider")
	assert.Contains(t, bare.NotWired, "does not launch claude")
}

// A provider the registry does not know is not a known-good adapter.
func TestProviderCapabilities_Unknown(t *testing.T) {
	assert.Equal(t, ResumeSupport{}, Resume("not-a-provider", RuntimeKindStreamingStdio))
	caps := ProviderCapabilities("not-a-provider", "")
	assert.False(t, caps.SupportsResume)
	assert.False(t, caps.SupportsStreaming)
	assert.False(t, caps.SupportsTools)
	assert.False(t, GenuinelyResumable("gemini", ""), "gemini is not a registry runtime")
}

// The executor-wide flags hold for every known provider; only resume varies.
func TestProviderCapabilities_ExecutorFlags(t *testing.T) {
	for _, p := range []string{"claude-code", "codex", "opencode", "copilot", "pi", "agy"} {
		caps := ProviderCapabilities(p, "")
		assert.True(t, caps.SupportsStreaming, p)
		assert.True(t, caps.SupportsTools, p)
		assert.False(t, caps.SupportsSandbox, p)
		assert.False(t, caps.SupportsPermissions, p)
	}
}

// Two provider names are the same runtime when the registry says so: a
// session id one recorded means something to the other.
func TestSameRuntime(t *testing.T) {
	assert.True(t, sameRuntime("claude", "claude-code"))
	assert.True(t, sameRuntime("opencode", "open-code"))
	assert.False(t, sameRuntime("claude-code", "codex"))
	assert.False(t, sameRuntime("claude-code", ""))
	assert.False(t, sameRuntime("gemini", "gemini"), "unknown providers are no runtime")
}

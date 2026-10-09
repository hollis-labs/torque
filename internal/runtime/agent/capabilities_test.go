package agent

import (
	"slices"
	"testing"

	"github.com/hollis-labs/substrate/harness/adapters/registry"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"github.com/stretchr/testify/assert"

	"github.com/hollis-labs/torque/internal/config"
)

// Resume is derived from the go-providers registry's per-mode capabilities
// (CW-20261001-0174). Declared follows the descriptor for every runtime and
// mode it lists; Wired is the allow-list of pairs Torque genuinely resumes,
// so a mode a library bump adds stays unwired. Changing the wired set
// changes what ResumeSession, the HITL dispatcher and planstart do for
// that runtime.
func TestResume_FromTheRegistry(t *testing.T) {
	wired := map[runtimes.ID][]runtimes.Mode{
		runtimes.Claude:   {runtimes.ModeStreamingStdio, runtimes.ModeSubprocessPerTurn}, // --resume <id>
		runtimes.OpenCode: {runtimes.ModeSubprocessPerTurn, runtimes.ModeACPStdio},       // --session <id>, session/load
		runtimes.Pi:       {runtimes.ModeACPStdio},                                       // session/load
	}
	for _, d := range registry.All() {
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
			got := Resume(name, RuntimeKind(ms.Mode))
			assert.Equal(t, d.Has(ms.Mode, runtimes.CapResume), got.Declared, "%s/%s: Declared follows the descriptor", d.ID, ms.Mode)
			assert.Equal(t, slices.Contains(wired[d.ID], ms.Mode), got.Wired, "%s/%s wired", d.ID, ms.Mode)
			if got.Declared && !got.Wired {
				assert.NotEmpty(t, got.NotWired, "%s/%s: a declared resume Torque does not wire says why", d.ID, ms.Mode)
			}
			assert.Equal(t, got.Wired, GenuinelyResumable(name, RuntimeKind(ms.Mode)))
			assert.Equal(t, got.Wired, ProviderCapabilities(name, RuntimeKind(ms.Mode)).SupportsResume)
		}
	}
	// The ones Torque does not wire yet say which task tracks them.
	assert.Contains(t, Resume("codex", RuntimeKindJsonRpcStdio).NotWired, "CW-20261001-0180")
	assert.Contains(t, Resume("agy", "").NotWired, "CW-20261001-0181")
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

	// Names match in any case, as Boot's registry lookup does.
	assert.True(t, Resume("Claude-Code", "").Wired)
	assert.True(t, Resume("OPENCODE", RuntimeKindSubprocess).Wired)
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
	assert.True(t, SameRuntime("claude", "claude-code"))
	assert.True(t, SameRuntime("opencode", "open-code"))
	assert.True(t, SameRuntime("Claude-Code", "claude"), "in any case")
	assert.False(t, SameRuntime("claude-code", "codex"))
	assert.False(t, SameRuntime("claude-code", ""))
	assert.False(t, SameRuntime("gemini", "gemini"), "unknown providers are no runtime")
}

// go-providers v0.41.0 makes codex exec (subprocess-per-turn) resume its
// thread, and the registry declares it, but Torque does not wire it: no cold
// boot may hand a stored thread id to a fresh CODEX_HOME, where that thread
// does not exist. The allow-list is what keeps ResumeSession, Manager.Resume
// and planstart from passing one (CW-20261001-0255 tracks wiring it).
func TestResume_CodexExecIsDeclaredButNotWired(t *testing.T) {
	for _, kind := range []RuntimeKind{RuntimeKindSubprocess, ""} {
		got := Resume("codex", kind)
		if kind == "" {
			// codex's registry default is the app-server, which declares it too.
			got = Resume("codex", RuntimeKindJsonRpcStdio)
		}
		assert.True(t, got.Declared, "codex/%q", kind)
		assert.False(t, got.Wired, "codex/%q must not be wired", kind)
		assert.NotEmpty(t, got.NotWired)
		assert.False(t, GenuinelyResumable("codex", kind))
	}
}

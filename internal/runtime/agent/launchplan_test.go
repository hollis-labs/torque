package agent

import (
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeIDFor(t *testing.T) {
	for provider, want := range map[string]string{
		"claude-code": "claude",
		"claude":      "claude",
		"codex":       "codex",
		"opencode":    "opencode",
		"agy":         "antigravity",
		" Codex ":     "codex",
		// Not in the registry: passed through for the caller's own error.
		"gemini": "gemini",
		"":       "",
	} {
		assert.Equal(t, want, runtimeIDFor(provider), provider)
	}
}

func TestMapRuntimeKind_LeafModes(t *testing.T) {
	for kind, want := range map[RuntimeKind]runtimes.Mode{
		"":                        runtimes.ModeSubprocessPerTurn,
		RuntimeKindSubprocess:     runtimes.ModeSubprocessPerTurn,
		RuntimeKindPTY:            runtimes.ModePTY,
		RuntimeKindStreamingStdio: runtimes.ModeStreamingStdio,
		RuntimeKindJsonRpcStdio:   runtimes.ModeJSONRPCStdio,
		RuntimeKindServeHTTP:      runtimes.ModeHTTPSSE,
	} {
		got, err := mapRuntimeKind(kind)
		require.NoError(t, err, kind)
		assert.Equal(t, want, got, kind)
		assert.True(t, got.Valid(), kind)
	}
	// A token that was not normalized is refused, not passed to agentkit
	// (which would fail it as ErrUnknownRuntime further from the cause).
	_, err := mapRuntimeKind("subprocess")
	assert.Error(t, err)
}

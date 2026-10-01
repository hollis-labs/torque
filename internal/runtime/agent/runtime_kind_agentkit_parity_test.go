package agent

// Delete this file with the go-providers v0.31.0 + agentkit v0.12.0 bump:
// agentkit drops agentruntime/runtimekind there, and TestParseRuntimeKind
// keeps the coverage. Until then it proves Torque's own normalization
// (CW-20261001-0063) returns exactly what the agentkit-backed version did.

import (
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
	"github.com/stretchr/testify/assert"
)

// agentkitRuntimeKindFromConfigValue is runtimeKindFromConfigValue as it
// stood before CW-20261001-0063, verbatim.
func agentkitRuntimeKindFromConfigValue(raw string) RuntimeKind {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	if normalized == "" {
		return ""
	}
	parsed := runtimekind.Parse(normalized)
	if parsed == runtimekind.Unknown || string(parsed) != normalized {
		return RuntimeKind(normalized)
	}
	return RuntimeKind(parsed)
}

func TestParseRuntimeKindMatchesAgentkit(t *testing.T) {
	assert.Equal(t, RuntimeKind(runtimekind.Subprocess), RuntimeKindSubprocess)
	assert.Equal(t, RuntimeKind(runtimekind.PTY), RuntimeKindPTY)
	assert.Equal(t, RuntimeKind(runtimekind.StreamingStdio), RuntimeKindStreamingStdio)
	assert.Equal(t, RuntimeKind(runtimekind.JSONRPCStdio), RuntimeKindJsonRpcStdio)
	assert.Equal(t, RuntimeKind(runtimekind.ServeHTTP), RuntimeKindServeHTTP)

	// Every token agentkit's runtimekind.Parse recognises, its canonical
	// spellings, and folding variants of the ones Torque accepts.
	tokens := []string{
		"", "  ",
		"api", "provider-api", "http-api",
		"subprocess", "exec", "cli", "single-turn", "oneshot", "one-shot",
		"streaming-stdio", "stream-json", "streaming", "claude-code", "managed-streaming",
		"jsonrpc-stdio", "json-rpc-stdio", "jsonrpc", "app-server", "codex-app-server",
		"serve-http", "http", "sse", "opencode-serve",
		"pty", "tui", "terminal",
		"pty-debug", "debug-pty", "raw-pty",
		"unknown", "warp-drive",
		"SubProcess", " streaming_stdio ", "JSONRPC_STDIO", "Serve_HTTP", "PTY", "App_Server",
	}
	for _, tok := range tokens {
		assert.Equal(t, agentkitRuntimeKindFromConfigValue(tok), parseRuntimeKind(tok), "token %q", tok)
	}
}

package runtimetoken

import (
	"errors"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		raw  string
		want Token
	}{
		{"", Token{}},
		{"subprocess-per-turn", Token{Mode: runtimes.ModeSubprocessPerTurn}},
		{"streaming-stdio", Token{Mode: runtimes.ModeStreamingStdio}},
		{"jsonrpc-stdio", Token{Mode: runtimes.ModeJSONRPCStdio}},
		{"http-sse", Token{Mode: runtimes.ModeHTTPSSE}},
		{"pty", Token{Mode: runtimes.ModePTY}},
		{"acp-stdio", Token{Mode: runtimes.ModeACPStdio}},
		{"subprocess", Token{Mode: runtimes.ModeSubprocessPerTurn, Legacy: true}},
		{"cli", Token{Mode: runtimes.ModeSubprocessPerTurn, Legacy: true}},
		{"serve-http", Token{Mode: runtimes.ModeHTTPSSE, Legacy: true}},
		{"app-server", Token{Mode: runtimes.ModeJSONRPCStdio, Legacy: true}},
		{"pty-debug", Token{Mode: runtimes.ModePTY, Legacy: true, Debug: true}},
		{"  Serve_HTTP ", Token{Mode: runtimes.ModeHTTPSSE, Legacy: true}},
		{"JSONRPC_STDIO", Token{Mode: runtimes.ModeJSONRPCStdio}},
	}
	for _, tc := range cases {
		got, err := Normalize(tc.raw)
		require.NoError(t, err, tc.raw)
		assert.Equal(t, tc.want, got, tc.raw)
	}
}

func TestNormalize_UnknownFailsLoudly(t *testing.T) {
	for _, raw := range []string{"exec", "tui", "streaming", "serve"} {
		_, err := Normalize(raw)
		require.Error(t, err, raw)
		assert.True(t, errors.Is(err, ErrUnknown), raw)
		assert.Contains(t, err.Error(), "subprocess-per-turn", "the error lists what is accepted")
	}
}

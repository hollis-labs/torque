package runtimetoken

import (
	"errors"
	"testing"

	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
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
		{"pty_debug", Token{Mode: runtimes.ModePTY, Legacy: true, Debug: true}},
		{"acp-tcp", Token{Mode: runtimes.ModeACPTCP}},
		{"ACP_STDIO", Token{Mode: runtimes.ModeACPStdio}},
	}
	for _, tc := range cases {
		got, err := Normalize(tc.raw)
		require.NoError(t, err, tc.raw)
		assert.Equal(t, tc.want, got, tc.raw)
	}
}

func TestNormalize_UnknownFailsLoudly(t *testing.T) {
	// Internal whitespace is not normalized away.
	for _, raw := range []string{"exec", "tui", "streaming", "serve", "serve http", "subprocess per turn", "acp"} {
		_, err := Normalize(raw)
		require.Error(t, err, raw)
		assert.True(t, errors.Is(err, ErrUnknown), raw)
		assert.Contains(t, err.Error(), "subprocess-per-turn", "the error lists what is accepted")
	}
}

func TestNormalizeProfile(t *testing.T) {
	for raw, want := range map[string]Token{
		"":                    {},
		"subprocess-per-turn": {Mode: runtimes.ModeSubprocessPerTurn},
		"http-sse":            {Mode: runtimes.ModeHTTPSSE},
		"subprocess":          {Mode: runtimes.ModeSubprocessPerTurn, Legacy: true},
		"Serve_HTTP":          {Mode: runtimes.ModeHTTPSSE, Legacy: true},
	} {
		got, err := NormalizeProfile(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}
	// Older spellings that only stored session rows carry stay errors in a
	// profile, naming the spelling to use instead.
	for raw, use := range map[string]string{"cli": "subprocess-per-turn", "app-server": "jsonrpc-stdio", "pty-debug": "pty", "PTY_DEBUG": "pty"} {
		_, err := NormalizeProfile(raw)
		require.Error(t, err, raw)
		assert.True(t, errors.Is(err, ErrNotAProfileKind), raw)
		assert.Contains(t, err.Error(), `use "`+use+`"`, raw)
	}
	_, err := NormalizeProfile("tui")
	assert.True(t, errors.Is(err, ErrUnknown))
}

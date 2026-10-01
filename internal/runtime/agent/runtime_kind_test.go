package agent

import (
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseRuntimeKind pins the runtime-kind tokens Torque accepts from
// profiles.yaml and session rows (CW-20261001-0063). The five spellings,
// including the `subprocess` and `serve-http` that live profiles and
// stored sessions carry, resolve to their kinds after case, space and
// underscore folding. Everything else comes back folded and fails validate,
// including the aliases agentkit's runtimekind.Parse recognised: Torque never
// accepted those.
func TestParseRuntimeKind(t *testing.T) {
	cases := []struct {
		raw   string
		want  RuntimeKind
		valid bool
	}{
		{"subprocess", RuntimeKindSubprocess, true},
		{"pty", RuntimeKindPTY, true},
		{"streaming-stdio", RuntimeKindStreamingStdio, true},
		{"jsonrpc-stdio", RuntimeKindJsonRpcStdio, true},
		{"serve-http", RuntimeKindServeHTTP, true},

		{"SubProcess", RuntimeKindSubprocess, true},
		{" streaming_stdio ", RuntimeKindStreamingStdio, true},
		{"JSONRPC_STDIO", RuntimeKindJsonRpcStdio, true},
		{"Serve_HTTP", RuntimeKindServeHTTP, true},

		{"", "", true},
		{"   ", "", true},

		{"app-server", "app-server", false},
		{"codex_app_server", "codex-app-server", false},
		{"opencode-serve", "opencode-serve", false},
		{"stream-json", "stream-json", false},
		{"claude-code", "claude-code", false},
		{"cli", "cli", false},
		{"http", "http", false},
		{"tui", "tui", false},
		{"pty-debug", "pty-debug", false},
		{"api", "api", false},
		{"warp-drive", "warp-drive", false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got := parseRuntimeKind(tc.raw)
			assert.Equal(t, tc.want, got)
			if tc.valid {
				assert.NoError(t, got.validate())
			} else {
				assert.Error(t, got.validate())
			}
		})
	}
}

// TestSessionFromRecordParsesRuntimeKind: a stored session's runtime_kind
// comes back through parseRuntimeKind, so a spelling Torque wrote reads
// back as the same kind, and the read path is where a renamed kind will
// map old rows.
func TestSessionFromRecordParsesRuntimeKind(t *testing.T) {
	for stored, want := range map[string]RuntimeKind{
		"subprocess":      RuntimeKindSubprocess,
		"pty":             RuntimeKindPTY,
		"streaming-stdio": RuntimeKindStreamingStdio,
		"jsonrpc-stdio":   RuntimeKindJsonRpcStdio,
		"serve-http":      RuntimeKindServeHTTP,
		"Serve_HTTP":      RuntimeKindServeHTTP,
		"":                "",
	} {
		s := sessionFromRecord(&sqlstore.SessionRecord{ID: "SES-RK", RuntimeKind: stored, MetaJSON: "{}"})
		require.NotNil(t, s)
		assert.Equal(t, string(want), s.RuntimeKind, "stored %q", stored)
	}
}

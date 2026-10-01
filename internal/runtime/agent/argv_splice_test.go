package agent

import (
	"slices"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
)

func TestInsertBeforeEndOfOptions(t *testing.T) {
	in := []string{"exec", "--json", "--", "-x prompt"}
	got := insertBeforeEndOfOptions(in, "--model", "m")
	assert.Equal(t, []string{"exec", "--json", "--model", "m", "--", "-x prompt"}, got)
	assert.Equal(t, []string{"exec", "--json", "--", "-x prompt"}, in, "the input is not modified")

	assert.Equal(t, []string{"app-server", "-c", "x"}, insertBeforeEndOfOptions([]string{"app-server"}, "-c", "x"))
	// Only the first "--" is the marker; a later one is prompt text.
	assert.Equal(t, []string{"run", "--a", "--", "say -- this"}, insertBeforeEndOfOptions([]string{"run", "--", "say -- this"}, "--a"))
}

// composeBuildArgs (the legacy path's per-turn argv) put the generic
// --model suffix after go-providers v0.34.1's `-- <prompt>`, so codex exec
// got "--model <m>" as prompt text (CW-20261001-0064).
func TestComposeBuildArgs_FlagsStayBeforeEndOfOptions(t *testing.T) {
	oc := provider.NewOpencodeAdapter()
	oc.Agent = "worker"
	oc.Model = "oc-model"
	for _, tc := range []struct {
		name     string
		adapter  provider.CLIAdapter
		provider string
	}{
		{"codex exec", provider.NewCodexAdapter(), "codex"},
		{"opencode run", oc, "opencode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := config.AgentProfile{Provider: tc.provider, Model: "oc-model", Args: []string{"--profile-flag", "v"}}
			args := composeBuildArgs(buildArgsParams{
				Adapter: tc.adapter, Profile: profile, SystemPrompt: "SYS", TurnPrompt: "--dangerously-skip-permissions",
				SkipModelSuffix: skipModelSuffixForProvider(tc.provider),
			})
			end := slices.Index(args, "--")
			require.NotEqual(t, -1, end, "argv: %q", args)
			require.Equal(t, len(args)-2, end, "the prompt must be the only argument after --: %q", args)
			for _, flag := range []string{"--profile-flag", "--model"} {
				i := slices.Index(args, flag)
				require.NotEqual(t, -1, i, "argv lacks %q: %q", flag, args)
				assert.Less(t, i, end, "%q must come before --: %q", flag, args)
			}
		})
	}
}

// claudeProfileArgv is the wrapper path's splice. Claude streaming carries
// no prompt in argv, but a prepared command that ends `-- <prompt>` must
// still get --model before the marker.
func TestClaudeProfileArgv(t *testing.T) {
	profile := config.AgentProfile{Provider: "claude-code", Model: "m", Args: []string{"--max-turns", "3", "--dangerously-skip-permissions"}}
	assert.Equal(t,
		[]string{"claude", "--max-turns", "3", "-p", "--add-dir", "/p", "--model", "m", "--", "PROMPT"},
		claudeProfileArgv([]string{"claude", "-p", "--add-dir", "/p", "--", "PROMPT"}, profile))
	assert.Equal(t,
		[]string{"claude", "--max-turns", "3", "-p", "--input-format", "stream-json", "--model", "m"},
		claudeProfileArgv([]string{"claude", "-p", "--input-format", "stream-json"}, profile))
}

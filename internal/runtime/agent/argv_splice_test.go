package agent

import (
	"slices"
	"testing"

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
// got "--model <m>" as prompt text (CW-20261001-0064). The model is now an
// adapter field the convention places itself (CW-20261001-0094).
func TestComposeBuildArgs_FlagsStayBeforeEndOfOptions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		model    string
	}{
		{"codex exec", "codex", `model="oc-model"`},
		{"opencode run", "opencode", "oc-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := config.AgentProfile{Provider: tc.provider, Model: "oc-model", Args: []string{"--profile-flag", "v"}}
			adapter, _, err := adapterFor(profile, "worker", RuntimeKindSubprocess)
			require.NoError(t, err)
			args := composeBuildArgs(buildArgsParams{
				Adapter: adapter, Profile: profile, SystemPrompt: "SYS", TurnPrompt: "--dangerously-skip-permissions",
			})
			end := slices.Index(args, "--")
			require.NotEqual(t, -1, end, "argv: %q", args)
			require.Equal(t, len(args)-2, end, "the prompt must be the only argument after --: %q", args)
			for _, flag := range []string{"--profile-flag", tc.model} {
				i := slices.Index(args, flag)
				require.NotEqual(t, -1, i, "argv lacks %q: %q", flag, args)
				assert.Less(t, i, end, "%q must come before --: %q", flag, args)
			}
		})
	}
}

// torqueLaunchArgs is what Boot adds at the launch template's extra slot:
// the profile's args without the developer-mode flag (the Claude adapter
// emits that itself), and claude-code's planted settings file.
func TestTorqueLaunchArgs(t *testing.T) {
	claude := config.AgentProfile{Provider: "claude-code", Model: "m", Args: []string{"--max-turns", "3", "--dangerously-skip-permissions"}}
	assert.Equal(t,
		[]string{"--max-turns", "3", "--settings", "/boot/.claude/settings.json"},
		torqueLaunchArgs(claude, "/boot"))

	codex := config.AgentProfile{Provider: "codex", Model: "m", Args: []string{"--enable", "f"}}
	assert.Equal(t, []string{"--enable", "f"}, torqueLaunchArgs(codex, "/boot"))
	assert.Empty(t, torqueLaunchArgs(config.AgentProfile{Provider: "opencode", Model: "m"}, "/boot"))
}

func TestTrimArgvPrefix(t *testing.T) {
	prefix := []string{"app-server", "-c", `model="m"`}
	assert.Equal(t, []string{"--enable", "f"}, trimArgvPrefix([]string{"app-server", "-c", `model="m"`, "--enable", "f"}, prefix))
	assert.Empty(t, trimArgvPrefix(prefix, prefix))
	other := []string{"app-server", "--enable", "f"}
	assert.Equal(t, other, trimArgvPrefix(other, prefix), "argv without the prefix is unchanged")
	assert.Equal(t, other, trimArgvPrefix(other, nil))
}

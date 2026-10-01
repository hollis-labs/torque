package agent

import (
	"bytes"
	"log"
	"os"
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
		[]string{"--max-turns", "3", "--settings", "/boot/.claude/settings.json", "--strict-mcp-config"},
		torqueLaunchArgs(claude, "/boot"))

	codex := config.AgentProfile{Provider: "codex", Model: "m", Args: []string{"--enable", "f"}}
	assert.Equal(t, []string{"--enable", "f"}, torqueLaunchArgs(codex, "/boot"))
	assert.Empty(t, torqueLaunchArgs(config.AgentProfile{Provider: "opencode", Model: "m"}, "/boot"))
}

func TestTrimArgvPrefix(t *testing.T) {
	prefix := []string{"app-server", "-c", `model="m"`}
	rest, ok := trimArgvPrefix([]string{"app-server", "-c", `model="m"`, "--enable", "f"}, prefix)
	assert.True(t, ok)
	assert.Equal(t, []string{"--enable", "f"}, rest)
	rest, ok = trimArgvPrefix(prefix, prefix)
	assert.True(t, ok)
	assert.Empty(t, rest)
	other := []string{"app-server", "--enable", "f"}
	rest, ok = trimArgvPrefix(other, prefix)
	assert.False(t, ok, "an argv that does not open with the prefix is reported")
	assert.Equal(t, other, rest)
	_, ok = trimArgvPrefix(other, nil)
	assert.True(t, ok, "an empty prefix always matches")
}

// bootLegacy splices only what follows the adapter's own app-server command;
// a prepared argv that does not open with it fails the boot instead of
// running the command twice.
func TestCodexAppServerArgs(t *testing.T) {
	command := []string{"app-server", "-c", `model="m"`}
	args, err := codexAppServerArgs([]string{"app-server", "-c", `model="m"`, "--enable", "f"}, command)
	require.NoError(t, err)
	assert.Equal(t, []string{"--enable", "f"}, args)

	_, err = codexAppServerArgs([]string{"app-server", "--enable", "f"}, command)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `["app-server" "--enable" "f"]`)
	assert.Contains(t, err.Error(), `model=\"m\"`)
}

// Claude loads only the MCP servers Torque plants (CW-20261001-0226): every
// Claude launch gets --strict-mcp-config, whatever the provider's spelling,
// and no other runtime does. A profile that already passes the flag is not
// given it twice.
func TestTorqueLaunchArgs_StrictMCPConfig(t *testing.T) {
	for _, provider := range []string{"claude-code", "Claude-Code", "claude"} {
		assert.Contains(t, torqueLaunchArgs(config.AgentProfile{Provider: provider}, "/boot"), "--strict-mcp-config", provider)
	}
	for _, provider := range []string{"codex", "opencode", "copilot", "pi", "agy", ""} {
		assert.NotContains(t, torqueLaunchArgs(config.AgentProfile{Provider: provider}, "/boot"), "--strict-mcp-config", provider)
	}
	args := torqueLaunchArgs(config.AgentProfile{Provider: "claude-code", Args: []string{"--strict-mcp-config", "--effort", "high"}}, "/boot")
	assert.Equal(t, 1, countArgs(args, "--strict-mcp-config"), "argv: %q", args)
}

// TORQUE_CLAUDE_STRICT_MCP is the interim kill switch: only 0, false, off
// and no (any case, padded) turn strict mode off, a typo keeps it on, and
// turning it off is logged as a WARN naming the value.
func TestClaudeStrictMCP_KillSwitch(t *testing.T) {
	claude := config.AgentProfile{Provider: "claude-code"}
	for _, tc := range []struct {
		value  string
		set    bool
		strict bool
		warn   string
	}{
		{set: false, strict: true},
		{value: "", set: true, strict: true},
		{value: "1", set: true, strict: true},
		{value: "true", set: true, strict: true},
		{value: "0", set: true, strict: false, warn: `TORQUE_CLAUDE_STRICT_MCP="0"`},
		{value: "false", set: true, strict: false, warn: `"false"`},
		{value: " OFF ", set: true, strict: false, warn: `" OFF "`},
		{value: "No", set: true, strict: false, warn: `"No"`},
		{value: "disabled", set: true, strict: true, warn: `"disabled" is not recognized`},
		{value: "o", set: true, strict: true, warn: `"o" is not recognized`},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if tc.set {
				t.Setenv("TORQUE_CLAUDE_STRICT_MCP", tc.value)
			} else {
				require.NoError(t, os.Unsetenv("TORQUE_CLAUDE_STRICT_MCP"))
			}
			var logs bytes.Buffer
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })

			args := torqueLaunchArgs(claude, "/boot")
			assert.Equal(t, tc.strict, slices.Contains(args, "--strict-mcp-config"), "argv: %q", args)
			if tc.warn == "" {
				assert.NotContains(t, logs.String(), "WARN")
			} else {
				assert.Contains(t, logs.String(), "WARN")
				assert.Contains(t, logs.String(), tc.warn)
			}
		})
	}
	// The switch is about Claude only: other runtimes log nothing.
	t.Setenv("TORQUE_CLAUDE_STRICT_MCP", "0")
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	assert.Empty(t, torqueLaunchArgs(config.AgentProfile{Provider: "opencode"}, "/boot"))
	assert.Empty(t, logs.String())
}

func countArgs(args []string, want string) int {
	n := 0
	for _, a := range args {
		if a == want {
			n++
		}
	}
	return n
}

// With the kill switch off, a profile whose own args carry --strict-mcp-config
// keeps the flag, and the WARN says so rather than claiming it launches
// without it.
func TestClaudeStrictMCP_KillSwitchWithTheFlagInProfileArgs(t *testing.T) {
	t.Setenv("TORQUE_CLAUDE_STRICT_MCP", "0")
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	args := torqueLaunchArgs(config.AgentProfile{Provider: "claude-code", Args: []string{"--strict-mcp-config"}}, "/boot")
	assert.Equal(t, 1, countArgs(args, "--strict-mcp-config"), "the profile's own flag stays: %q", args)
	assert.Contains(t, logs.String(), "the profile's own args carry it")
	assert.NotContains(t, logs.String(), "WITHOUT")
}

package config

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A profile's mux_servers loads, in order, as written (CW-20261001-0226).
func TestLoadProfiles_MuxServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`agent_profiles:
  worker:
    executor: cli
    provider: claude-code
    mux_servers: [vanta, tesseract]
  plain:
    executor: cli
    provider: claude-code
`), 0o644))
	profiles, err := LoadProfiles(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"vanta", "tesseract"}, profiles["worker"].MuxServers)
	assert.Empty(t, profiles["plain"].MuxServers, "unset grants none")
	assert.Empty(t, profiles["worker"].DangerousMuxGrants())
}

// An unknown, empty or repeated name is a load-time error naming the profile
// and the entry, so a typo cannot silently plant nothing, or something else.
func TestLoadProfiles_MuxServersValidation(t *testing.T) {
	for _, tc := range []struct{ name, servers, want string }{
		{"unknown", `[vanta, vantaa]`, `mux_servers[1]: unknown mux server "vantaa"`},
		{"empty", `["tesseract", ""]`, `mux_servers[1] is empty`},
		{"repeated", `[torque, torque]`, `mux_servers[1]: "torque" is listed twice`},
		{"case", `[Cerberus]`, `unknown mux server "Cerberus"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profiles.yaml")
			require.NoError(t, os.WriteFile(path, []byte("agent_profiles:\n  worker:\n    executor: cli\n    provider: claude-code\n    mux_servers: "+tc.servers+"\n"), 0o644))
			_, err := LoadProfiles(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `agent_profiles["worker"]`)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// Naming a server that grants host command execution (cerberus: deploy and
// ssh; nanite: its dev_bash runs shell commands) loads but warns, saying why.
func TestLoadProfiles_MuxServersDangerousWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte("agent_profiles:\n  deployer:\n    executor: cli\n    provider: claude-code\n    mux_servers: [vanta, cerberus, nanite]\n  tidy:\n    executor: cli\n    provider: claude-code\n    mux_servers: [tesseract]\n"), 0o644))
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	profiles, err := LoadProfiles(path)
	require.NoError(t, err)
	assert.Equal(t, []DangerousMuxGrant{
		{Server: "cerberus", Grants: DangerousMuxServers["cerberus"]},
		{Server: "nanite", Grants: DangerousMuxServers["nanite"]},
	}, profiles["deployer"].DangerousMuxGrants())
	assert.Empty(t, profiles["tidy"].DangerousMuxGrants())
	assert.Contains(t, logs.String(), "WARNING")
	assert.Contains(t, logs.String(), `agent_profiles["deployer"] mux_servers names cerberus, which grants deploy and ssh`)
	assert.Contains(t, logs.String(), `agent_profiles["deployer"] mux_servers names nanite, which grants shell execution: its dev_bash tool`)
	assert.NotContains(t, logs.String(), `"tidy"`)
}

// The lint reports an unknown name as an error and cerberus as a warning,
// and a Codex or ACP profile is told the grant needs bypassPermissions.
func TestLintProfilesYAML_MuxServers(t *testing.T) {
	problems, err := LintProfilesYAML([]byte(`agent_profiles:
  worker:
    executor: cli
    provider: claude-code
    mux_servers: [vanta, vantaa]
  deployer:
    executor: cli
    provider: claude-code
    mux_servers: [torque, cerberus, nanite]
  worker-codex:
    executor: cli
    provider: codex
    mux_servers: [torque]
  worker-codex-bypass:
    executor: cli
    provider: codex
    permission_mode: bypassPermissions
    mux_servers: [torque]
  worker-copilot:
    executor: cli
    provider: copilot
    mux_servers: [torque]
  tidy:
    executor: cli
    provider: claude-code
    mux_servers: [tesseract, torque]
`))
	require.NoError(t, err)
	byPath := map[string][]ProfileLintProblem{}
	for _, p := range problems {
		byPath[p.Path] = append(byPath[p.Path], p)
	}
	require.Len(t, byPath["agent_profiles.worker.mux_servers"], 1, "%v", problems)
	assert.False(t, byPath["agent_profiles.worker.mux_servers"][0].Warning, "an unknown name is an error")
	assert.Contains(t, byPath["agent_profiles.worker.mux_servers"][0].Message, `unknown mux server "vantaa"`)

	require.Len(t, byPath["agent_profiles.deployer.mux_servers"], 2)
	for _, p := range byPath["agent_profiles.deployer.mux_servers"] {
		assert.True(t, p.Warning, "cerberus and nanite are warnings")
	}
	assert.Contains(t, byPath["agent_profiles.deployer.mux_servers"][0].Message, "cerberus grants deploy and ssh")
	assert.Contains(t, byPath["agent_profiles.deployer.mux_servers"][1].Message, "nanite grants shell execution: its dev_bash tool")

	for _, name := range []string{"worker-codex", "worker-copilot"} {
		got := byPath["agent_profiles."+name+".mux_servers"]
		require.Len(t, got, 1, name)
		assert.True(t, got[0].Warning, name)
		assert.Contains(t, got[0].Message, "only under permission_mode bypassPermissions", name)
	}
	assert.Empty(t, byPath["agent_profiles.worker-codex-bypass.mux_servers"], "bypassPermissions plants it")
	assert.Empty(t, byPath["agent_profiles.tidy.mux_servers"])
}

func TestLintProblem_StringMarksWarnings(t *testing.T) {
	assert.Equal(t, "line 4: p: warning: m", ProfileLintProblem{Line: 4, Path: "p", Message: "m", Warning: true}.String())
	assert.Equal(t, "line 4: p: m", ProfileLintProblem{Line: 4, Path: "p", Message: "m"}.String())
}

// A Claude profile on an ACP runtime kind is warned: native Claude is
// launched with --strict-mcp-config, but the ACP bridge takes no such flag,
// so Torque cannot confirm it loads only the servers it plants.
func TestLintProfilesYAML_ClaudeOverACPWarns(t *testing.T) {
	problems, err := LintProfilesYAML([]byte(`agent_profiles:
  worker-claude-code:
    executor: cli
    provider: claude-code
    runtime_kind: acp-stdio
  tidy-claude-code:
    executor: cli
    provider: claude-code
  worker-copilot:
    executor: cli
    provider: copilot
`))
	require.NoError(t, err)
	var warned []string
	for _, p := range problems {
		if strings.Contains(p.Message, "--strict-mcp-config") {
			assert.True(t, p.Warning)
			warned = append(warned, p.Path)
		}
	}
	assert.Equal(t, []string{"agent_profiles.worker-claude-code.runtime_kind"}, warned, "%v", problems)
}

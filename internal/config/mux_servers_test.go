package config

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
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
	assert.False(t, profiles["worker"].GrantsCerberus())
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

// Naming cerberus, which can deploy to and ssh into hosts, loads but warns.
func TestLoadProfiles_MuxServersCerberusWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte("agent_profiles:\n  deployer:\n    executor: cli\n    provider: claude-code\n    mux_servers: [vanta, cerberus]\n"), 0o644))
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	profiles, err := LoadProfiles(path)
	require.NoError(t, err)
	assert.True(t, profiles["deployer"].GrantsCerberus())
	assert.Contains(t, logs.String(), "WARNING")
	assert.Contains(t, logs.String(), `agent_profiles["deployer"] mux_servers names cerberus`)
	assert.Contains(t, logs.String(), "deploy to and ssh into hosts")
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
    mux_servers: [torque, cerberus]
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

	require.Len(t, byPath["agent_profiles.deployer.mux_servers"], 1)
	assert.True(t, byPath["agent_profiles.deployer.mux_servers"][0].Warning, "cerberus is a warning")
	assert.Contains(t, byPath["agent_profiles.deployer.mux_servers"][0].Message, "cerberus grants deploy and ssh")

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

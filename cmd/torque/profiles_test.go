package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunProfilesLint_ReportsProblems(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
agent_profiles:
  nanite-backend:
    executor: cli
    provider: opencode
`), 0o644))

	var out bytes.Buffer
	err := runProfilesLint(&out, path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "profiles lint failed: 1 problem")
	assert.Contains(t, out.String(), "dishonest profile name: missing provider binding")
}

func TestRunProfilesLint_OK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
agent_profiles:
  torque-backend-codex:
    executor: cli
    provider: codex
    model: gpt-5.4
`), 0o644))

	var out bytes.Buffer
	require.NoError(t, runProfilesLint(&out, path))
	assert.Contains(t, out.String(), "profiles OK:")
}

// A profile that grants cerberus gets a lint warning, which is printed and
// does not fail the lint; an unknown mux server is an error and does.
func TestRunProfilesLint_MuxServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
agent_profiles:
  deployer-claude-code:
    executor: cli
    provider: claude-code
    mux_servers: [torque, cerberus]
`), 0o644))
	var out bytes.Buffer
	require.NoError(t, runProfilesLint(&out, path), "a warning does not fail the lint")
	assert.Contains(t, out.String(), "warning: cerberus grants deploy and ssh")
	assert.Contains(t, out.String(), "profiles OK with 1 warning(s)")

	require.NoError(t, os.WriteFile(path, []byte(`
agent_profiles:
  deployer-claude-code:
    executor: cli
    provider: claude-code
    mux_servers: [torque, cerbrus]
`), 0o644))
	out.Reset()
	err := runProfilesLint(&out, path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "profiles lint failed: 1 problem")
	assert.Contains(t, out.String(), `unknown mux server "cerbrus"`)
}

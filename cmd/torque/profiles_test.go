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

package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathCmdReportsResolvedLayout(t *testing.T) {
	dir := t.TempDir()
	isolateTorquePaths(t, dir)

	var out bytes.Buffer
	cmd := pathCmd()
	cmd.SetOut(&out)
	require.NoError(t, cmd.Execute())

	got := out.String()
	assert.Contains(t, got, filepath.Join(dir, "xdg-data", "torque", "workspaces", "default", "main.db"))
	assert.Contains(t, got, filepath.Join(dir, "xdg-state", "torque", "queue.db"))
	assert.Contains(t, got, filepath.Join(dir, "xdg-config", "torque", "profiles.yaml"))
	assert.Contains(t, got, "workspace")
	assert.Contains(t, got, "default")
}

func TestPathCmdHonorsDBOverride(t *testing.T) {
	dir := t.TempDir()
	isolateTorquePaths(t, dir)
	override := filepath.Join(dir, "explicit.db")
	t.Setenv("TORQUE_DB_PATH", override)

	var out bytes.Buffer
	cmd := pathCmd()
	cmd.SetOut(&out)
	require.NoError(t, cmd.Execute())

	got := out.String()
	assert.Contains(t, got, "main-db")
	assert.Contains(t, got, override)
}

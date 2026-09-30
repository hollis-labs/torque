package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServeRefusesNonLoopbackWithoutToken runs the real serve command: the
// bind check must fire before anything listens or opens the database.
func TestServeRefusesNonLoopbackWithoutToken(t *testing.T) {
	dir := t.TempDir()
	isolateTorquePaths(t, dir)
	t.Setenv("TORQUE_API_TOKEN", "")

	for _, addr := range []string{"0.0.0.0:0", ":0"} {
		cmd := serveCmd()
		cmd.SetArgs([]string{"--addr", addr})
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		err := cmd.Execute()
		require.Error(t, err, addr)
		assert.Contains(t, err.Error(), "without a token", addr)
	}
}

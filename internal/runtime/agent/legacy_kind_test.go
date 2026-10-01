package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/testutil/testenv"
)

// bootLegacy's argv splice is right for codex app-server alone; production
// routes nothing else there, and the RuntimeFactory test seam (whose
// runtimes spawn nothing) may run any kind (CW-20261001-0080).
func TestLegacyRuntimeKindAllowed(t *testing.T) {
	for _, kind := range []RuntimeKind{RuntimeKindSubprocess, RuntimeKindStreamingStdio, RuntimeKindPTY, RuntimeKindServeHTTP} {
		assert.False(t, legacyRuntimeKindAllowed(kind, false), "%s must not spawn through bootLegacy", kind)
		assert.True(t, legacyRuntimeKindAllowed(kind, true), "%s under the test seam", kind)
	}
	assert.True(t, legacyRuntimeKindAllowed(RuntimeKindJsonRpcStdio, false))
}

// Outside the test seam bootLegacy refuses a kind it would launch with its
// command repeated, says why, and removes the boot dir planted for it.
func TestBootLegacy_RefusesOtherKindsOutsideTheSeam(t *testing.T) {
	bootDir := filepath.Join(t.TempDir(), "agentlaunch-bootdir-test")
	require.NoError(t, os.MkdirAll(bootDir, 0o700))
	_, err := bootLegacy(context.Background(), &Dependencies{WorkspacesRoot: testenv.WorkspacesRoot(t)}, nil, Options{}, &plantedBoot{
		runtimeKind:     RuntimeKindSubprocess,
		capturedBootDir: bootDir,
	})
	require.ErrorIs(t, err, ErrBootFailed)
	assert.Contains(t, err.Error(), `runtime kind "subprocess-per-turn" has no launch on this path`)
	assert.NoDirExists(t, bootDir, "the planted boot dir must not leak")
}

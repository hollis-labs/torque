package agent_boot

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// inlineLaunchCatalog returns a self-contained launch-profile catalog
// YAML (an inline GlobalCatalog) for the given provider — the standalone,
// Tether-not-required shape Options.LaunchProfileInline accepts.
func inlineLaunchCatalog(provider, runtimeKind string) []byte {
	return []byte(fmt.Sprintf(`version: "0.1.0"
projects:
  - id: demo-project
    repo_root: /tmp/demo-project
agents:
  - id: demo-agent
providers:
  - id: %s
    runtime_kind: %s
launches:
  - id: demo-launch
    project: demo-project
    agent: demo-agent
    provider: %s
    workspace:
      mode: persistent
`, provider, runtimeKind, provider))
}

// TestBoot_LaunchProfile_InlinePayload exercises requirement (a) at the
// Boot level: a task that opts into a launch profile (via an inline
// payload) boots successfully through the same Compile/Prepare/Plant
// flow. The fake runtime stands in for the real provider.
func TestBoot_LaunchProfile_InlinePayload(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:              "CW-TEST-LP-001",
		AgentProfile:        "torque-backend",
		Workdir:             t.TempDir(),
		Mode:                agent.ModeLongLived,
		LaunchProfileInline: inlineLaunchCatalog("claude", "subprocess"),
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "claude-code", sess.Provider,
		"session provider stays Torque's profile provider")

	// The workspace dir still flowed through — workspace ownership stays
	// with Torque even on the launch-profile path.
	require.NotNil(t, cd.Runtime.workspaceDir.Load(),
		"WorkspaceDir must be wired through on the launch-profile path")

	// Boot prompt planting stays with Torque: the kickoff payload follows
	// the same Boot @./boot.md convention as the non-launch-profile path.
	payload := cd.Runtime.firstTurnPayload.Load()
	require.NotNil(t, payload)
	assert.Contains(t, string(*payload), "Boot @./boot.md")
}

// TestBoot_NoLaunchProfile_DefaultPathUnchanged is the regression guard
// at the Boot level: a task with no launch-profile reference boots
// exactly as before — the default inline path.
func TestBoot_NoLaunchProfile_DefaultPathUnchanged(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-LP-DEFAULT-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
		// No LaunchProfile / LaunchProfileInline — default path.
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "claude-code", sess.Provider)
	require.NotNil(t, cd.Runtime.workspaceDir.Load())
}

// TestBoot_LaunchProfile_MalformedFailsCleanly covers requirement (c) at
// the Boot level: a malformed launch profile fails cleanly with an
// errors.Is-matchable error — Boot returns ErrBootFailed wrapping
// ErrLaunchProfile, not a panic.
func TestBoot_LaunchProfile_MalformedFailsCleanly(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("missing path", func(t *testing.T) {
		sess, err := cd.Manager.Boot(ctx, agent.Options{
			TaskID:        "CW-TEST-LP-BAD-001",
			AgentProfile:  "torque-backend",
			Workdir:       t.TempDir(),
			Mode:          agent.ModeLongLived,
			LaunchProfile: "/no/such/catalog.yaml",
		})
		require.Error(t, err)
		assert.Nil(t, sess)
		assert.True(t, errors.Is(err, agent.ErrBootFailed), "want ErrBootFailed, got %v", err)
		assert.True(t, errors.Is(err, agent.ErrLaunchProfile), "want ErrLaunchProfile, got %v", err)
	})

	t.Run("malformed inline payload", func(t *testing.T) {
		sess, err := cd.Manager.Boot(ctx, agent.Options{
			TaskID:              "CW-TEST-LP-BAD-002",
			AgentProfile:        "torque-backend",
			Workdir:             t.TempDir(),
			Mode:                agent.ModeLongLived,
			LaunchProfileInline: []byte("not: valid: yaml: ["),
		})
		require.Error(t, err)
		assert.Nil(t, sess)
		assert.True(t, errors.Is(err, agent.ErrLaunchProfile), "want ErrLaunchProfile, got %v", err)
	})
}

// TestBoot_LaunchProfile_OneShotResolvesPlan confirms the launch-profile
// path works for ModeOneShot too — the scheduler-dispatched executor
// lifecycle is the most common Torque task shape.
func TestBoot_LaunchProfile_OneShotResolvesPlan(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: false}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:              "CW-TEST-LP-OS-001",
		AgentProfile:        "torque-backend",
		Workdir:             t.TempDir(),
		Mode:                agent.ModeOneShot,
		OneShotPrompt:       "do the thing",
		LaunchProfileInline: inlineLaunchCatalog("claude", "subprocess"),
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, agent.StatusDone, sess.Status)
}

package agent_boot

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// A resume Torque claims reaches the agent CLI (CW-20261001-0174): on the
// production wrapper path, ResumeSession's stored provider session id is
// rendered into the CLI's resume argument by the launch template.

// claude-code (streaming-stdio) is spawned with --resume <id>; the fake
// replays go-providers' captured resumed stream.
func TestResumeSession_ClaudeCode_LaunchesWithResumeArg(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Replay("claude/stream_resume"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "claude-code", PermissionMode: "acceptEdits"}}

	const providerSessionID = "00000000-0000-4000-8000-000000000039"
	plantSessionForResume(t, cd.Store, "SES-RESUME-ARGV-CLAUDE", "claude-code", providerSessionID, t.TempDir(), "worker")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-ARGV-CLAUDE", agent.ResumeOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)
	got, ok := fake.Call(0).ArgAfter("--resume")
	require.True(t, ok, "claude must be launched with --resume: %v", fake.Call(0).Args)
	assert.Equal(t, providerSessionID, got)
}

// opencode run (subprocess-per-turn) is spawned with --session <id>.
func TestResumeSession_Opencode_LaunchesWithSessionArg(t *testing.T) {
	fake := providertest.New(t, runtimes.OpenCode, providertest.Replay("opencode/run_tool_use"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "opencode")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode", PermissionMode: "acceptEdits"}}

	const providerSessionID = "ses_resume_opencode_1"
	plantSessionForResume(t, cd.Store, "SES-RESUME-ARGV-OPENCODE", "opencode", providerSessionID, t.TempDir(), "worker")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.ResumeSession(ctx, "SES-RESUME-ARGV-OPENCODE", agent.ResumeOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond)
	got, ok := fake.Call(0).ArgAfter("--session")
	require.True(t, ok, "opencode run must be launched with --session: %v", fake.Call(0).Args)
	assert.Equal(t, providerSessionID, got)
}

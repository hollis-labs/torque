package agent_boot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// opencode run takes its first turn, boot.md's content, as one argument, and
// Linux refuses an execve argument over 128 KiB (MAX_ARG_STRLEN) with E2BIG,
// so a long task description kept the turn from launching at all. Past
// Torque's bound the turn points at the planted boot.md by absolute path,
// since opencode runs in the project dir, and the launch goes ahead
// (CW-20261001-0121).
func TestBoot_OpencodeRunLargeKickoffPointsAtBootMD(t *testing.T) {
	fake := providertest.New(t, runtimes.OpenCode, providertest.Replay("opencode/run_turn1"))
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, string(runtimes.OpenCode))
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode"}}
	description := strings.Repeat("Carry this requirement through. ", 5000)
	require.Greater(t, len(description), 128<<10)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-LARGE-KICKOFF", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived, Description: description})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	require.Eventually(t, func() bool { return len(fake.Calls()) >= 1 && fake.Call(0).Exited }, 10*time.Second, 20*time.Millisecond, "turn 1 never ran to completion")

	call := fake.Call(0)
	require.Zero(t, call.ExitCode)
	bootMD := filepath.Join(sess.BootDir, "boot.md")
	require.True(t, filepath.IsAbs(bootMD))
	require.Equal(t, []string{"--", "Boot @" + bootMD}, call.Args[len(call.Args)-2:])
	planted, err := os.ReadFile(bootMD)
	require.NoError(t, err)
	require.Contains(t, string(planted), description, "the briefing the pointer names carries the description")
}

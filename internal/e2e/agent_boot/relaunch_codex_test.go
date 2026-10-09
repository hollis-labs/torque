package agent_boot

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/harness/adapters/providertest"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// No codex path hands a stored thread id to a fresh CODEX_HOME: a cold
// re-launch of a codex exec session (subprocess-per-turn, which go-providers
// v0.41.0 resumes by thread) boots fresh from both ResumeSession and
// Manager.Resume. The thread lives in the original session's CODEX_HOME, which
// a cold boot's per-boot CODEX_HOME does not have, so `exec … resume <id>`
// would fail there. Torque does not wire codex exec resume (CW-20261001-0255).
func TestRelaunch_CodexExecNeverPassesAStoredThreadID(t *testing.T) {
	const threadID = "00000000-0000-4000-8000-0000000000aa"
	for name, relaunch := range map[string]func(*composedDeps, context.Context) error{
		"ResumeSession": func(cd *composedDeps, ctx context.Context) error {
			_, err := cd.Manager.ResumeSession(ctx, "SES-CODEX-EXEC", agent.ResumeOptions{})
			return err
		},
		"Manager.Resume": func(cd *composedDeps, ctx context.Context) error {
			_, err := cd.Manager.Resume(ctx, agent.ResumeRequest{SessionID: "SES-CODEX-EXEC"})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := providertest.New(t, runtimes.Codex, providertest.Replay("codex/exec_turn1"))
			fake.Install()
			cd := composeDeps(t, fakeRuntimeConfig{}, "codex")
			cd.Deps.RuntimeFactory = nil
			cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "codex", RuntimeKind: "subprocess-per-turn"}}
			plantResumeCheckpoint(t, cd.Store, "SES-CODEX-EXEC", "codex", "worker", threadID)
			require.NoError(t, cd.Store.UpdateSessionResumeHint("SES-CODEX-EXEC", []byte(threadID)))

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			require.NoError(t, relaunch(cd, ctx))

			var args []string
			require.Eventually(t, func() bool {
				calls := fake.Calls()
				if len(calls) == 0 || len(calls[0].Args) == 0 {
					return false
				}
				args = calls[0].Args
				return true
			}, 5*time.Second, 20*time.Millisecond, "codex exec is launched")
			assert.NotContains(t, args, "resume", "a cold boot does not resume a thread: %q", args)
			assert.NotContains(t, args, threadID, "the stored thread id is not passed: %q", args)
			assert.Len(t, fake.Calls(), 1)
		})
	}
}

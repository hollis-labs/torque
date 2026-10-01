package agent_boot

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// opencode serve-http gets no projected argv spliced after the serve
// command the runtime builds itself. agentkit v0.12.0's projection carries
// the full `serve --port 0 --hostname 127.0.0.1` command for http-sse;
// splicing it would repeat that (before v0.12.0 the splice was `--dir`,
// which `opencode serve` rejects). Both the new and the older runtime-kind
// spellings reach the same launch (CW-20261001-0064).
func TestBoot_OpencodeServeHTTPGetsNoPreparedArgs(t *testing.T) {
	for _, kind := range []string{"http-sse", "serve-http"} {
		t.Run(kind, func(t *testing.T) {
			cd := composeDeps(t, fakeRuntimeConfig{}, "opencode")
			cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode", RuntimeKind: kind}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-OPENCODE-SERVE", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
			require.Equal(t, string(agent.RuntimeKindServeHTTP), sess.RuntimeKind)
			start := cd.Runtime.lastStartOpts.Load()
			require.NotNil(t, start)
			require.Empty(t, start.ExtraArgs)
		})
	}
}

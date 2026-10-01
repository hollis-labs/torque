package agent_boot

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// On the legacy path (bootLegacy; composeDeps' fake runtime forces it)
// opencode serve-http gets no projected argv spliced after the serve command
// the runtime builds itself. agentkit v0.12.0's projection carries the full
// `serve --port 0 --hostname 127.0.0.1` command for http-sse; splicing it
// would repeat that (before v0.12.0 the splice was `--dir`, which `opencode
// serve` rejects). Both the new and the older runtime-kind spellings reach
// the same launch (CW-20261001-0064). The production wrapper path is
// TestBoot_OpencodeServeHTTPWrapperLaunchesServeCommand.
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

// On the production wrapper path the prepared argv is the whole command, so
// opencode serve-http must launch `opencode serve --port 0 --hostname
// 127.0.0.1`. bootWrapper used to trim it to the executable alone, which
// launched a bare `opencode` (CW-20261001-0064 review). The fake exits at
// once, so Boot fails waiting for a listen URL; only the argv is checked.
func TestBoot_OpencodeServeHTTPWrapperLaunchesServeCommand(t *testing.T) {
	fake := providertest.New(t, runtimes.OpenCode, providertest.Script(providertest.Exit(0)))
	fake.ExpectErrors()
	fake.Install()
	cd := composeDeps(t, fakeRuntimeConfig{}, "opencode")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "opencode", RuntimeKind: "http-sse"}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-TEST-OPENCODE-SERVE-WRAPPER", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived}); err == nil {
		t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	}
	require.Eventually(t, func() bool { return len(fake.Calls()) > 0 }, 5*time.Second, 20*time.Millisecond, "opencode was never launched")
	require.Equal(t, []string{"serve", "--port", "0", "--hostname", "127.0.0.1"}, fake.Call(0).Args)
}

package agent_boot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

type fixedLoopback struct{}

func (fixedLoopback) URL() string                    { return "http://127.0.0.1:1/mcp" }
func (fixedLoopback) Shutdown(context.Context) error { return nil }

// TestBootCodexPlantsMuxOnlyUnderBypass is CW-20261001-0110. Codex runs MCP
// tools marked readOnlyHint without asking, so the approval responder cannot
// gate mux's tools; a codex session gets the daemon's mux MCP server only
// under bypassPermissions. Every posture keeps the run's own loopback.
func TestBootCodexPlantsMuxOnlyUnderBypass(t *testing.T) {
	cases := []struct {
		permissionMode string
		wantMux        bool
		// muxServers is the profile's mux_servers (CW-20261001-0226): it
		// narrows the planted mux's servers, but never plants it outside
		// bypassPermissions.
		muxServers []string
		wantArgs   string
	}{
		{permissionMode: "", wantMux: false},
		{permissionMode: "default", wantMux: false},
		{permissionMode: "acceptEdits", wantMux: false},
		{permissionMode: "plan", wantMux: false},
		{permissionMode: "bypassPermissions", wantMux: true, wantArgs: `"--servers"`},
		{permissionMode: "acceptEdits", wantMux: false, muxServers: []string{"tesseract"}},
		{permissionMode: "bypassPermissions", wantMux: true, muxServers: []string{"tesseract"}, wantArgs: `"--only", "tesseract"`},
	}
	for _, tc := range cases {
		name := tc.permissionMode
		if name == "" {
			name = "unset"
		}
		if len(tc.muxServers) > 0 {
			name += "+mux_servers"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			executable, err := os.Executable()
			require.NoError(t, err)
			binary := filepath.Join(dir, "codex")
			quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
			require.NoError(t, os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nexec %s -test.run=TestCodexRPCProcessHelper -- \"$@\"\n", quoted)), 0o700))
			t.Setenv("CODEX_CLI_PATH", binary)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			cd := composeDeps(t, fakeRuntimeConfig{}, "codex")
			cd.Deps.RuntimeFactory = nil
			cd.Deps.Loopback = func(string, string) (agent.LoopbackHandle, error) { return fixedLoopback{}, nil }
			cd.Deps.MuxCommand = "/usr/local/bin/mux"
			cd.Deps.MuxArgs = []string{"mcp", "--proxy", "--servers", "vanta,torque,cerberus"}
			cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "codex", PermissionMode: tc.permissionMode, MuxServers: tc.muxServers}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			recordPath := filepath.Join(dir, "process.json")
			sess, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID: "CW-CODEX-MUX", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
				Env: map[string]string{"TORQUE_TEST_CODEX_HELPER": "1", "TORQUE_TEST_CODEX_RECORD": recordPath},
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

			raw, err := os.ReadFile(recordPath)
			require.NoError(t, err)
			var rec codexProcessRecord
			require.NoError(t, json.Unmarshal(raw, &rec))
			settings, err := os.ReadFile(filepath.Join(rec.Home, "config.toml"))
			require.NoError(t, err)
			toml := string(settings)
			assert.Contains(t, toml, "[mcp_servers.loopback]", "every codex posture keeps the run's own loopback")
			if tc.wantMux {
				assert.Contains(t, toml, "[mcp_servers.mux]")
				assert.Contains(t, toml, tc.wantArgs)
				if len(tc.muxServers) > 0 {
					assert.NotContains(t, toml, "cerberus", "only the named servers are planted")
				}
			} else {
				assert.NotContains(t, toml, "[mcp_servers.mux]", "mux must not be planted for permission_mode %q", tc.permissionMode)
			}
		})
	}
}

package agent_boot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Use the real wrapper and streaming runtime with a recording executable.
// RuntimeFactory substitutes the legacy path, so it cannot catch options
// discarded between PreparedExecution and the actual wrapper spawn.
func TestBootWrapperClaudeProfileReachesProcess(t *testing.T) {
	for _, mode := range []string{"acceptEdits", "plan", "bypassPermissions"} {
		t.Run(mode, func(t *testing.T) {
			binDir := t.TempDir()
			record := filepath.Join(binDir, "argv")
			binary := filepath.Join(binDir, "claude")
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TORQUE_TEST_ARGV\"\nwhile IFS= read -r line; do :; done\n"
			require.NoError(t, os.WriteFile(binary, []byte(script), 0700))
			t.Setenv("CLAUDE_CLI_PATH", binary)
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			cd := composeDeps(t, fakeRuntimeConfig{}, "claude-code")
			cd.Deps.RuntimeFactory = nil
			cd.Deps.Profiles = config.ProfileMap{"worker": {
				Executor: "cli", Provider: "claude-code", Model: "configured-model",
				PermissionMode: mode, Args: []string{"--effort", "high"},
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			sess, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID: "CW-WRAPPER-" + mode, AgentProfile: "worker",
				Workdir: t.TempDir(), Mode: agent.ModeLongLived,
				Env: map[string]string{"TORQUE_TEST_ARGV": record},
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
			var raw []byte
			require.Eventually(t, func() bool {
				raw, err = os.ReadFile(record)
				return err == nil && len(raw) > 0
			}, time.Second, 10*time.Millisecond)
			args := strings.Split(strings.TrimSpace(string(raw)), "\n")
			values := map[string]string{}
			for i, arg := range args {
				if strings.HasPrefix(arg, "--") && i+1 < len(args) {
					values[arg] = args[i+1]
				}
			}
			assert.Equal(t, "configured-model", values["--model"])
			assert.Equal(t, "high", values["--effort"])
			assert.Equal(t, "stream-json", values["--input-format"])
			assert.NotContains(t, args, "--dangerously-skip-permissions")
			require.NotEmpty(t, values["--settings"])
			raw, err = os.ReadFile(values["--settings"])
			require.NoError(t, err)
			var settings struct {
				Permissions struct {
					DefaultMode string `json:"defaultMode"`
				} `json:"permissions"`
			}
			require.NoError(t, json.Unmarshal(raw, &settings))
			assert.Equal(t, mode, settings.Permissions.DefaultMode)
		})
	}
}

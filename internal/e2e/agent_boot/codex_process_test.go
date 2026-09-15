package agent_boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/stretchr/testify/require"
)

type codexProcessRecord struct {
	Args      []string `json:"args"`
	Home      string   `json:"home"`
	Methods   []string `json:"methods"`
	ThreadCWD string   `json:"thread_cwd"`
	AuthReady bool     `json:"auth_ready"`
}

// Run the real JSON-RPC runtime against a controlled subprocess. Replacing
// RuntimeFactory hides the argv splice that caused duplicate app-server args.
func TestBootCodexProcessCommandAndKickoff(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	binary := filepath.Join(dir, "codex")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	require.NoError(t, os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nexec %s -test.run=TestCodexRPCProcessHelper -- \"$@\"\n", quoted)), 0700))
	t.Setenv("CODEX_CLI_PATH", binary)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cd := composeDeps(t, fakeRuntimeConfig{}, "codex")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "codex", Model: "configured-model", PermissionMode: "bypassPermissions", Args: []string{"--enable", "test_feature"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recordPath := filepath.Join(dir, "process.json")
	workdir := t.TempDir()
	sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-CODEX-PROCESS", AgentProfile: "worker", Workdir: workdir, Mode: agent.ModeLongLived, Env: map[string]string{"TORQUE_TEST_CODEX_HELPER": "1", "TORQUE_TEST_CODEX_RECORD": recordPath}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })
	raw, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	var rec codexProcessRecord
	require.NoError(t, json.Unmarshal(raw, &rec))
	require.Equal(t, []string{"app-server", "--enable", "test_feature", "-c", `model="configured-model"`}, rec.Args)
	require.Equal(t, []string{"initialize", "thread/start", "turn/start"}, rec.Methods)
	require.Equal(t, workdir, rec.ThreadCWD)
	require.NotEmpty(t, rec.Home)
	require.NotEqual(t, workdir, rec.Home)
	require.True(t, rec.AuthReady, "credentials must be available when the process starts")
	settings, err := os.ReadFile(filepath.Join(rec.Home, "config.toml"))
	require.NoError(t, err)
	require.Contains(t, string(settings), `approval_policy = "never"`)
	require.Contains(t, string(settings), `sandbox_mode = "danger-full-access"`)
}

func TestCodexRPCProcessHelper(t *testing.T) {
	if os.Getenv("TORQUE_TEST_CODEX_HELPER") != "1" {
		return
	}
	rec := codexProcessRecord{Home: os.Getenv("CODEX_HOME")}
	// Record only whether the synthetic fixture arrived, never its contents.
	if auth, err := os.ReadFile(filepath.Join(rec.Home, "auth.json")); err == nil {
		st, statErr := os.Stat(filepath.Join(rec.Home, "auth.json"))
		rec.AuthReady = string(auth) == `{"OPENAI_API_KEY":"synthetic-test-credential"}` && statErr == nil && st.Mode().Perm() == 0600
	}
	for i, arg := range os.Args {
		if arg == "--" {
			rec.Args = os.Args[i+1:]
			break
		}
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := decoder.Decode(&request); err != nil {
			os.Exit(0)
		}
		if len(request.ID) == 0 {
			continue
		}
		rec.Methods = append(rec.Methods, request.Method)
		result := map[string]any{}
		if request.Method == "thread/start" {
			rec.ThreadCWD, _ = request.Params["cwd"].(string)
			result["thread"] = map[string]string{"id": "thread-process"}
		}
		if request.Method == "turn/start" {
			result["turn"] = map[string]string{"id": "turn-process"}
		}
		raw, _ := json.Marshal(rec)
		if err := os.WriteFile(os.Getenv("TORQUE_TEST_CODEX_RECORD"), raw, 0600); err != nil {
			os.Exit(3)
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
			os.Exit(4)
		}
	}
}

func TestBootCodexMissingAuthFailsBeforeRuntimeAndCleansBootDir(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{RuntimeFactoryErr: errors.New("runtime must not be constructed")}, "codex")
	require.NoError(t, os.Remove(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")))
	bootRoot := agent.DefaultBuildDirRoot()
	before := dirEntrySet(t, bootRoot)
	sess, err := cd.Manager.Boot(context.Background(), agent.Options{TaskID: "CW-CODEX-NO-AUTH", AgentProfile: "torque-backend", Workdir: t.TempDir(), Mode: agent.ModeLongLived})
	require.Nil(t, sess)
	require.ErrorIs(t, err, agent.ErrBootFailed)
	require.Contains(t, err.Error(), "read Codex login cache")
	require.NotContains(t, err.Error(), "runtime must not be constructed")
	for name := range dirEntrySet(t, bootRoot) {
		require.True(t, before[name], "failed boot left directory %s", name)
	}
}

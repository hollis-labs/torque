package agent_boot

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/stretchr/testify/require"
)

// The codex app-server sends each JSON-RPC frame as one stdout line, and an
// item/completed for a command carries the command's whole output, so one
// noisy command can produce a line of several MiB. agentkit's
// jsonrpc_stdio_session reads stdout with a 1 MiB bufio.Scanner and ends
// its reader loop on a longer line without reporting scanner.Err(): the
// stream stops, the child blocks once the pipe fills, the session still
// reads as running, and a steering turn/start times out with no error that
// names the cause (CW-20260913-0001).

// TestCodexLargeFrameHelper is the codex app-server these tests run: it
// answers the kickoff, then sends one item/completed of
// TORQUE_TEST_CODEX_OUTPUT_BYTES of command output, an agent message and
// turn/completed, and keeps answering requests.
func TestCodexLargeFrameHelper(t *testing.T) {
	if os.Getenv("TORQUE_TEST_CODEX_LARGE_FRAME") != "1" {
		return
	}
	size, err := strconv.Atoi(os.Getenv("TORQUE_TEST_CODEX_OUTPUT_BYTES"))
	if err != nil {
		os.Exit(2)
	}
	var mu sync.Mutex
	out := bufio.NewWriter(os.Stdout)
	write := func(v any) {
		raw, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(append(raw, '\n'))
		_ = out.Flush()
	}
	decoder := json.NewDecoder(os.Stdin)
	turns := 0
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := decoder.Decode(&request); err != nil {
			os.Exit(0)
		}
		if len(request.ID) == 0 {
			continue
		}
		result := map[string]any{}
		switch request.Method {
		case "thread/start":
			result["thread"] = map[string]string{"id": "thread-large"}
		case "turn/start":
			turns++
			result["turn"] = map[string]string{"id": fmt.Sprintf("turn-%d", turns)}
		}
		write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		if request.Method == "turn/start" && turns == 1 {
			go func() {
				write(map[string]any{"jsonrpc": "2.0", "method": "item/completed", "params": map[string]any{
					"threadId": "thread-large", "turnId": "turn-1",
					"item": map[string]any{"type": "commandExecution", "id": "cmd-1", "command": "cat huge.log",
						"aggregatedOutput": strings.Repeat("x", size), "exitCode": 0, "status": "completed"},
				}})
				write(map[string]any{"jsonrpc": "2.0", "method": "item/completed", "params": map[string]any{
					"threadId": "thread-large", "turnId": "turn-1",
					"item": map[string]any{"type": "agentMessage", "id": "msg-1", "text": "after the large output"},
				}})
				write(map[string]any{"jsonrpc": "2.0", "method": "turn/completed", "params": map[string]any{
					"threadId": "thread-large", "turn": map[string]any{"id": "turn-1", "status": "completed", "items": []any{}},
				}})
			}()
		}
	}
}

// bootCodexLargeFrame boots a long-lived codex session against
// TestCodexLargeFrameHelper, gives the large frame time to be read, then
// sends a steering turn and returns its error, with the path of the
// session's stream.jsonl.
func bootCodexLargeFrame(t *testing.T, outputBytes int) (streamLog string, err error) {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	binary := filepath.Join(dir, "codex")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	require.NoError(t, os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nexec %s -test.run=TestCodexLargeFrameHelper -- \"$@\"\n", quoted)), 0700))
	t.Setenv("CODEX_CLI_PATH", binary)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cd := composeDeps(t, fakeRuntimeConfig{}, "codex")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "codex", PermissionMode: "bypassPermissions"}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{TaskID: "CW-CODEX-LARGE-FRAME", AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived, Env: map[string]string{
		"TORQUE_TEST_CODEX_LARGE_FRAME": "1", "TORQUE_TEST_CODEX_OUTPUT_BYTES": strconv.Itoa(outputBytes),
	}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	time.Sleep(500 * time.Millisecond)
	steerCtx, steerCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer steerCancel()
	return filepath.Join(sess.WorkspaceDir, "logs", "stream.jsonl"), cd.Manager.SendTurn(steerCtx, sess, "steering after the large output")
}

// A frame just under agentkit's 1 MiB line limit is read, and steering
// afterwards is answered.
func TestBootCodexLargeFrameUnderLineLimit(t *testing.T) {
	_, err := bootCodexLargeFrame(t, 900*1024)
	require.NoError(t, err, "steering after a 900 KiB command output must be delivered")
}

// A frame over the old 1 MiB line limit must not kill the control channel.
// Since agentkit v0.14.1 the reader routes lines up to 64 MiB whole
// (CW-20261001-0086, CW-20260913-0001): the 1.5 MiB command item itself
// reaches stream.jsonl as its Bash tool call, the message after it follows,
// and steering is answered. Before, the reader stopped silently and steering
// timed out while the session still read as running. A reader that skipped
// the long line would pass the steering check but not the tool call's.
func TestBootCodexOversizedFrameKeepsSteeringUsable(t *testing.T) {
	streamLog, err := bootCodexLargeFrame(t, 3*1024*1024/2)
	require.NotErrorIs(t, err, context.DeadlineExceeded,
		"a 1.5 MiB command output left steering to time out instead of reporting the reader failure: %v", err)
	require.NoError(t, err, "steering after a 1.5 MiB command output must be delivered")
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(streamLog)
		return err == nil && strings.Contains(string(raw), "cat huge.log") && strings.Contains(string(raw), "after the large output")
	}, 5*time.Second, 20*time.Millisecond, "the 1.5 MiB command item and the message after it must reach %s", streamLog)
}

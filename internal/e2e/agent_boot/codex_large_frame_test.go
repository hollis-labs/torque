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
// sends a steering turn and returns its error.
func bootCodexLargeFrame(t *testing.T, outputBytes int) error {
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
	return cd.Manager.SendTurn(steerCtx, sess, "steering after the large output")
}

// A frame just under agentkit's 1 MiB line limit is read, and steering
// afterwards is answered.
func TestBootCodexLargeFrameUnderLineLimit(t *testing.T) {
	require.NoError(t, bootCodexLargeFrame(t, 900*1024),
		"steering after a 900 KiB command output must be delivered")
}

// A frame over the limit must not silently kill the control channel:
// either the reader keeps framing and steering is answered, or the session
// fails with an error that names the reader — never a bare timeout while
// the session still reads as running.
func TestBootCodexOversizedFrameKeepsSteeringUsable(t *testing.T) {
	t.Skip("CW-20260913-0001: agentkit jsonrpc_stdio_session runReaderLoop ends silently on a stdout line over 1 MiB; unskip once the agentkit fix is pinned")
	err := bootCodexLargeFrame(t, 3*1024*1024/2)
	if err != nil {
		require.NotErrorIs(t, err, context.DeadlineExceeded,
			"a 1.5 MiB command output left steering to time out instead of reporting the reader failure: %v", err)
	}
}

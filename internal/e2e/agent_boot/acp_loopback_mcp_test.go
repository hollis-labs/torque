package agent_boot

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	httptransport "github.com/hollis-labs/libs/plugin-mcp/go-mcp/transport/http"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/service"
)

// The go-providers ACP fakes replay transcripts and cannot open an HTTP
// connection, so the agent here is this test binary: a minimal ACP agent
// that advertises mcpCapabilities.http, takes the `loopback` server from
// session/new, and on its turn calls torque_comment_add on it.

const acpLoopbackComment = "comment from the ACP worker"

// TestACPAgentHelper is that agent. It records session/new's mcpServers in
// TORQUE_TEST_ACP_RECORD.
func TestACPAgentHelper(t *testing.T) {
	if os.Getenv("TORQUE_TEST_ACP_AGENT") != "1" {
		return
	}
	// TORQUE_TEST_PROTECT_PROBE=1: at start, try to write into the protected
	// directory and the working directory, and record both outcomes (the ACP
	// counterpart of TestProtectProbeHelper, CW-20261001-0162).
	if os.Getenv("TORQUE_TEST_PROTECT_PROBE") == "1" {
		attempt := func(path string) string {
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				return err.Error()
			}
			return "wrote"
		}
		rec := protectProbeRecord{
			ProtectWrite: attempt(filepath.Join(os.Getenv("TORQUE_TEST_PROTECT_DIR"), "agent-wrote")),
			CwdWrite:     attempt("agent-cwd-write"),
		}
		raw, _ := json.Marshal(rec)
		_ = os.WriteFile(os.Getenv("TORQUE_TEST_PROTECT_RECORD"), raw, 0o600)
	}
	var mu sync.Mutex
	enc := json.NewEncoder(os.Stdout)
	write := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(v)
	}
	dec := json.NewDecoder(os.Stdin)
	loopbackURL := ""
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			os.Exit(0)
		}
		if len(req.ID) == 0 {
			continue
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion":   1,
				"agentCapabilities": map[string]any{"loadSession": false, "mcpCapabilities": map[string]any{"http": true, "sse": false}},
				"authMethods":       []any{},
			}
		case "session/new":
			var p struct {
				MCPServers []map[string]any `json:"mcpServers"`
			}
			_ = json.Unmarshal(req.Params, &p)
			for _, s := range p.MCPServers {
				if s["name"] == "loopback" {
					loopbackURL, _ = s["url"].(string)
				}
			}
			raw, _ := json.Marshal(p.MCPServers)
			_ = os.WriteFile(os.Getenv("TORQUE_TEST_ACP_RECORD"), raw, 0o600)
			result = map[string]any{"sessionId": "acp-helper"}
		case "session/prompt":
			text := callLoopbackComment(loopbackURL)
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": "acp-helper",
				"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}},
			}})
			result = map[string]any{"stopReason": "end_turn"}
		}
		write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}

func callLoopbackComment(url string) string {
	if url == "" {
		return "no loopback in session/new"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "acp-helper", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		return "connect: " + err.Error()
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "torque_comment_add",
		Arguments: map[string]any{"content": acpLoopbackComment, "author": "acp-helper"},
	})
	if err != nil {
		return "call: " + err.Error()
	}
	if res.IsError {
		return "tool error"
	}
	return "commented"
}

// testLoopback serves the worker loopback Torque's daemon builds
// (bootstrap's loopbackBuilder): mcpadapter.NewLoopback over streamable
// HTTP on 127.0.0.1.
type testLoopback struct {
	url string
	srv *http.Server
}

func (l *testLoopback) URL() string                        { return l.url }
func (l *testLoopback) Shutdown(ctx context.Context) error { return l.srv.Shutdown(ctx) }

func serveWorkerLoopback(t *testing.T, store *sqlstore.Store, taskID string) *testLoopback {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	loopback := mcpadapter.NewLoopback(service.New(store), taskID)
	srv := &http.Server{Handler: httptransport.NewHandler(loopback.Server(), httptransport.HandlerOptions{}), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &testLoopback{url: fmt.Sprintf("http://%s/mcp", ln.Addr()), srv: srv}
}

// TestBootCopilotACP_WorkerCallsLoopbackMCP is CW-20261001-0097's acceptance,
// completed by go-agent-wrapper v0.19.0 (CW-20261001-0120): a long-lived
// Copilot task run boots through agent.Boot, session/new hands the agent
// Torque's loopback (and, at this default posture, not mux), and the
// worker's tool call on the loopback lands a comment on its task.
func TestBootCopilotACP_WorkerCallsLoopbackMCP(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	binary := filepath.Join(dir, "copilot")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	require.NoError(t, os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nexec %s -test.run=TestACPAgentHelper -- \"$@\"\n", quoted)), 0o700))
	t.Setenv("COPILOT_CLI_PATH", binary)
	// The ACP client resolves `copilot` on PATH; TestMain's refusing shim
	// would otherwise come first.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	record := filepath.Join(dir, "mcp-servers.json")

	const taskID = "CW-ACP-LOOPBACK"
	cd := composeDeps(t, fakeRuntimeConfig{}, "copilot")
	cd.Deps.RuntimeFactory = nil
	cd.Deps.Profiles = config.ProfileMap{"worker": {Executor: "cli", Provider: "copilot"}}
	cd.Deps.MuxCommand = "/usr/local/bin/mux"
	cd.Deps.MuxArgs = []string{"mcp"}
	require.NoError(t, cd.Store.CreateTask(&sqlstore.TaskRecord{ID: taskID, Title: "ACP loopback", Priority: 2}))
	var loopbackURL string
	cd.Deps.Loopback = func(id, _ string) (agent.LoopbackHandle, error) {
		l := serveWorkerLoopback(t, cd.Store, id)
		loopbackURL = l.URL()
		return l, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID: taskID, RunID: 61, AgentProfile: "worker", Workdir: t.TempDir(), Mode: agent.ModeLongLived,
		Env: map[string]string{"TORQUE_TEST_ACP_AGENT": "1", "TORQUE_TEST_ACP_RECORD": record},
	})
	require.NoError(t, err, "a Copilot long-lived task run launches: Copilot takes HTTP MCP servers")
	t.Cleanup(func() { _ = cd.Manager.Stop(context.Background(), sess.ID) })

	require.Eventually(t, func() bool {
		comments, err := cd.Store.ListComments(taskID)
		if err != nil {
			return false
		}
		for _, c := range comments {
			if c.Content == acpLoopbackComment && c.Author == "acp-helper" {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "the worker's torque_comment_add on the loopback must land on its task")

	raw, err := os.ReadFile(record)
	require.NoError(t, err)
	var servers []map[string]any
	require.NoError(t, json.Unmarshal(raw, &servers))
	assert.Equal(t, []map[string]any{
		{"type": "http", "name": "loopback", "url": loopbackURL, "headers": []any{}},
	}, servers, "session/new carries the loopback under the name native boot dirs plant, and no mux outside bypassPermissions")
}

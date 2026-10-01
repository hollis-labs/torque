package mcpbridge_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/mcpbridge"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/hollis-labs/torque/internal/testutil/testenv"
)

// daemon is a test Torque HTTP server with /mcp mounted the way serve
// mounts it, behind the API's auth.
type daemon struct {
	svc      *service.Service
	sessions *agent.Manager
	srv      *httptest.Server
}

func newDaemon(t *testing.T, sec httpserver.Security, wrap func(http.Handler) http.Handler) *daemon {
	t.Helper()
	store := sqlitetest.OpenStore(t)
	svc := service.New(store)
	sessions := agent.NewManager(&agent.Dependencies{Store: store, WorkspacesRoot: testenv.WorkspacesRoot(t)})
	var h http.Handler = httpserver.New(svc, nil).
		WithSecurity(sec).
		WithMCP(bootstrap.DaemonMCPHandler(svc, nil, sessions, nil, nil, nil))
	if wrap != nil {
		h = wrap(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &daemon{svc: svc, sessions: sessions, srv: srv}
}

// bridgeClient starts mcpbridge.Run on a pipe pair and returns an MCP
// client session on the other end, as mux would hold one.
func bridgeClient(t *testing.T, opts mcpbridge.Options) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	toBridge, fromClient := io.Pipe()
	toClient, fromBridge := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- mcpbridge.Run(ctx, &mcp.IOTransport{Reader: toBridge, Writer: fromBridge}, opts)
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "bridge-test", Version: "1"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: toClient, Writer: fromClient}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = cs.Close()
		_ = fromClient.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the bridge did not stop")
		}
	})
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	var names []string
	for tool, err := range cs.Tools(context.Background(), nil) {
		require.NoError(t, err)
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func callJSON(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "%s: %v", name, res.Content)
	require.NotEmpty(t, res.Content)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "%s: content %T", name, res.Content[0])
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text.Text), &out), text.Text)
	return out
}

// /mcp serves exactly the tools stdio `torque mcp` does.
func TestDaemonMCPListsTheStdioTools(t *testing.T) {
	d := newDaemon(t, httpserver.Security{}, nil)
	remote := toolNames(t, bridgeClient(t, mcpbridge.Options{Endpoint: d.srv.URL + "/mcp"}))

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := bootstrap.StdioMCPAdapter(d.svc, d.sessions, nil).Server().SDKServer().Connect(ctx, serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-shape", Version: "1"}, nil).Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	local := toolNames(t, cs)

	require.NotEmpty(t, local)
	assert.Equal(t, local, remote)
	assert.Contains(t, remote, "torque_task_create")
}

// Through the bridge a client creates and reads back a task in the
// daemon's store.
func TestBridgeRoundTripsATask(t *testing.T) {
	d := newDaemon(t, httpserver.Security{}, nil)
	cs := bridgeClient(t, mcpbridge.Options{Endpoint: d.srv.URL + "/mcp"})

	created := callJSON(t, cs, "torque_task_create", map[string]any{"title": "made over --remote", "project_id": ""})
	data, _ := created["data"].(map[string]any)
	id, _ := data["ID"].(string)
	require.NotEmpty(t, id, "created: %v", created)

	got := callJSON(t, cs, "torque_task_get", map[string]any{"id": id})
	gotData, _ := got["data"].(map[string]any)
	assert.Equal(t, "made over --remote", gotData["Title"])

	rec, err := d.svc.Store().GetTask(id)
	require.NoError(t, err, "the task is in the daemon's store")
	assert.Equal(t, "made over --remote", rec.Title)
}

// /mcp answers only who /api/v1 answers: a loopback Host without a token,
// the bearer once a token is set.
func TestDaemonMCPKeepsTheAPIAuth(t *testing.T) {
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	post := func(url, host, auth string) int {
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(initialize))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if host != "" {
			req.Host = host
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	open := newDaemon(t, httpserver.Security{}, nil)
	assert.Equal(t, http.StatusOK, post(open.srv.URL+"/mcp", "", ""))
	assert.Equal(t, http.StatusForbidden, post(open.srv.URL+"/mcp", "torque.example.com", ""), "no token: loopback hosts only, as /api/v1")

	locked := newDaemon(t, httpserver.Security{Token: "s3cret-token"}, nil)
	assert.Equal(t, http.StatusUnauthorized, post(locked.srv.URL+"/mcp", "", ""))
	assert.Equal(t, http.StatusOK, post(locked.srv.URL+"/mcp", "", "Bearer s3cret-token"))

	cs := bridgeClient(t, mcpbridge.Options{Endpoint: locked.srv.URL + "/mcp", Token: "s3cret-token"})
	assert.Contains(t, toolNames(t, cs), "torque_task_get", "the bridge sends the token")
}

// A call made while the daemon is down fails with a JSON-RPC error and the
// bridge stays up: once the daemon is back, calls succeed again.
func TestBridgeSurvivesTheDaemonGoingAway(t *testing.T) {
	var down atomic.Bool
	d := newDaemon(t, httpserver.Security{}, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if down.Load() {
				http.Error(w, "daemon restarting", http.StatusBadGateway)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	log := &syncWriter{}
	cs := bridgeClient(t, mcpbridge.Options{Endpoint: d.srv.URL + "/mcp", Log: log})

	down.Store(true)
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "torque_task_list", Arguments: map[string]any{}})
	require.Error(t, err, "a call while the daemon is down fails")
	assert.Contains(t, log.String(), "unreachable", "the bridge says why on stderr")

	down.Store(false)
	callJSON(t, cs, "torque_task_list", map[string]any{})
}

type syncWriter struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

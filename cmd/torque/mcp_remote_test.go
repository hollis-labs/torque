package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/hollis-labs/torque/internal/testutil/testenv"
)

func TestRemoteMCPEndpoint(t *testing.T) {
	for _, tc := range []struct{ flag, env, want string }{
		{"", "", ""},
		{"", "0", ""},
		{"", "false", ""},
		{remoteDefault, "", "http://127.0.0.1:8990/mcp"},
		{"", "1", "http://127.0.0.1:8990/mcp"},
		{"", "true", "http://127.0.0.1:8990/mcp"},
		{"", "http://127.0.0.1:9000/mcp", "http://127.0.0.1:9000/mcp"},
		{"http://127.0.0.1:9100/mcp", "1", "http://127.0.0.1:9100/mcp"},
	} {
		assert.Equal(t, tc.want, remoteMCPEndpoint(tc.flag, tc.env, 8990), "flag %q env %q", tc.flag, tc.env)
	}
}

// `torque mcp --remote` serves through the daemon while every directory
// Torque would keep state in is unreadable: it opens no database, creates
// nothing, and runs no orphan sweep, as an agent under ProtectedPaths needs
// (CW-20261001-0199).
func TestMCPRemoteNeverTouchesTheDatabase(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory")
	}
	store := sqlitetest.OpenStore(t)
	svc := service.New(store)
	sessions := agent.NewManager(&agent.Dependencies{Store: store, WorkspacesRoot: testenv.WorkspacesRoot(t)})
	daemon := httptest.NewServer(httpserver.New(svc, nil).WithMCP(bootstrap.DaemonMCPHandler(svc, nil, sessions, nil)))
	t.Cleanup(daemon.Close)

	locked := filepath.Join(t.TempDir(), "locked")
	require.NoError(t, os.Mkdir(locked, 0o700))
	for _, k := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(locked, k))
	}
	t.Setenv("TORQUE_DB_PATH", filepath.Join(locked, "main.db"))
	t.Setenv("TORQUE_MCP_REMOTE", "")
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	toCmd, fromClient := io.Pipe()
	toClient, fromCmd := io.Pipe()
	cmd := mcpCmd()
	cmd.SetIn(toCmd)
	cmd.SetOut(fromCmd)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--remote=" + daemon.URL + "/mcp"})
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "remote-test", Version: "1"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: toClient, Writer: fromClient}, nil)
	require.NoError(t, err)
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "torque_task_create", Arguments: map[string]any{"title": "via torque mcp --remote"}})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", res.Content)
	var created struct {
		Data struct{ ID string } `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &created))
	rec, err := store.GetTask(created.Data.ID)
	require.NoError(t, err, "the task landed in the daemon's store")
	assert.Equal(t, "via torque mcp --remote", rec.Title)

	_ = cs.Close()
	_ = fromClient.Close()
	select {
	case err := <-done:
		require.NoError(t, err, "stdin closing ends --remote cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("torque mcp --remote did not exit when stdin closed")
	}

	require.NoError(t, os.Chmod(locked, 0o700))
	entries, err := os.ReadDir(locked)
	require.NoError(t, err)
	assert.Empty(t, entries, "--remote must create nothing where Torque keeps its state")
}

// --remote's value is optional, so `--remote URL` is --remote plus a
// positional URL. That used to be ignored: the bridge dialed the default
// daemon, possibly the wrong one. It is refused now, naming the right form,
// and prints nothing on stdout (the protocol stream) or the URL's
// credentials anywhere.
func TestMCPRemoteURLNeedsAnEqualsSign(t *testing.T) {
	cmd := mcpCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--remote", "http://alice:hunter2@127.0.0.1:9/mcp"})
	err := cmd.ExecuteContext(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--remote=URL")
	assert.Empty(t, out.String(), "stdout carries the MCP protocol")
	for _, text := range []string{err.Error(), errOut.String()} {
		assert.NotContains(t, text, "hunter2")
		assert.NotContains(t, text, "alice")
	}
}

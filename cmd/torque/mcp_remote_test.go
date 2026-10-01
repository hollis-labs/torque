package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	const def = "http://127.0.0.1:8990/mcp"
	for _, tc := range []struct {
		name    string
		flagSet bool
		flag    string
		envSet  bool
		env     string
		want    string
	}{
		{name: "neither is local"},
		{name: "--remote alone", flagSet: true, flag: remoteDefault, want: def},
		{name: "--remote=URL", flagSet: true, flag: "http://127.0.0.1:9100/mcp", want: "http://127.0.0.1:9100/mcp"},
		{name: "env 1", envSet: true, env: "1", want: def},
		{name: "env true", envSet: true, env: "true", want: def},
		{name: "env URL", envSet: true, env: "http://127.0.0.1:9000/mcp", want: "http://127.0.0.1:9000/mcp"},
		{name: "env 0 asks for local", envSet: true, env: "0"},
		{name: "env false asks for local", envSet: true, env: "false"},
		{name: "--remote=0 asks for local", flagSet: true, flag: "0"},
		{name: "the flag wins over the env", flagSet: true, flag: "http://127.0.0.1:9100/mcp", envSet: true, env: "1", want: "http://127.0.0.1:9100/mcp"},
		{name: "--remote=0 wins over the env", flagSet: true, flag: "0", envSet: true, env: "1"},
	} {
		got, err := remoteMCPEndpoint(tc.flagSet, tc.flag, tc.envSet, tc.env, 8990)
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
	}
	// Given but empty is an error, never local.
	for _, tc := range []struct {
		name         string
		flagSet      bool
		flag         string
		envSet       bool
		env          string
		wantInErrMsg string
	}{
		{name: "--remote=", flagSet: true, flag: "", wantInErrMsg: "--remote was given an empty value"},
		{name: "--remote=  ", flagSet: true, flag: "  ", wantInErrMsg: "--remote was given an empty value"},
		{name: "empty env", envSet: true, env: "", wantInErrMsg: "TORQUE_MCP_REMOTE is set but empty"},
		{name: "empty flag with a good env", flagSet: true, flag: "", envSet: true, env: "1", wantInErrMsg: "--remote was given an empty value"},
	} {
		got, err := remoteMCPEndpoint(tc.flagSet, tc.flag, tc.envSet, tc.env, 8990)
		require.Error(t, err, tc.name)
		assert.Contains(t, err.Error(), tc.wantInErrMsg, tc.name)
		assert.Empty(t, got, tc.name)
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

// lockedState points every directory Torque would keep state in at a
// mode-000 directory, so anything that tries to open a database or create a
// directory fails, and returns a check that nothing was created in it.
func lockedState(t *testing.T) (assertUntouched func()) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory")
	}
	locked := filepath.Join(t.TempDir(), "locked")
	require.NoError(t, os.Mkdir(locked, 0o700))
	for _, k := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(locked, k))
	}
	t.Setenv("TORQUE_DB_PATH", filepath.Join(locked, "main.db"))
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	return func() {
		t.Helper()
		require.NoError(t, os.Chmod(locked, 0o700))
		entries, err := os.ReadDir(locked)
		require.NoError(t, err)
		assert.Empty(t, entries, "nothing may be created where Torque keeps its state")
		require.NoError(t, os.Chmod(locked, 0))
	}
}

// run executes `torque mcp` with args and returns its error and stdout.
func runMCP(t *testing.T, args ...string) (error, string, string) {
	t.Helper()
	cmd := mcpCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background()), out.String(), errOut.String()
}

// An empty --remote value (a launcher passing --remote=$UNSET) or an empty
// TORQUE_MCP_REMOTE used to look like the flag being absent and ran in local
// database mode: it created main.db, its WAL files and the data dirs, which
// is what --remote exists to avoid. It is an error now, and nothing is
// created.
func TestMCPRemoteEmptyValueNeverFallsIntoLocalMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  *string // TORQUE_MCP_REMOTE, when set
		args []string
		want string
	}{
		{name: "--remote=", args: []string{"--remote="}, want: "--remote was given an empty value"},
		{name: "empty env", env: ptr(""), want: "TORQUE_MCP_REMOTE is set but empty"},
		{name: "--remote= beats a good env", env: ptr("1"), args: []string{"--remote="}, want: "--remote was given an empty value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			untouched := lockedState(t)
			if tc.env != nil {
				t.Setenv("TORQUE_MCP_REMOTE", *tc.env)
			}
			err, out, _ := runMCP(t, tc.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, out, "stdout carries the MCP protocol")
			untouched()
		})
	}
}

func ptr(s string) *string { return &s }

// A value that cannot be validated is never echoed: a URL with its scheme
// forgotten, a bad port or a stray bracket fails to parse, and what
// url.Parse cannot split a scrubber cannot either. That holds for --remote=,
// TORQUE_MCP_REMOTE and a positional argument, and for a parseable URL of
// the wrong scheme.
func TestMCPRemoteDoesNotEchoCredentialsItCannotValidate(t *testing.T) {
	values := []string{
		"alice:hunter2@127.0.0.1:8990/mcp",        // scheme forgotten
		"http://alice:hunter2@127.0.0.1:99x/mcp",  // bad port
		"http://alice:hunter2@[::1/mcp",           // stray bracket
		"ftp://alice:hunter2@127.0.0.1:8990/mcp",  // parses, wrong scheme
		"//alice:hunter2@127.0.0.1:8990/mcp",      // no scheme, with slashes
		"http://alice:hunter2@/mcp",               // no host
		"http://alice:hunter2@127.0.0.1:8990/mcp", // valid, so it is dialed (daemon down), and scrubbed
	}
	check := func(t *testing.T, err error, out, errOut string) {
		t.Helper()
		require.Error(t, err)
		for _, text := range []string{err.Error(), out, errOut} {
			assert.NotContains(t, text, "hunter2")
			assert.NotContains(t, text, "alice")
		}
		assert.Empty(t, out, "stdout carries the MCP protocol")
	}
	for _, v := range values[:6] {
		t.Run("flag "+v, func(t *testing.T) {
			untouched := lockedState(t)
			err, out, errOut := runMCP(t, "--remote="+v)
			check(t, err, out, errOut)
			assert.Contains(t, err.Error(), "is not an http(s) URL")
			untouched()
		})
		t.Run("env "+v, func(t *testing.T) {
			untouched := lockedState(t)
			t.Setenv("TORQUE_MCP_REMOTE", v)
			err, out, errOut := runMCP(t)
			check(t, err, out, errOut)
			assert.Contains(t, err.Error(), "is not an http(s) URL")
			untouched()
		})
	}
	for _, v := range []string{"alice:hunter2@127.0.0.1:8990/mcp", "http://alice:hunter2@127.0.0.1:8990/mcp"} {
		t.Run("positional "+v, func(t *testing.T) {
			err, out, errOut := runMCP(t, "--remote", v)
			check(t, err, out, errOut)
			assert.Contains(t, err.Error(), "--remote=URL")
		})
	}
	// A value without '@' is shown, since it can carry no credentials.
	err, _, _ := runMCP(t, "--remote=127.0.0.1:8990/mcp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"127.0.0.1:8990/mcp"`)
}

// A password with an unencoded / # or ? after an all-digit or empty prefix
// makes url.Parse accept the value: `http://alice:12/PW@127.0.0.1:1/mcp`
// parses as host "alice:12" with "PW@127.0.0.1:1/mcp" in the path, so
// u.User is nil. The user name would be dialed as a hostname and the
// credentials echoed. A real user:password@ always parses into u.User, so an
// '@' that did not is refused, hidden, before anything is dialed.
func TestMCPRemoteRefusesAnAtSignThatIsNotUserinfo(t *testing.T) {
	values := []string{
		"http://alice:12/PW@127.0.0.1:1/mcp", // /
		"http://alice:12#PW@127.0.0.1:1/mcp", // #
		"http://alice:12?PW@127.0.0.1:1/mcp", // ?
		"http://alice:/PW@127.0.0.1:1/mcp",   // empty port prefix
		"http://alice:12/PW@127.0.0.1:1/mcp?key=SECRET",
		"https://127.0.0.1:1/mcp/user@example.com", // '@' in the path, no userinfo
	}
	check := func(t *testing.T, err error, out, errOut string) {
		t.Helper()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is refused", "refused at validation, before any dial")
		for _, text := range []string{err.Error(), out, errOut} {
			for _, secret := range []string{"alice", "PW", "SECRET", "example.com"} {
				assert.NotContains(t, text, secret)
			}
		}
		assert.Empty(t, out)
	}
	for _, v := range values {
		t.Run("flag "+v, func(t *testing.T) {
			untouched := lockedState(t)
			err, out, errOut := runMCP(t, "--remote="+v)
			check(t, err, out, errOut)
			untouched()
		})
		t.Run("env "+v, func(t *testing.T) {
			untouched := lockedState(t)
			t.Setenv("TORQUE_MCP_REMOTE", v)
			err, out, errOut := runMCP(t)
			check(t, err, out, errOut)
			untouched()
		})
	}
	// A real userinfo is still accepted by the validation (it is scrubbed
	// from everything shown).
	err, _, _ := runRemoteURLCheck("http://alice:pw@127.0.0.1:1/mcp")
	assert.NoError(t, err)
}

// runRemoteURLCheck runs only runRemoteMCP's URL validation: a stdin that is
// already closed ends the bridge at once, so nothing is dialed.
func runRemoteURLCheck(endpoint string) (error, string, string) {
	cmd := mcpCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetContext(context.Background())
	return runRemoteMCP(cmd, endpoint, ""), out.String(), errOut.String()
}

// A query string can carry a key, so an error never shows one.
func TestMCPRemoteDoesNotEchoAQueryString(t *testing.T) {
	for _, v := range []string{"127.0.0.1:8990/mcp?key=QUERYSECRET", "ftp://127.0.0.1:8990/mcp?key=QUERYSECRET", "http:///mcp?key=QUERYSECRET#FRAGSECRET"} {
		err, out, errOut := runMCP(t, "--remote="+v)
		require.Error(t, err, v)
		for _, text := range []string{err.Error(), out, errOut} {
			assert.NotContains(t, text, "QUERYSECRET", v)
			assert.NotContains(t, text, "FRAGSECRET", v)
		}
	}
}

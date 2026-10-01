package mcpbridge_test

import (
	"bytes"
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
		WithMCP(bootstrap.DaemonMCPHandler(svc, nil, sessions, nil))
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
	assert.Contains(t, log.String(), "answered 502", "the bridge says what the daemon answered, on stderr")

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

// toolText calls a tool and returns its result text and whether it was an
// error result.
func toolText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return text.Text, res.IsError
}

// The daemon's /mcp is wired as stdio `torque mcp` is: with no poll
// registry. torque_inbox_poll opts a recipient out of the steering bridge's
// injection in the daemon's registry and expects a broker to drain; /mcp has
// none (torque_broker_inbox says so), so with the live registry wired a
// client could enable polling nothing drains and silence its own
// deliveries. bootstrap.DaemonMCPHandler takes no registry at all: this is
// the production constructor, so the wiring cannot be reintroduced here
// without this test failing to build or fail.
func TestDaemonMCPInboxPollIsUnavailableAsOverStdio(t *testing.T) {
	d := newDaemon(t, httpserver.Security{}, nil)
	cs := bridgeClient(t, mcpbridge.Options{Endpoint: d.srv.URL + "/mcp"})
	args := map[string]any{"to": "msg://agent/local/some-agent"}

	text, isErr := toolText(t, cs, "torque_inbox_poll", args)
	assert.True(t, isErr, "polling must not be enabled over /mcp: %s", text)
	assert.Contains(t, text, "inbox polling not available")

	released, relErr := toolText(t, cs, "torque_inbox_poll", map[string]any{"to": "msg://agent/local/some-agent", "release": true})
	assert.True(t, relErr, "nor released: %s", released)

	// The stdio-shaped adapter answers the same.
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := bootstrap.StdioMCPAdapter(d.svc, d.sessions, nil).Server().SDKServer().Connect(ctx, serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	stdio, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-shape", Version: "1"}, nil).Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdio.Close() })
	stdioText, stdioErr := toolText(t, stdio, "torque_inbox_poll", args)
	assert.True(t, stdioErr)
	assert.Contains(t, stdioText, "inbox polling not available")
}

// /mcp is behind the API's cors and token middleware, not only the SDK's own
// checks. The assertions read our messages, which the SDK does not produce,
// so they fail if either middleware is dropped from the route.
func TestDaemonMCPIsBehindTheAPIMiddleware(t *testing.T) {
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	do := func(method, url string, hdr map[string]string) (int, string) {
		req, err := http.NewRequest(method, url, strings.NewReader(initialize))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		for k, v := range hdr {
			if k == "Host" {
				req.Host = v
				continue
			}
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// corsMiddleware: a foreign browser origin is refused with its message,
	// on the call and on the preflight; a loopback origin is allowed and the
	// preflight allows the MCP headers.
	open := newDaemon(t, httpserver.Security{}, nil)
	for _, method := range []string{http.MethodPost, http.MethodOptions} {
		code, body := do(method, open.srv.URL+"/mcp", map[string]string{"Origin": "https://evil.example"})
		assert.Equal(t, http.StatusForbidden, code, method)
		assert.Contains(t, body, "origin not allowed", "%s: that is corsMiddleware's message", method)
		code, body = do(method, open.srv.URL+"/mcp", map[string]string{"Origin": "null"})
		assert.Equal(t, http.StatusForbidden, code, method+" null origin")
		assert.Contains(t, body, "origin not allowed", method+" null origin")
	}
	req, err := http.NewRequest(http.MethodOptions, open.srv.URL+"/mcp", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://localhost:5182")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Headers"), "Mcp-Session-Id")
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Headers"), "Mcp-Protocol-Version")

	// Without a token: loopback hosts only, answered by requireToken.
	code, body := do(http.MethodPost, open.srv.URL+"/mcp", map[string]string{"Host": "torque.example.com"})
	assert.Equal(t, http.StatusForbidden, code)
	assert.Contains(t, body, "host not allowed: without TORQUE_API_TOKEN")

	// With a token, requireToken answers first, whatever the Host: a missing
	// or wrong bearer is 401 on a loopback host and on another one (the
	// SDK's own localhost check alone would answer the latter 403).
	locked := newDaemon(t, httpserver.Security{Token: "s3cret-token"}, nil)
	for _, host := range []string{"", "torque.example.com"} {
		for _, auth := range []string{"", "Bearer wrong-token"} {
			code, body = do(http.MethodPost, locked.srv.URL+"/mcp", map[string]string{"Host": host, "Authorization": auth})
			assert.Equal(t, http.StatusUnauthorized, code, "host %q auth %q", host, auth)
			assert.Contains(t, body, "TORQUE_API_TOKEN", "the daemon's hint is in the body")
		}
	}
	code, _ = do(http.MethodPost, locked.srv.URL+"/mcp", map[string]string{"Authorization": "Bearer s3cret-token"})
	assert.Equal(t, http.StatusOK, code)
	// Documented limit: the SDK's localhost check ignores the token, so a
	// valid bearer on a non-loopback Host (a reverse proxy that keeps the
	// Host header) is still refused.
	code, _ = do(http.MethodPost, locked.srv.URL+"/mcp", map[string]string{"Host": "torque.example.com", "Authorization": "Bearer s3cret-token"})
	assert.Equal(t, http.StatusForbidden, code)
}

// A refused call says what the daemon answered, with its own hint, not that
// it was unreachable.
func TestBridgeNamesWhatTheDaemonAnswered(t *testing.T) {
	locked := newDaemon(t, httpserver.Security{Token: "s3cret-token"}, nil)
	log := &syncWriter{}
	cs, err := connectClient(t, mcpbridge.Options{Endpoint: locked.srv.URL + "/mcp", Log: log})
	require.Error(t, err, "no token: the handshake is refused")
	_ = cs
	assert.Contains(t, err.Error(), "answered 401 Unauthorized")
	assert.Contains(t, err.Error(), "TORQUE_API_TOKEN", "the daemon's own hint")
	assert.NotContains(t, err.Error(), "unreachable")
	assert.Contains(t, log.String(), "answered 401")
}

// The bearer is sent to the configured host only: a redirect is not
// followed, so a 307 to another host carries no Authorization.
func TestBridgeNeverFollowsARedirectWithTheBearer(t *testing.T) {
	var gotAuth atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)

	_, err := connectClient(t, mcpbridge.Options{Endpoint: redirector.URL + "/mcp", Token: "s3cret-token"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "answered 307")
	assert.Contains(t, err.Error(), "does not follow redirects")
	assert.Nil(t, gotAuth.Load(), "the other host was never called, so it saw no Authorization")
}

// A slow tool does not hold up other calls: the bridge forwards requests
// concurrently.
func TestBridgeForwardsConcurrently(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	d := newDaemon(t, httpserver.Security{}, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if bytes.Contains(body, []byte("SLOW-TASK")) {
				select {
				case started <- struct{}{}:
				default:
				}
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	cs := bridgeClient(t, mcpbridge.Options{Endpoint: d.srv.URL + "/mcp"})
	// Registered last, so it runs first: a regression fails the test below
	// instead of hanging cleanup on the blocked handler.
	var releaseOnce sync.Once
	releaseSlow := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseSlow)

	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		_, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "torque_task_get", Arguments: map[string]any{"id": "SLOW-TASK"}})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow call never reached the daemon")
	}

	fastDone := make(chan []string, 1)
	go func() { fastDone <- toolNames(t, cs) }()
	select {
	case names := <-fastDone:
		assert.Contains(t, names, "torque_task_get", "a fast call completes while the slow one is still running")
	case <-time.After(5 * time.Second):
		t.Fatal("a slow tool blocked another call")
	}
	select {
	case <-slowDone:
		t.Fatal("the slow call finished before it was released")
	default:
	}
	releaseSlow()
	<-slowDone
}

// When the daemon's connection dies after the request was accepted, the
// client is answered at once with an error, not left to time out.
func TestBridgeAnswersOutstandingRequestsWhenTheConnectionDies(t *testing.T) {
	d := newDaemon(t, httpserver.Security{}, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte("DROP-TASK")) {
				// Accept the call as an event stream, then break it: a line
				// that is not SSE fails the SDK's connection for good, so the
				// bridge's connection to the daemon dies with the call
				// outstanding.
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "this is not an event stream line\n\n")
				w.(http.Flusher).Flush()
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	})
	cs := bridgeClient(t, mcpbridge.Options{Endpoint: d.srv.URL + "/mcp"})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "torque_task_get", Arguments: map[string]any{"id": "DROP-TASK"}})
	require.Error(t, err)
	assert.NoError(t, ctx.Err(), "answered, not timed out: %v", err)
	assert.Less(t, time.Since(start), 5*time.Second)

	// And the bridge is still serving.
	assert.Contains(t, toolNames(t, cs), "torque_task_get")
}

// Credentials in the endpoint URL never reach an error or a log line.
func TestBridgeDoesNotEchoEndpointCredentials(t *testing.T) {
	log := &syncWriter{}
	_, err := connectClient(t, mcpbridge.Options{Endpoint: "http://alice:hunter2@127.0.0.1:1/mcp", Log: log})
	require.Error(t, err)
	for _, text := range []string{err.Error(), log.String()} {
		assert.NotContains(t, text, "hunter2")
		assert.NotContains(t, text, "alice")
	}
	assert.Contains(t, err.Error(), "127.0.0.1:1")
}

// A bearer over plain http to a non-loopback host is allowed but warned
// about.
func TestBridgeWarnsAboutABearerOverPlainHTTP(t *testing.T) {
	run := func(endpoint, token string) string {
		log := &syncWriter{}
		local := &mcp.IOTransport{Reader: io.NopCloser(strings.NewReader("")), Writer: nopWriteCloser{io.Discard}}
		require.NoError(t, mcpbridge.Run(context.Background(), local, mcpbridge.Options{Endpoint: endpoint, Token: token, Log: log}))
		return log.String()
	}
	assert.Contains(t, run("http://192.0.2.10:8990/mcp", "tok"), "warning: sending the API token over plain http to 192.0.2.10")
	assert.Empty(t, run("http://127.0.0.1:8990/mcp", "tok"), "loopback is fine")
	assert.Empty(t, run("http://localhost:8990/mcp", "tok"))
	assert.Empty(t, run("https://192.0.2.10:8990/mcp", "tok"), "https is fine")
	assert.Empty(t, run("http://192.0.2.10:8990/mcp", ""), "no token, nothing to leak")
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// connectClient is bridgeClient for a handshake that is expected to fail: it
// returns the error instead of failing the test.
func connectClient(t *testing.T, opts mcpbridge.Options) (*mcp.ClientSession, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	toBridge, fromClient := io.Pipe()
	toClient, fromBridge := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- mcpbridge.Run(ctx, &mcp.IOTransport{Reader: toBridge, Writer: fromBridge}, opts)
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "bridge-test", Version: "1"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: toClient, Writer: fromClient}, nil)
	t.Cleanup(func() {
		if cs != nil {
			_ = cs.Close()
		}
		_ = fromClient.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the bridge did not stop")
		}
	})
	return cs, err
}

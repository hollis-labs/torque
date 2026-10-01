package mcpbridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trackedConn is a daemon connection whose Close is recorded.
type trackedConn struct {
	mcp.Connection
	closed atomic.Bool
}

func (c *trackedConn) Close() error {
	c.closed.Store(true)
	return c.Connection.Close()
}

// testBridge is a bridge whose client end the test reads from, with every
// connection it dials recorded.
type testBridge struct {
	b      *bridge
	client mcp.Connection
	mu     sync.Mutex
	conns  []*trackedConn
}

func newTestBridge(t *testing.T, opts Options) *testBridge {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	lt, ct := mcp.NewInMemoryTransports()
	lconn, err := lt.Connect(ctx)
	require.NoError(t, err)
	cconn, err := ct.Connect(ctx)
	require.NoError(t, err)
	tb := &testBridge{b: newBridge(opts, lconn), client: cconn}
	dial := tb.b.dial
	tb.b.dial = func(ctx context.Context) (mcp.Connection, error) {
		c, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		tc := &trackedConn{Connection: c}
		tb.mu.Lock()
		tb.conns = append(tb.conns, tc)
		tb.mu.Unlock()
		return tc, nil
	}
	return tb
}

func (tb *testBridge) dialed() []*trackedConn {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return append([]*trackedConn(nil), tb.conns...)
}

// reply reads the next message the bridge sent the client.
func (tb *testBridge) reply(t *testing.T) *jsonrpc.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, err := tb.client.Read(ctx)
	require.NoError(t, err)
	resp, ok := msg.(*jsonrpc.Response)
	require.True(t, ok, "%T", msg)
	return resp
}

// settled reports whether every forwarded message has finished.
func (tb *testBridge) settled(d time.Duration) bool {
	done := make(chan struct{})
	go func() { tb.b.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func call(t *testing.T, id int) *jsonrpc.Request {
	t.Helper()
	jid, err := jsonrpc.MakeID(float64(id))
	require.NoError(t, err)
	return &jsonrpc.Request{ID: jid, Method: "ping"}
}

// echoDaemon answers each JSON-RPC call with an empty result under
// replyID(the call's raw id), and accepts every notification.
func echoDaemon(t *testing.T, replyID func(rawID string) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(body, &req) != nil {
			http.Error(w, "not json", http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 { // a notification
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+replyID(string(req.ID))+`,"result":{}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// No connection is reused: every forwarded message, calls and a
// notification alike, is dialed on its own and closed once it is done. This
// is what keeps a failed request from failing its neighbours (a shared
// connection is permanently failed by the SDK on a 400, 401, 403, 404 or
// 500), so it is pinned here and not left to the behaviour test alone.
func TestEachForwardedMessageGetsItsOwnConnection(t *testing.T) {
	srv := echoDaemon(t, func(id string) string { return id })
	tb := newTestBridge(t, Options{Endpoint: srv.URL + "/mcp"})
	ctx := context.Background()

	for id := 1; id <= 5; id++ {
		tb.b.dispatch(ctx, call(t, id))
		resp := tb.reply(t)
		assert.NoError(t, resp.Error)
		jid, _ := jsonrpc.MakeID(float64(id))
		assert.Equal(t, jid, resp.ID, "each call is answered under its own id")
	}
	tb.b.dispatch(ctx, &jsonrpc.Request{Method: "notifications/initialized"})
	require.True(t, tb.settled(5*time.Second))

	conns := tb.dialed()
	require.Len(t, conns, 6, "one connection per message")
	seen := map[*trackedConn]bool{}
	for i, c := range conns {
		assert.False(t, seen[c], "connection %d was dialed again", i)
		seen[c] = true
		assert.True(t, c.closed.Load(), "connection %d was left open", i)
	}
}

// The first reply on a call's connection is the reply. One that names
// another id used to be skipped while the call waited for its own, which
// never came: the call hung and held one of the in-flight slots for good. It
// is an error answered at once, and the slot is released.
func TestAReplyToAnotherIDFailsTheCallInsteadOfHangingIt(t *testing.T) {
	srv := echoDaemon(t, func(string) string { return "999" })
	tb := newTestBridge(t, Options{Endpoint: srv.URL + "/mcp"})

	tb.b.dispatch(context.Background(), call(t, 1))
	resp := tb.reply(t)
	require.Error(t, resp.Error)
	assert.Contains(t, resp.Error.Error(), "replied to a different request")
	jid, _ := jsonrpc.MakeID(float64(1))
	assert.Equal(t, jid, resp.ID, "answered under the id the client sent")
	require.True(t, tb.settled(5*time.Second), "the call must not hang")
	assert.Empty(t, tb.b.sem, "its slot was released")
	for _, c := range tb.dialed() {
		assert.True(t, c.closed.Load())
	}
}

// A query string can carry a key; neither the error the client sees nor the
// bridge's own log carries it, though the SDK's errors quote the full URL.
func TestBridgeErrorsDoNotCarryTheQueryString(t *testing.T) {
	var log strings.Builder
	tb := newTestBridge(t, Options{Endpoint: "http://127.0.0.1:1/mcp?key=QUERYSECRET#FRAGSECRET", Log: &log})

	tb.b.dispatch(context.Background(), call(t, 1))
	resp := tb.reply(t)
	require.Error(t, resp.Error)
	assert.Contains(t, resp.Error.Error(), "unreachable at http://127.0.0.1:1/mcp")
	for _, text := range []string{resp.Error.Error(), log.String()} {
		assert.NotContains(t, text, "QUERYSECRET")
		assert.NotContains(t, text, "FRAGSECRET")
	}
	require.True(t, tb.settled(5*time.Second))
}

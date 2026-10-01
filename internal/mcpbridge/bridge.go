// Package mcpbridge connects a local MCP client to the Torque daemon's
// /mcp endpoint: `torque mcp --remote` (CW-20261001-0199).
//
// An agent under ProtectedPaths cannot open main.db, so the stdio
// `torque mcp` that mux starts for it cannot serve. The bridge serves the
// same tool surface by forwarding each JSON-RPC message, unchanged, to the
// daemon over MCP Streamable HTTP and relaying the replies. It opens no
// database and writes nothing.
package mcpbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures a bridge.
type Options struct {
	// Endpoint is the daemon's MCP URL, e.g. http://127.0.0.1:8990/mcp.
	Endpoint string
	// Token, when set, is sent as "Authorization: Bearer <token>", as the
	// daemon's API requires when TORQUE_API_TOKEN is configured.
	Token string
	// HTTPClient overrides the client used to reach the daemon.
	HTTPClient *http.Client
	// Log receives one line per request the daemon could not be reached
	// for. Nil discards them.
	Log io.Writer
}

// Run relays messages between local and the daemon until local closes
// (which returns nil) or ctx ends. A request the daemon cannot be reached
// for is answered with a JSON-RPC error and the bridge keeps running, so a
// daemon restart costs the client those calls, not its MCP server.
func Run(ctx context.Context, local mcp.Transport, opts Options) error {
	if opts.Endpoint == "" {
		return errors.New("mcpbridge: no endpoint")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	lconn, err := local.Connect(ctx)
	if err != nil {
		return fmt.Errorf("mcpbridge: connect local: %w", err)
	}
	defer lconn.Close()

	b := &bridge{opts: opts, local: lconn}
	defer b.closeRemote()

	for {
		msg, err := lconn.Read(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, mcp.ErrConnectionClosed) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("mcpbridge: read local: %w", err)
		}
		if err := b.forward(ctx, msg); err != nil {
			return err
		}
	}
}

type bridge struct {
	opts  Options
	local mcp.Connection

	mu     sync.Mutex
	remote mcp.Connection
}

// forward sends one local message to the daemon. A failed send drops the
// remote connection, so the next message reconnects, and answers the
// message if it expected a reply.
func (b *bridge) forward(ctx context.Context, msg jsonrpc.Message) error {
	remote, err := b.remoteConn(ctx)
	if err == nil {
		if err = remote.Write(ctx, msg); err == nil {
			return nil
		}
		b.dropRemote(remote)
	}
	if b.opts.Log != nil {
		fmt.Fprintf(b.opts.Log, "torque mcp --remote: %s unreachable: %v\n", b.opts.Endpoint, err)
	}
	req, ok := msg.(*jsonrpc.Request)
	if !ok || !req.ID.IsValid() {
		return nil // a notification or response: nothing waits on it
	}
	reply := &jsonrpc.Response{ID: req.ID, Error: &jsonrpc.Error{
		Code:    jsonrpc.CodeInternalError,
		Message: fmt.Sprintf("torque daemon unreachable at %s: %v", b.opts.Endpoint, err),
	}}
	if werr := b.local.Write(ctx, reply); werr != nil {
		return fmt.Errorf("mcpbridge: write local: %w", werr)
	}
	return nil
}

// remoteConn returns the daemon connection, connecting when there is none,
// and starts relaying its messages to the local client.
func (b *bridge) remoteConn(ctx context.Context) (mcp.Connection, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remote != nil {
		return b.remote, nil
	}
	t := &mcp.StreamableClientTransport{
		Endpoint:             b.opts.Endpoint,
		HTTPClient:           b.httpClient(),
		MaxRetries:           -1,
		DisableStandaloneSSE: true, // the daemon's endpoint is stateless
	}
	conn, err := t.Connect(ctx)
	if err != nil {
		return nil, err
	}
	b.remote = conn
	go b.relay(ctx, conn)
	return conn, nil
}

// relay copies the daemon's messages (replies and their notifications) to
// the local client until the connection ends.
func (b *bridge) relay(ctx context.Context, conn mcp.Connection) {
	for {
		msg, err := conn.Read(ctx)
		if err != nil {
			b.dropRemote(conn)
			return
		}
		if err := b.local.Write(ctx, msg); err != nil {
			return
		}
	}
}

func (b *bridge) dropRemote(conn mcp.Connection) {
	b.mu.Lock()
	if b.remote == conn {
		b.remote = nil
	}
	b.mu.Unlock()
	_ = conn.Close()
}

func (b *bridge) closeRemote() {
	b.mu.Lock()
	conn := b.remote
	b.remote = nil
	b.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (b *bridge) httpClient() *http.Client {
	c := b.opts.HTTPClient
	if c == nil {
		c = &http.Client{}
	}
	if b.opts.Token == "" {
		return c
	}
	withToken := *c
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	withToken.Transport = bearer{token: b.opts.Token, next: base}
	return &withToken
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (t bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(r)
}

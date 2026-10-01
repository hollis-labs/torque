// Package mcpbridge connects a local MCP client to the Torque daemon's
// /mcp endpoint: `torque mcp --remote` (CW-20261001-0199).
//
// An agent under ProtectedPaths cannot open main.db, so the stdio
// `torque mcp` that mux starts for it cannot serve. The bridge serves the
// same tool surface by forwarding each JSON-RPC message, unchanged, to the
// daemon over MCP Streamable HTTP and relaying the reply. It opens no
// database and writes nothing.
//
// The daemon's endpoint is stateless, so the bridge keeps no connection
// between messages: each forwarded message gets a connection of its own,
// which is closed once its reply has been relayed. A request that fails
// (a 400, a 500 from a tool that panicked, a refused token) therefore fails
// only itself; the calls in flight beside it complete and are answered
// normally. Requests are forwarded concurrently, at most maxInFlight at a
// time, so a slow tool does not hold up health checks or other calls.
//
// What the bridge does not preserve, because nothing here is stateful:
//   - the order in which concurrent messages reach the daemon, notifications
//     included (a notification may overtake the request sent before it);
//   - a client's cancellation of a request: the daemon finishes the call and
//     its reply is dropped.
//
// Either would matter if the daemon's MCP handler became stateful.
package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RemoteEnv selects `torque mcp --remote` without the flag: "1" (or
// "true") for the daemon on this host, or a URL. `torque mcp` reads it, so a
// launcher can set it in the environment of the `torque mcp` it starts; the
// automatic setting of it in the planted mux entry's env for agents under
// ProtectedPaths is CW-20261001-0320.
const RemoteEnv = "TORQUE_MCP_REMOTE"

// maxInFlight bounds the requests forwarded at once. When it is reached the
// bridge stops reading from the client until one finishes.
const maxInFlight = 32

// Options configures a bridge.
type Options struct {
	// Endpoint is the daemon's MCP URL, e.g. http://127.0.0.1:8990/mcp.
	Endpoint string
	// Token, when set, is sent as "Authorization: Bearer <token>", as the
	// daemon's API requires when TORQUE_API_TOKEN is configured. It is
	// never sent to another host: the bridge does not follow redirects.
	Token string
	// HTTPClient overrides the client used to reach the daemon.
	HTTPClient *http.Client
	// Log receives one line per request the daemon could not serve, and
	// warnings. Nil discards them.
	Log io.Writer
}

// Run relays messages between local and the daemon until local closes
// (which returns nil) or ctx ends. A request the daemon cannot serve is
// answered with a JSON-RPC error and the bridge keeps running, so a daemon
// restart costs the client those calls, not its MCP server.
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

	b := newBridge(opts, lconn)
	b.warnInsecure()
	defer b.shutdown(cancel)

	for {
		msg, err := lconn.Read(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, mcp.ErrConnectionClosed) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("mcpbridge: read local: %w", err)
		}
		b.dispatch(ctx, msg)
	}
}

type bridge struct {
	opts     Options
	endpoint string // Options.Endpoint without credentials, for messages
	scrubs   []string
	client   *http.Client
	local    mcp.Connection
	localMu  sync.Mutex // one message at a time to the client
	sem      chan struct{}
	wg       sync.WaitGroup
}

func newBridge(opts Options, local mcp.Connection) *bridge {
	b := &bridge{
		opts:     opts,
		endpoint: opts.Endpoint,
		local:    local,
		sem:      make(chan struct{}, maxInFlight),
	}
	b.client = b.httpClient()
	if u, err := url.Parse(opts.Endpoint); err == nil && u.User != nil {
		// Credentials in the URL never reach a log line or an error.
		user := u.User.Username()
		pass, _ := u.User.Password()
		b.scrubs = []string{u.User.String() + "@", user + ":***@", user + "@"}
		if pass != "" {
			b.scrubs = append(b.scrubs, pass)
		}
		u.User = nil
		b.endpoint = u.String()
	}
	return b
}

// scrub removes the endpoint's credentials from s.
func (b *bridge) scrub(s string) string {
	for _, x := range b.scrubs {
		s = strings.ReplaceAll(s, x, "")
	}
	return s
}

func (b *bridge) logf(format string, args ...any) {
	if b.opts.Log != nil {
		fmt.Fprintf(b.opts.Log, "torque mcp --remote: %s\n", b.scrub(fmt.Sprintf(format, args...)))
	}
}

// warnInsecure says so when the bearer would cross the network in clear.
func (b *bridge) warnInsecure() {
	u, err := url.Parse(b.opts.Endpoint)
	if err != nil || b.opts.Token == "" || u.Scheme != "http" || loopbackHost(u.Hostname()) {
		return
	}
	b.logf("warning: sending the API token over plain http to %s; use https or a loopback address", u.Hostname())
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// dispatch forwards one client message in its own goroutine, once a slot is
// free.
func (b *bridge) dispatch(ctx context.Context, msg jsonrpc.Message) {
	select {
	case b.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer func() { <-b.sem }()
		b.forward(ctx, msg)
	}()
}

// forward sends one client message to the daemon on a connection of its
// own and, for a request, relays the reply. Whatever goes wrong here is this
// message's alone: a request the daemon cannot answer is answered here with
// a JSON-RPC error, exactly once.
func (b *bridge) forward(ctx context.Context, msg jsonrpc.Message) {
	var callID jsonrpc.ID
	isCall := false
	if req, ok := msg.(*jsonrpc.Request); ok && req.IsCall() {
		callID, isCall = req.ID, true
	}
	note := &respNote{}
	t := &mcp.StreamableClientTransport{
		Endpoint:             b.opts.Endpoint,
		HTTPClient:           b.client,
		MaxRetries:           -1,
		DisableStandaloneSSE: true, // the daemon's endpoint is stateless
	}
	accepted := false
	conn, err := t.Connect(ctx)
	if err == nil {
		defer conn.Close()
		if err = conn.Write(context.WithValue(ctx, noteKey{}, note), msg); err == nil {
			if !isCall {
				return
			}
			accepted = true
			err = b.await(ctx, conn, callID)
		}
	}
	if err == nil || ctx.Err() != nil {
		return
	}
	text := b.describe(err, note, accepted)
	b.logf("%s", text)
	if isCall {
		_ = b.writeLocal(ctx, &jsonrpc.Response{ID: callID, Error: &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: text}})
	}
}

// await relays what the daemon sends on conn (notifications that belong to
// the call, then its reply) to the client, and returns nil once the reply
// for id has been relayed, or the error that ended the connection first.
func (b *bridge) await(ctx context.Context, conn mcp.Connection, id jsonrpc.ID) error {
	for {
		msg, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if resp, ok := msg.(*jsonrpc.Response); ok && resp.ID != id {
			continue // not this call's reply
		}
		if err := b.writeLocal(ctx, msg); err != nil {
			return err
		}
		if _, ok := msg.(*jsonrpc.Response); ok {
			return nil
		}
	}
}

// describe explains a failed send or reply: what the daemon answered when it
// did (with its own message, which carries hints such as the token variable
// to set), that the connection ended after the daemon accepted the request,
// or that the daemon could not be reached.
func (b *bridge) describe(err error, note *respNote, accepted bool) string {
	status, body := note.get()
	switch {
	case status != 0:
		text := fmt.Sprintf("torque daemon at %s answered %d %s", b.endpoint, status, http.StatusText(status))
		var env struct {
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(body), &env) == nil && env.Error != "" {
			text += ": " + env.Error
		} else if s := strings.TrimSpace(body); s != "" {
			text += ": " + s
		}
		if status >= 300 && status < 400 {
			text += " (the bridge does not follow redirects; point --remote at the daemon's own /mcp URL)"
		}
		return b.scrub(text)
	case accepted:
		return b.scrub(fmt.Sprintf("torque daemon connection to %s ended before the reply: %v", b.endpoint, err))
	}
	return b.scrub(fmt.Sprintf("torque daemon unreachable at %s: %v", b.endpoint, err))
}

// writeLocal sends one message to the client; writes from the goroutines
// forwarding never interleave.
func (b *bridge) writeLocal(ctx context.Context, msg jsonrpc.Message) error {
	b.localMu.Lock()
	defer b.localMu.Unlock()
	return b.local.Write(ctx, msg)
}

// shutdown cancels the forwarding goroutines and waits for them.
func (b *bridge) shutdown(cancel context.CancelFunc) {
	cancel()
	b.wg.Wait()
}

// httpClient is the client for the daemon: it sends the bearer, records what
// the daemon answers when it refuses, and never follows a redirect, so the
// bearer cannot be handed to another host.
func (b *bridge) httpClient() *http.Client {
	c := &http.Client{}
	if b.opts.HTTPClient != nil {
		cp := *b.opts.HTTPClient
		c = &cp
	}
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = roundTripper{token: b.opts.Token, next: base}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// noteKey carries a respNote in a request's context.
type noteKey struct{}

// respNote is what the daemon answered a request with when it was not a
// success, kept so the error shown names it: the SDK keeps only the status
// text and drops the body, whose message says what to fix.
type respNote struct {
	mu     sync.Mutex
	status int
	body   string
}

func (n *respNote) set(status int, body string) {
	n.mu.Lock()
	n.status, n.body = status, body
	n.mu.Unlock()
}

func (n *respNote) get() (int, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status, n.body
}

type roundTripper struct {
	token string
	next  http.RoundTripper
}

func (t roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if t.token != "" {
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.next.RoundTrip(r)
	if err != nil || resp.StatusCode < 300 {
		return resp, err
	}
	if note, ok := r.Context().Value(noteKey{}).(*respNote); ok {
		peek := make([]byte, 512)
		n, _ := io.ReadFull(resp.Body, peek)
		note.set(resp.StatusCode, string(peek[:n]))
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(peek[:n]), resp.Body), resp.Body}
	}
	return resp, nil
}

// Package mcpbridge connects a local MCP client to the Torque daemon's
// /mcp endpoint: `torque mcp --remote` (CW-20261001-0199).
//
// An agent under ProtectedPaths cannot open main.db, so the stdio
// `torque mcp` that mux starts for it cannot serve. The bridge serves the
// same tool surface by forwarding each JSON-RPC message, unchanged, to the
// daemon over MCP Streamable HTTP and relaying the replies. It opens no
// database and writes nothing.
//
// The daemon's endpoint is stateless, so a client's cancellation of a
// request is not propagated: the daemon finishes the call and its reply is
// dropped. Requests are forwarded concurrently (at most maxInFlight at a
// time), so a slow tool does not hold up health checks or other calls.
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
// "true") for the daemon on this host, or a URL. Torque sets it in the
// planted mux entry's env for agents under ProtectedPaths; mux passes its
// env to the `torque mcp` it starts.
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
	local    mcp.Connection
	localMu  sync.Mutex // one message at a time to the client
	sem      chan struct{}
	wg       sync.WaitGroup

	mu      sync.Mutex
	remote  mcp.Connection
	pending map[jsonrpc.ID]*pendingCall // requests forwarded, not yet answered
	dead    map[mcp.Connection]error    // connections that ended, and why
}

// pendingCall is a request owed an answer.
type pendingCall struct {
	conn mcp.Connection // the connection it went on
	// sent is set once the daemon has accepted the request. Until then
	// forward is still sending it and answers it itself, with what the
	// daemon said; the relay answers only the requests that were accepted.
	sent bool
}

func newBridge(opts Options, local mcp.Connection) *bridge {
	b := &bridge{
		opts:     opts,
		endpoint: opts.Endpoint,
		local:    local,
		sem:      make(chan struct{}, maxInFlight),
		pending:  map[jsonrpc.ID]*pendingCall{},
		dead:     map[mcp.Connection]error{},
	}
	if u, err := url.Parse(opts.Endpoint); err == nil && u.User != nil {
		// Credentials in the URL never reach a log line or an error.
		user, pass := u.User.Username(), ""
		pass, _ = u.User.Password()
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

// forward sends one client message to the daemon. A failed send drops the
// remote connection, so the next message reconnects, and answers the
// message if it expected a reply.
func (b *bridge) forward(ctx context.Context, msg jsonrpc.Message) {
	var callID jsonrpc.ID
	isCall := false
	if req, ok := msg.(*jsonrpc.Request); ok && req.IsCall() {
		callID, isCall = req.ID, true
	}
	note := &respNote{}
	remote, err := b.remoteConn(ctx)
	if err == nil {
		if isCall {
			b.track(callID, remote)
		}
		if err = remote.Write(context.WithValue(ctx, noteKey{}, note), msg); err == nil {
			if isCall {
				if ended := b.markSent(callID); ended != nil {
					b.answer(ctx, callID, b.connEnded(ended))
				}
			}
			return
		}
		b.dropRemote(remote)
	}
	text := b.describe(err, note)
	b.logf("%s", text)
	if isCall {
		b.answer(ctx, callID, text)
	}
}

// describe explains a failed send: what the daemon answered when it did
// (with its own message, which carries hints such as the token variable to
// set), and otherwise that it could not be reached.
func (b *bridge) describe(err error, note *respNote) string {
	status, body := note.get()
	if status == 0 {
		return b.scrub(fmt.Sprintf("torque daemon unreachable at %s: %v", b.endpoint, err))
	}
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
}

// remoteConn returns the daemon connection, connecting when there is none,
// and starts relaying its messages to the client.
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
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.relay(ctx, conn)
	}()
	return conn, nil
}

// track records a request about to be sent on conn, so its answer is owed.
func (b *bridge) track(id jsonrpc.ID, conn mcp.Connection) {
	b.mu.Lock()
	b.pending[id] = &pendingCall{conn: conn}
	b.mu.Unlock()
}

// markSent records that the daemon accepted request id. It returns why the
// connection ended when it ended before this was recorded: the relay, which
// answers accepted requests when a connection ends, did not see this one.
func (b *bridge) markSent(id jsonrpc.ID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.pending[id]
	if !ok {
		return nil // answered already
	}
	if err, ended := b.dead[p.conn]; ended {
		return err
	}
	p.sent = true
	return nil
}

// connEnded is the text of the error for a request whose connection ended
// after the daemon accepted it.
func (b *bridge) connEnded(cause error) string {
	return b.scrub(fmt.Sprintf("torque daemon connection to %s ended before the reply: %v", b.endpoint, cause))
}

// takePending reports whether id was still owed an answer, and settles it.
func (b *bridge) takePending(id jsonrpc.ID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.pending[id]; !ok {
		return false
	}
	delete(b.pending, id)
	return true
}

// answer replies to request id with a JSON-RPC internal error, unless it was
// answered already.
func (b *bridge) answer(ctx context.Context, id jsonrpc.ID, message string) {
	if !b.takePending(id) {
		return
	}
	_ = b.writeLocal(ctx, &jsonrpc.Response{ID: id, Error: &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: message}})
}

// writeLocal sends one message to the client; writes from the goroutines
// forwarding and relaying never interleave.
func (b *bridge) writeLocal(ctx context.Context, msg jsonrpc.Message) error {
	b.localMu.Lock()
	defer b.localMu.Unlock()
	return b.local.Write(ctx, msg)
}

// relay copies the daemon's messages (replies and their notifications) to
// the client until the connection ends. When it ends, every request sent on
// it and not yet answered is answered with an error now, rather than left
// for the client to time out.
func (b *bridge) relay(ctx context.Context, conn mcp.Connection) {
	for {
		msg, err := conn.Read(ctx)
		if err != nil {
			b.dropRemote(conn)
			b.failPending(ctx, conn, err)
			return
		}
		if resp, ok := msg.(*jsonrpc.Response); ok && !b.takePending(resp.ID) {
			continue // answered already, or not ours
		}
		_ = b.writeLocal(ctx, msg)
	}
}

// failPending records that conn ended and answers the requests the daemon
// had accepted on it. A request still being sent is not answered here:
// forward answers it, naming what the daemon said (a refused send is what
// ends the connection in the first place).
func (b *bridge) failPending(ctx context.Context, conn mcp.Connection, cause error) {
	b.mu.Lock()
	b.dead[conn] = cause
	var ids []jsonrpc.ID
	for id, p := range b.pending {
		if p.conn == conn && p.sent {
			ids = append(ids, id)
		}
	}
	b.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	text := b.connEnded(cause)
	b.logf("%s", text)
	for _, id := range ids {
		b.answer(ctx, id, text)
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

// shutdown stops the forwarding and relaying goroutines and closes the
// daemon connection.
func (b *bridge) shutdown(cancel context.CancelFunc) {
	cancel()
	b.mu.Lock()
	conn := b.remote
	b.remote = nil
	b.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
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

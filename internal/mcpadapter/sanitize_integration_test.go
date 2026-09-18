package mcpadapter

// Integration test for go-mcp's sanitize middleware install
// (CW-20260509-0033, mirrors vanta-conduit v0.6.1 Pattern A; ported onto
// go-mcp's sanitize package for CW-20260917-0014).
//
// Lives in `package mcpadapter` (not `_test`) so it can reach a.Server() and
// a.svc directly. Unlike the pre-migration version, sanitize is now
// installed once at the protocol level (Server()'s
// AddReceivingMiddleware(sanitize.Middleware(...)) call, not wrapped around
// each handler individually), so exercising it requires a real protocol
// round-trip rather than calling the handler function directly -- an
// in-memory client/server transport pair drives that without a real
// process boundary. End-to-end business-logic coverage lives in
// task_tools_test.go via callTool (Server.CallTool, which bypasses this
// middleware layer on purpose — see its doc comment); this file specifically
// locks the sanitize-middleware wiring shape.

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"

	_ "modernc.org/sqlite"
)

// newSanitizeTestAdapter builds an Adapter wired against an in-memory sqlite
// store with the slog logger captured to the t.Log buffer. Mirrors the
// production wiring shape (cmd/torque/mcp.go) minus the agent.Manager
// (torque_task_create doesn't need it).
func newSanitizeTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)
	a := New(svc, nil)
	a.Logger = slog.New(slog.NewTextHandler(testLogWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return a
}

// testLogWriter routes slog output to t.Log so cleaned-call telemetry is
// visible in -v test runs without polluting stderr.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// callToolOverProtocol drives a real MCP protocol round-trip against a, over
// an in-memory transport pair, so protocol-level middleware (sanitize)
// actually runs — unlike Server.CallTool's direct in-process path. Returns
// the unmarshaled `data` field of the {ok, data, error} envelope; fails the
// test on non-text content or ok=false.
func callToolOverProtocol(t *testing.T, a *Adapter, name string, args map[string]any) map[string]any {
	t.Helper()
	ctx := context.Background()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "sanitize-test-client", Version: "0.0.0"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()

	serverSession, err := a.Server().SDKServer().Connect(ctx, t1, nil)
	require.NoError(t, err, "server connect")
	t.Cleanup(func() { _ = serverSession.Wait() })

	clientSession, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err, "client connect")
	defer clientSession.Close()

	res, err := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, "CallTool transport error")
	require.NotNil(t, res, "tool result must not be nil")
	require.False(t, res.IsError, "tool result reported IsError")
	require.NotEmpty(t, res.Content, "tool result must have content")
	textContent, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok, "expected TextContent, got %T", res.Content[0])

	var env struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &env), "envelope unmarshal: %s", textContent.Text)
	require.True(t, env.OK, "expected ok=true, got: %s", textContent.Text)

	var data map[string]any
	require.NoError(t, json.Unmarshal(env.Data, &data), "data unmarshal: %s", textContent.Text)
	return data
}

// TestSanitizeMiddleware_PollutedTaskCreate exercises go-mcp's sanitize
// middleware against the smoking-gun shape: a torque_task_create call
// where description ends with leaked agent-XML markup
// (`</description>\n<parameter name="title">REAL_TITLE</parameter>...`),
// with a separate clean title field also present in the call.
//
// After the wrapped handler runs, the persisted TaskRecord must have:
//   - description cleaned (no closing tag, no leaked <parameter> markup)
//   - title preserved as the agent's clean copy (NOT overwritten by the
//     leaked fragment)
//
// This locks in the integration as wired by Adapter.Server()'s
// AddReceivingMiddleware install — same Pattern A proven in vanta-conduit
// v0.6.1, now ported onto go-mcp's sanitize package.
func TestSanitizeMiddleware_PollutedTaskCreate(t *testing.T) {
	a := newSanitizeTestAdapter(t)

	cleanTitle := "Fix auth bug"
	cleanDescription := "Login returns 500 when the session cookie is missing the trust facet."

	// The smoking-gun shape: description has a leaked closing tag plus
	// duplicated <parameter name="title">...</parameter> XML markup
	// inlined as if the LLM had tail-leaked agent tool-call framing.
	pollutedDescription := cleanDescription +
		"</description>\n" +
		"<parameter name=\"title\">REAL_TITLE_LEAKED</parameter>"

	data := callToolOverProtocol(t, a, "torque_task_create", map[string]any{
		"title":       cleanTitle,
		"description": pollutedDescription,
		"priority":    "1",
	})
	id, ok := data["ID"].(string)
	require.True(t, ok, "response should contain string ID; got %v", data)
	require.NotEmpty(t, id)

	// Read the persisted task back through the service layer to verify the
	// stored shape matches what the sanitizer should have cleaned.
	task, err := a.svc.Task.Get(id)
	require.NoError(t, err, "Get task by ID")

	// Assertion 1 — description is cleaned: no closing tag, no leaked
	// <parameter> markup.
	require.NotContains(t, task.Description, "</description>",
		"description still contains closing </description> tag: %q", task.Description)
	require.NotContains(t, task.Description, "<parameter",
		"description still contains <parameter ...> markup: %q", task.Description)
	require.True(t, strings.HasPrefix(task.Description, "Login returns 500"),
		"description lost its real content; got: %q", task.Description)

	// Assertion 2 — title is preserved as the agent's clean copy. The
	// middleware must NOT overwrite the explicit clean title field with
	// anything it recovered from the polluted description.
	require.Equal(t, cleanTitle, task.Title,
		"title was overwritten or lost.\n  want: %q\n  got:  %q", cleanTitle, task.Title)
}

// TestSanitizeMiddleware_CleanTaskCreatePassesThrough confirms the middleware
// is a near-no-op on already-clean calls: no rewrite, no log noise, identical
// persisted shape.
func TestSanitizeMiddleware_CleanTaskCreatePassesThrough(t *testing.T) {
	a := newSanitizeTestAdapter(t)

	data := callToolOverProtocol(t, a, "torque_task_create", map[string]any{
		"title":       "Clean title, no markup",
		"description": "Clean description, no markup at all.",
		"priority":    "2",
	})
	id, ok := data["ID"].(string)
	require.True(t, ok, "response should contain string ID; got %v", data)

	task, err := a.svc.Task.Get(id)
	require.NoError(t, err)
	require.Equal(t, "Clean title, no markup", task.Title, "clean title mutated")
	require.Equal(t, "Clean description, no markup at all.", task.Description, "clean description mutated")
}

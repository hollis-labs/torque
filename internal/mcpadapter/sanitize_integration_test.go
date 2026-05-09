package mcpadapter

// Integration test for the go-mcp-sanitize middleware install
// (CW-20260509-0033, mirrors vanta-conduit v0.6.1 Pattern A).
//
// Lives in `package mcpadapter` (not `_test`) so it can call the unexported
// handleTaskCreate handler through the same middleware chain Adapter.addTool
// installs at registration. End-to-end coverage via HandleMessage lives in
// task_tools_test.go; this file specifically locks the integration shape.

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	mcpsanitize "github.com/hollis-labs/go-mcp-sanitize"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"

	_ "modernc.org/sqlite"
)

// newSanitizeTestAdapter builds an Adapter wired against an in-memory sqlite
// store with the slog logger captured to the t.Log buffer. Mirrors the
// production wiring shape (cmd/clockwork/mcp.go) minus the agent.Manager
// (clockwork_task_create doesn't need it).
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

// parseSanitizeResult extracts the {ok, data, error} envelope from a tool
// result and returns the unmarshaled data map. Fails the test on non-text
// content or non-ok=true responses.
func parseSanitizeResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.NotNil(t, res, "tool result must not be nil")
	require.NotEmpty(t, res.Content, "tool result must have content")
	textContent, ok := res.Content[0].(mcp.TextContent)
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

// TestSanitizeMiddleware_PollutedTaskCreate exercises the go-mcp-sanitize
// middleware against the smoking-gun shape: a clockwork_task_create call
// where description ends with leaked agent-XML markup
// (`</description>\n<parameter name="title">REAL_TITLE</parameter>...`),
// with a separate clean title field also present in the call.
//
// After the wrapped handler runs, the persisted TaskRecord must have:
//   - description cleaned (no closing tag, no leaked <parameter> markup)
//   - title preserved as the agent's clean copy (NOT overwritten by the
//     leaked fragment)
//
// This locks in the integration as wired by Adapter.addTool — same Pattern A
// proven in vanta-conduit v0.6.1.
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

	args := map[string]any{
		"title":       cleanTitle,
		"description": pollutedDescription,
		"priority":    "1",
	}

	// Wrap the handler with the same middleware Adapter.addTool installs.
	// This mirrors the production wiring exactly.
	wrapped := mcpsanitize.Middleware(a.Logger)(a.handleTaskCreate)

	req := mcp.CallToolRequest{}
	req.Params.Name = "clockwork_task_create"
	req.Params.Arguments = args

	res, err := wrapped(context.Background(), req)
	require.NoError(t, err, "wrapped handler error")

	data := parseSanitizeResult(t, res)
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

	args := map[string]any{
		"title":       "Clean title, no markup",
		"description": "Clean description, no markup at all.",
		"priority":    "2",
	}

	wrapped := mcpsanitize.Middleware(a.Logger)(a.handleTaskCreate)

	req := mcp.CallToolRequest{}
	req.Params.Name = "clockwork_task_create"
	req.Params.Arguments = args

	res, err := wrapped(context.Background(), req)
	require.NoError(t, err, "wrapped handler error")

	data := parseSanitizeResult(t, res)
	id, ok := data["ID"].(string)
	require.True(t, ok, "response should contain string ID; got %v", data)

	task, err := a.svc.Task.Get(id)
	require.NoError(t, err)
	require.Equal(t, "Clean title, no markup", task.Title, "clean title mutated")
	require.Equal(t, "Clean description, no markup at all.", task.Description, "clean description mutated")
}

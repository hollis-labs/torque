package mcpadapter

import (
	"context"
	"strconv"

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Adapter wires the service layer to an MCP server.
//
// loopbackTaskID is non-empty only for adapters built via NewLoopback. It
// pins every interpretive tool call (summary, blocked, review, comment_add,
// artifact_create, subtodo_*) to a specific task, so the spawned agent can
// only act on its own task. Regular adapters (built via New) leave it empty
// and do not register the loopback tool subset.
type Adapter struct {
	svc            *service.Service
	sched          *scheduler.Scheduler
	server         *server.MCPServer
	loopbackTaskID string
	// sessions wires the long-lived agent session manager (CW-20260503-0014).
	// Nil disables clockwork_session_* tools — they reply with a domain error
	// rather than panicking. Mirrors the sched=nil contract.
	sessions *agent.Manager
	// broker wires the typed envelope dispatcher (CW-20260503-0013, S1.3).
	// Nil disables clockwork_broker_* tools the same way.
	broker *broker.Broker
}

// New creates an Adapter, registers all tools, and returns it. sched may be
// nil — scheduler tools will respond with a not-available error in that case,
// mirroring httpserver.New's nil-sched → 503 contract. The stdio mcp
// subcommand runs in a separate process from serve and passes nil; tests and
// any future in-process wiring can pass a live *scheduler.Scheduler.
func New(svc *service.Service, sched *scheduler.Scheduler) *Adapter {
	s := server.NewMCPServer(
		"Clockwork Manifold",
		"0.1.0",
		server.WithToolCapabilities(true),
	)
	a := &Adapter{svc: svc, sched: sched, server: s}
	a.registerCoreTools()
	a.registerOptInTools()
	return a
}

// Server returns the underlying MCPServer.
func (a *Adapter) Server() *server.MCPServer { return a.server }

// WithSessions attaches the unified agent session manager so the
// clockwork_session_* tools surface real data. Must be called before any
// MCP requests are served (not goroutine-safe with respect to live calls).
//
// Renamed from WithSessionMgr (CW-20260508-0001) when sessionmgr was folded
// into the agent package.
func (a *Adapter) WithSessions(mgr *agent.Manager) *Adapter {
	a.sessions = mgr
	return a
}

// WithBroker attaches the typed envelope broker so the clockwork_broker_*
// tools surface real data. Same pre-flight contract as WithSessionMgr.
func (a *Adapter) WithBroker(b *broker.Broker) *Adapter {
	a.broker = b
	return a
}

func (a *Adapter) registerCoreTools() {
	a.server.AddTool(mcp.NewTool("clockwork_health",
		mcp.WithDescription(`Liveness probe for the Clockwork MCP server.
Use before any other tool when you need to confirm the service is reachable and discover which opt-in feature flags (sprints, projects, epics, collections) are enabled.
Response shape: data = {status, message, enabled_features[]}.
Example: {}`),
	), a.handleHealth)
	a.registerTaskTools()
	a.registerRunTools()
	a.registerArtifactTools()
	a.registerCommentTools()
	a.registerSettingsTools()
	a.registerSchedulerTools()
	a.registerCheckpointTools()
	a.registerTemplateTools()
	a.registerSubtodoTools()
	a.registerPlanTools()
	a.registerModelTools()
	a.registerSessionTools()
	a.registerBrokerTools()
}

// registerOptInTools checks feature flags and registers tools for enabled layers.
// Called at startup; features must be enabled before the adapter is created.
func (a *Adapter) registerOptInTools() {
	if a.svc.Feature.IsEnabled("sprints") {
		a.registerSprintTools()
	}
	if a.svc.Feature.IsEnabled("projects") {
		a.registerProjectTools()
	}
	if a.svc.Feature.IsEnabled("epics") {
		a.registerEpicTools()
	}
	if a.svc.Feature.IsEnabled("collections") {
		a.registerCollectionTools()
	}
}

func (a *Adapter) handleHealth(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	features := a.svc.Feature.ListEnabled()
	return okResult(map[string]interface{}{
		"status":           "running",
		"message":          "Clockwork Manifold is running",
		"enabled_features": features,
	})
}

// ---- helpers ----------------------------------------------------------------

// reqHasArg reports whether the caller supplied the named argument. Use to
// distinguish "omitted" from "explicit zero / empty string / false" when a
// handler treats the two cases differently (e.g. partial-update PATCH calls).
func reqHasArg(req mcp.CallToolRequest, key string) bool {
	_, ok := req.GetArguments()[key]
	return ok
}

// reqStr extracts a string argument. Non-string values return "" to match
// mcp-go's CallToolRequest.GetString behavior — callers that need
// presence-detection should use reqHasArg.
func reqStr(req mcp.CallToolRequest, key string) string {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// reqInt extracts an integer parameter from an MCP request.
//
// Per CW-20260418-0011 the schema declares numeric params as strings so
// LLM-backed clients that emit `"limit": "50"` (string-encoded numeric)
// aren't rejected at the mcp-go schema boundary. This helper therefore
// accepts float64, int, int64, AND string inputs — callers passing 50
// (number) or "50" (string) must both resolve to 50. int64 handling
// (CW-20260418-0019) covers mcp-go paths that materialize schema-number
// types as int64.
//
// Silent-zero policy: a malformed numeric string (e.g. "abc") returns 0,
// the same value as a missing key. This is intentional. Every numeric
// call site in the adapter already treats 0 as "not set" (see handleTaskList
// where limit==0 rebinds to default 50, and the presence-gated
// `if _, ok := args[key]; ok` pattern in handleTaskUpdate that only applies
// budgets when the key is present). Degrading garbage input to "as if unset"
// matches that convention and avoids punching a new `(int, error)` signature
// through every tool handler. If a future caller needs to distinguish
// "explicit zero" from "malformed" we would add a separate helper rather
// than change this one.
//
// Sentinel values "-1" (unlimited) and "0" (explicit zero) must round-trip
// exactly — see adapter_test.go for the matrix.
func reqInt(req mcp.CallToolRequest, key string) int {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		case string:
			if n == "" {
				return 0
			}
			if i, err := strconv.ParseInt(n, 10, 64); err == nil {
				return int(i)
			}
			// Accept numeric-looking strings with a fractional part too
			// (e.g. "50.0") by falling back to float parse.
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				return int(f)
			}
			return 0
		}
	}
	return 0
}

// reqFloat extracts a float64 parameter from an MCP request. Mirrors reqInt:
// accepts float64, int, int64, and string inputs; returns 0 on missing key or
// unparseable string. See reqInt for the silent-zero rationale.
func reqFloat(req mcp.CallToolRequest, key string) float64 {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case string:
			if n == "" {
				return 0
			}
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				return f
			}
			return 0
		}
	}
	return 0
}

// reqBool extracts a bool argument. Coerces from string ("true"/"false"/"1"/"0"),
// int, int64, and float64 to match mcp-go's CallToolRequest.GetBool behavior.
// Without this coercion, a caller passing "manual": "true" (string) silently
// drops to false even though the MCP server-side tool declares a Boolean
// field — task_update silent-drop regression tracked in CW-20260418-0019.
func reqBool(req mcp.CallToolRequest, key string) bool {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		switch b := v.(type) {
		case bool:
			return b
		case string:
			if parsed, err := strconv.ParseBool(b); err == nil {
				return parsed
			}
		case int:
			return b != 0
		case int64:
			return b != 0
		case float64:
			return b != 0
		}
	}
	return false
}

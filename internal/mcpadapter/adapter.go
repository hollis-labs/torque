package mcpadapter

import (
	"context"
	"strconv"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Adapter wires the service layer to an MCP server.
type Adapter struct {
	svc    *service.Service
	server *server.MCPServer
}

// New creates an Adapter, registers all tools, and returns it.
func New(svc *service.Service) *Adapter {
	s := server.NewMCPServer(
		"Clockwork Manifold",
		"0.1.0",
		server.WithToolCapabilities(true),
	)
	a := &Adapter{svc: svc, server: s}
	a.registerCoreTools()
	a.registerOptInTools()
	return a
}

// Server returns the underlying MCPServer.
func (a *Adapter) Server() *server.MCPServer { return a.server }

func (a *Adapter) registerCoreTools() {
	a.server.AddTool(mcp.NewTool("clockwork_health",
		mcp.WithDescription("Check Clockwork Manifold health status"),
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
// accepts float64, int, AND string inputs — callers passing 50 (number)
// or "50" (string) must both resolve to 50.
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
// accepts float64, int, and string inputs; returns 0 on missing key or
// unparseable string. See reqInt for the silent-zero rationale.
func reqFloat(req mcp.CallToolRequest, key string) float64 {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
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

func reqBool(req mcp.CallToolRequest, key string) bool {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

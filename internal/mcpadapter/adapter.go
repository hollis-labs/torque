package mcpadapter

import (
	"context"
	"encoding/json"
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
	return jsonResult(map[string]interface{}{
		"status":           "running",
		"message":          "Clockwork Manifold is running",
		"enabled_features": features,
	})
}

// ---- helpers ----------------------------------------------------------------

// reqStr extracts a string argument. Non-string values return "" to match
// mcp-go's CallToolRequest.GetString behavior — callers that need
// presence-detection should gate on req.GetArguments()[key] directly.
func reqStr(req mcp.CallToolRequest, key string) string {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// reqInt extracts an int argument. Coerces from float64, int, and numeric
// strings to match mcp-go's CallToolRequest.GetInt behavior. Without this
// coercion, a caller passing "priority": "2" (string) silently drops to 0
// even though the MCP server-side tool declares a Number field.
// See CW-20260418-0019.
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
			if i, err := strconv.Atoi(n); err == nil {
				return i
			}
		}
	}
	return 0
}

// reqBool extracts a bool argument. Coerces from string ("true"/"false"/"1"/"0"),
// int, and float64 to match mcp-go's CallToolRequest.GetBool behavior. Without
// this coercion, a caller passing "manual": "true" (string) silently drops to
// false even though the MCP server-side tool declares a Boolean field — which
// is the task_update silent-drop regression tracked in CW-20260418-0019.
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

func jsonResult(v interface{}) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

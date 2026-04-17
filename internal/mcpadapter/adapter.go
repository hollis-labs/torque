package mcpadapter

import (
	"context"
	"encoding/json"

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

func reqStr(req mcp.CallToolRequest, key string) string {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func reqInt(req mcp.CallToolRequest, key string) int {
	args := req.GetArguments()
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
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

func jsonResult(v interface{}) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

package mcpadapter

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerSchedulerTools() {
	a.server.AddTool(mcp.NewTool("clockwork_scheduler_status",
		mcp.WithDescription("Get scheduler status"),
	), a.handleSchedulerStatus)

	a.server.AddTool(mcp.NewTool("clockwork_scheduler_toggle",
		mcp.WithDescription("Enable or disable the scheduler"),
		mcp.WithBoolean("enabled", mcp.Required(), mcp.Description("Whether to enable the scheduler")),
	), a.handleSchedulerToggle)
}

func (a *Adapter) handleSchedulerStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return okResult(map[string]string{
		"status":  "not_started",
		"message": "Scheduler not yet implemented — see Plan 2",
	})
}

func (a *Adapter) handleSchedulerToggle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return okResult(map[string]string{
		"status":  "not_started",
		"message": "Scheduler not yet implemented — see Plan 2",
	})
}

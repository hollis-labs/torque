package mcpadapter

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

func (a *Adapter) registerSchedulerTools() {
	a.server.AddTool(mcp.NewTool("clockwork_scheduler_status",
		mcp.WithDescription(`Report scheduler liveness (currently stubbed — Plan 2 / scheduler hardening will implement).
Use to check if auto-dispatch is running. Today always returns status=not_started.
Response shape: data = {status, message}.
Example: {}`),
	), a.handleSchedulerStatus)

	a.server.AddTool(mcp.NewTool("clockwork_scheduler_toggle",
		mcp.WithDescription(`Enable/disable the task scheduler (currently stubbed — Plan 2 / scheduler hardening will implement).
Use to pause auto-dispatch; today a no-op returning not_started. Feature-flag registration uses clockwork_settings_save.
Response shape: data = {status, message}.
Example: {"enabled":false}`),
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

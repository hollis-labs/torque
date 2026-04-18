package mcpadapter

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// Session-scoped toggle — the enabled flag lives on the scheduler instance and
// resets to cfg.Scheduler.Enabled whenever serve restarts. We document this in
// the tool description so agents know not to treat a toggle as durable state;
// persistent disable belongs in config, not a runtime toggle.
const schedulerToggleDescription = "Enable or disable the scheduler. " +
	"Session-scoped: reset on serve restart (scheduler re-initializes from " +
	"config). enabled=true resumes tick-driven dispatch; enabled=false pauses " +
	"picking new tasks (in-flight workers continue). Idempotent — toggling to " +
	"the current state is a no-op and returns the same status."

func (a *Adapter) registerSchedulerTools() {
	a.server.AddTool(mcp.NewTool("clockwork_scheduler_status",
		mcp.WithDescription("Get scheduler status (enabled flag, worker counts, queue depth, cost, subscribers, stale-heartbeat threshold). Same shape as GET /api/v1/scheduler/status."),
	), a.handleSchedulerStatus)

	a.server.AddTool(mcp.NewTool("clockwork_scheduler_toggle",
		mcp.WithDescription(schedulerToggleDescription),
		mcp.WithBoolean("enabled", mcp.Required(), mcp.Description("true to enable dispatch, false to pause. Session-scoped — no effect on config.")),
	), a.handleSchedulerToggle)
}

func (a *Adapter) handleSchedulerStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if a.sched == nil {
		return mcp.NewToolResultError("scheduler not running"), nil
	}
	return jsonResult(a.sched.Status())
}

// handleSchedulerToggle sets the scheduler's enabled flag to the requested
// value and returns the resulting Status. Idempotency is a property of
// Scheduler.SetEnabled — it writes the flag under s.mu.Lock regardless of the
// prior value, so toggling to the current state is a cheap no-op that still
// returns a consistent status snapshot. Unlike the HTTP toggle (which always
// flips), the MCP toggle takes an explicit enabled arg so agent callers don't
// have to read-modify-write across two tool calls.
func (a *Adapter) handleSchedulerToggle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if a.sched == nil {
		return mcp.NewToolResultError("scheduler not running"), nil
	}
	enabled := reqBool(req, "enabled")
	a.sched.SetEnabled(enabled)
	return jsonResult(a.sched.Status())
}

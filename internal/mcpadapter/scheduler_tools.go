package mcpadapter

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// Session-scoped toggle — the enabled flag lives on the scheduler instance and
// resets to cfg.Scheduler.Enabled whenever serve restarts. We document this in
// the tool description so agents know not to treat a toggle as durable state;
// persistent disable belongs in config, not a runtime toggle.
const schedulerStatusDescription = `Get scheduler status: enabled flag, worker counts, queue depth, cost, subscribers, stale-heartbeat threshold.
Use to confirm auto-dispatch is live before creating manual=false tasks, or to monitor worker/queue load. Same shape as GET /api/v1/scheduler/status.
Response shape: data = {enabled, max_workers, active_workers, queue_depth, total_cost, subscribers, stale_heartbeat_seconds}.
Example: {}`

const schedulerToggleDescription = `Enable or disable the task scheduler.
Use to pause auto-dispatch for a session (e.g. before a mass-flip or noisy debug) and resume afterwards. Session-scoped: reset on serve restart (scheduler re-initializes from config); persistent disable belongs in clockwork_settings_save, not here.
Idempotent — toggling to the current state is a no-op and returns the same status. Unlike the HTTP toggle (which always flips), this tool takes an explicit enabled arg so agents don't have to read-modify-write across two tool calls.
Response shape: data = {enabled, max_workers, active_workers, queue_depth, total_cost, subscribers, stale_heartbeat_seconds}.
Example: {"enabled":"false"}`

func (a *Adapter) registerSchedulerTools() {
	a.addTool(mcp.NewTool("clockwork_scheduler_status",
		mcp.WithDescription(schedulerStatusDescription),
	), a.handleSchedulerStatus)

	a.addTool(mcp.NewTool("clockwork_scheduler_toggle",
		mcp.WithDescription(schedulerToggleDescription),
		mcp.WithString("enabled", mcp.Required(), mcp.Description("true to enable dispatch, false to pause. Session-scoped — no effect on config. (boolean, accepts \"true\"/\"false\" strings).")),
	), a.handleSchedulerToggle)
}

func (a *Adapter) handleSchedulerStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if a.sched == nil {
		return errResult(ErrCodeDomain, "scheduler not running in this process (stdio mcp subcommand)", "")
	}
	return okResult(a.sched.Status())
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
		return errResult(ErrCodeDomain, "scheduler not running in this process (stdio mcp subcommand)", "")
	}
	enabled := reqBool(req, "enabled")
	a.sched.SetEnabled(enabled)
	return okResult(a.sched.Status())
}

package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/mark3labs/mcp-go/mcp"
)

// Session-scoped toggle — the enabled flag lives on the scheduler instance and
// resets to cfg.Scheduler.Enabled whenever serve restarts. We document this in
// the tool description so agents know not to treat a toggle as durable state;
// persistent disable belongs in config, not a runtime toggle.
const schedulerStatusDescription = `Get scheduler status: enabled flag, worker counts, queue depth, cost, subscribers, stale-heartbeat threshold.
Use to confirm auto-dispatch is live before creating manual=false tasks, or to monitor worker/queue load. Same shape as GET /api/v1/scheduler/status. In stdio MCP mode this tool falls back to a read-only proxy against the local serve process at http://127.0.0.1:$TORQUE_HTTP_PORT/api/v1/scheduler/status, so clients can still tell whether auto-dispatch is live.
Response shape: data = {enabled, max_workers, active_workers, queue_depth, total_cost, subscribers, stale_heartbeat_seconds}.
Example: {}`

const schedulerToggleDescription = `Enable or disable the task scheduler.
Use to pause auto-dispatch for a session (e.g. before a mass-flip or noisy debug) and resume afterwards. Session-scoped: reset on serve restart (scheduler re-initializes from config); persistent disable belongs in torque_settings_save, not here. This requires an in-process scheduler; stdio MCP clients should use torque_scheduler_status for read-only serve-process state.
Idempotent — toggling to the current state is a no-op and returns the same status. Unlike the HTTP toggle (which always flips), this tool takes an explicit enabled arg so agents don't have to read-modify-write across two tool calls.
Response shape: data = {enabled, max_workers, active_workers, queue_depth, total_cost, subscribers, stale_heartbeat_seconds}.
Example: {"enabled":"false"}`

func (a *Adapter) registerSchedulerTools() {
	a.addTool(mcp.NewTool("torque_scheduler_status",
		mcp.WithDescription(schedulerStatusDescription),
	), a.handleSchedulerStatus)

	a.addTool(mcp.NewTool("torque_scheduler_toggle",
		mcp.WithDescription(schedulerToggleDescription),
		mcp.WithString("enabled", mcp.Required(), mcp.Description("true to enable dispatch, false to pause. Session-scoped — no effect on config. (boolean, accepts \"true\"/\"false\" strings).")),
	), a.handleSchedulerToggle)
}

func (a *Adapter) handleSchedulerStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if a.sched == nil {
		status, err := proxySchedulerStatus(ctx)
		if err != nil {
			return errResult(ErrCodeDomain, err.Error(), "")
		}
		return okResult(status)
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
		return errResult(ErrCodeDomain, "scheduler toggle requires the serve process scheduler; stdio mcp is read-only here, use torque_scheduler_status to inspect state", "")
	}
	enabled := reqBool(req, "enabled")
	a.sched.SetEnabled(enabled)
	return okResult(a.sched.Status())
}

func proxySchedulerStatus(ctx context.Context) (*scheduler.SchedulerStatus, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config for scheduler status proxy: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/scheduler/status", cfg.HTTPPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build scheduler status proxy request: %w", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scheduler status unavailable: no in-process scheduler and proxy to %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		var env struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &env) == nil && env.Error != "" {
			msg = env.Error
		}
		return nil, fmt.Errorf("scheduler status unavailable: serve proxy %s returned %s: %s", url, resp.Status, msg)
	}

	var status scheduler.SchedulerStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode scheduler status proxy response from %s: %w", url, err)
	}
	return &status, nil
}

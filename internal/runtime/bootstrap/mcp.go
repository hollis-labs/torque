package bootstrap

import (
	"log/slog"
	"net/http"

	httptransport "github.com/hollis-labs/go-mcp/transport/http"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/service"
)

// StdioMCPAdapter is the adapter `torque mcp` serves on stdio. It owns no
// scheduler: torque_scheduler_status proxies the daemon's, and
// torque_scheduler_toggle reports itself unavailable.
func StdioMCPAdapter(svc *service.Service, sessions *agent.Manager, logger *slog.Logger) *mcpadapter.Adapter {
	a := mcpadapter.New(svc, nil).WithSessions(sessions)
	if logger != nil {
		a = a.WithLogger(logger)
	}
	return a
}

// DaemonMCPHandler serves the same tool surface over MCP Streamable HTTP
// from the daemon, for clients that must not open the database: an agent
// under ProtectedPaths reaches it through `torque mcp --remote`
// (CW-20261001-0199). It is wired to the daemon's own scheduler, session
// manager and steering registries, so sessions it starts are the daemon's
// and the scheduler tools act on the running scheduler. The transport is
// stateless (go-mcp httptransport), like the per-session loopback servers.
// The caller mounts it behind the HTTP API's auth (httpserver.WithMCP).
func DaemonMCPHandler(svc *service.Service, sched *scheduler.Scheduler, sessions *agent.Manager, polls *steering.PollRegistry, reminders *steering.ReminderRegistry, logger *slog.Logger) http.Handler {
	a := mcpadapter.New(svc, sched).
		WithSessions(sessions).
		WithPollRegistry(polls).
		WithReminderRegistry(reminders)
	if logger != nil {
		a = a.WithLogger(logger)
	}
	return httptransport.NewHandler(a.Server(), httptransport.HandlerOptions{})
}

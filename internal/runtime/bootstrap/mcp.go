package bootstrap

import (
	"log"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	httptransport "github.com/hollis-labs/go-mcp/transport/http"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/mcpbridge"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
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
// (CW-20261001-0199). It is wired to the daemon's own scheduler and session
// manager, so sessions it starts are the daemon's and the scheduler tools
// act on the running scheduler. The transport is stateless (go-mcp
// httptransport), like the per-session loopback servers. The caller mounts
// it behind the HTTP API's auth (httpserver.WithMCP).
//
// It takes no poll or reminder registry, as stdio `torque mcp` has none.
// torque_inbox_poll opts a recipient out of the steering bridge's injection
// in the daemon's registry, and the envelopes it then expects to pull come
// from a broker this adapter is not wired to (torque_broker_inbox answers
// "broker not wired"). With the live registry wired, a client could enable
// polling that nothing drains and silence its own deliveries. Without a
// registry torque_inbox_poll answers that polling is not available, as it
// does over stdio.
func DaemonMCPHandler(svc *service.Service, sched *scheduler.Scheduler, sessions *agent.Manager, logger *slog.Logger) http.Handler {
	a := mcpadapter.New(svc, sched).WithSessions(sessions)
	if logger != nil {
		a = a.WithLogger(logger)
	}
	return httptransport.NewHandler(a.Server(), httptransport.HandlerOptions{})
}

// PlantRemoteMCP points the `torque mcp` that mux starts for an agent at
// the daemon's /mcp while protection is on (CW-20261001-0199). Under
// ProtectedPaths that child cannot open main.db; with mcpbridge.RemoteEnv in
// the planted mux entry's env (mux passes its env to the servers it
// starts) it relays to the daemon instead, so the agent keeps the torque_*
// tools without write access to Torque's state.
//
// It plants nothing without protection or a mux, and nothing when the API
// requires a token: agents never receive TORQUE_API_TOKEN (FilterEnv strips
// it), so the relay could only be refused.
func PlantRemoteMCP(deps *agent.Dependencies, listen net.Addr, tokenRequired bool) bool {
	if deps == nil || len(deps.ProtectedPaths) == 0 || deps.MuxCommand == "" {
		return false
	}
	if tokenRequired {
		log.Printf("[sandbox] the API requires TORQUE_API_TOKEN, which agents do not get, so mux's torque server is not pointed at the daemon's /mcp (CW-20261001-0199)")
		return false
	}
	url := DaemonMCPURL(listen)
	if url == "" {
		return false
	}
	deps.MuxOmitsTorque = false
	deps.MuxEnv = append(deps.MuxEnv, mcpbridge.RemoteEnv+"="+url)
	log.Printf("[sandbox] mux's torque server relays to %s (%s) while Torque's state is write-protected", url, mcpbridge.RemoteEnv)
	return true
}

// DaemonMCPURL is the /mcp URL an agent on this host reaches the daemon at:
// the loopback address when listening on loopback, 127.0.0.1 for every interface,
// else the address it listens on. "" for a non-TCP listener.
func DaemonMCPURL(listen net.Addr) string {
	tcp, ok := listen.(*net.TCPAddr)
	if !ok || tcp.Port == 0 {
		return ""
	}
	host := "127.0.0.1"
	if tcp.IP != nil && !tcp.IP.IsUnspecified() {
		host = tcp.IP.String()
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(tcp.Port)) + "/mcp"
}

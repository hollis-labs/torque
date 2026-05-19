package bootstrap

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/server"
)

// loopbackHandle owns the per-task MCP loopback HTTP listener and its
// serving goroutine. The agent's per-task .mcp.json points at the
// listener's URL; every tool call resolves to the closure-bound mcpadapter
// (no task_id parameter required or accepted).
//
// Lifecycle: bootstrapLoopbackBuilder constructs the listener, wraps the
// loopback adapter's MCPServer in mcp-go's StreamableHTTPServer (used as
// an http.Handler), and starts a serve goroutine on a stdlib http.Server
// we own directly. Shutdown drives that http.Server's graceful-close path;
// mcp-go's own Shutdown is not used because we run the listener ourselves.
//
// Forked from internal/runtime/cliexec/loopback.go. Lives in the bootstrap
// package (not the agent package) so the agent package's import graph stays
// free of mcpadapter — that direction would close the planstart → agent →
// mcpadapter → planstart cycle introduced by the Boot unification.
type loopbackHandle struct {
	url      string
	httpSrv  *http.Server
	listener net.Listener
	serveErr chan error
}

// URL returns the address planted into .mcp.json. Satisfies agent.LoopbackHandle.
func (h *loopbackHandle) URL() string {
	if h == nil {
		return ""
	}
	return h.url
}

// Shutdown gracefully closes the HTTP server (which releases the listener),
// then drains the serve goroutine. Bounded by the supplied context's
// deadline. Satisfies agent.LoopbackHandle.
func (h *loopbackHandle) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	shutdownErr := h.httpSrv.Shutdown(ctx)
	select {
	case err := <-h.serveErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("loopback serve: %w", err)
		}
	case <-ctx.Done():
		_ = h.listener.Close()
		<-h.serveErr
		return fmt.Errorf("loopback shutdown timed out: %w", ctx.Err())
	}
	return shutdownErr
}

// loopbackBuilder constructs the agent.LoopbackBuilder factory. svc is the
// service handle the loopback adapter needs for closure-bound task
// operations; nil returns a nil builder (which agent.setupLoopback then
// short-circuits to (nil, nil) — "no loopback") to match the legacy
// cliexec(svc=nil) test path.
//
// `sessionsRef` is a late-bound accessor for the agent.Manager that the
// orchestrator-class loopback adapter wires via WithSessions. The accessor
// pattern resolves a construction-order cycle: agent.Dependencies.Loopback
// is set BEFORE agent.NewManager creates Sessions; the closure reads
// deps.Sessions at handle-construction time (later, when sessions is set).
// May be nil for callers that don't need orchestrator support; orchestrator-
// role loopbacks then fall back to NewLoopback (restricted subset) and the
// orchestrator will self-block per CW-20260509-0018.
//
// pollReg is the opt-in inbox-poll registry (CW-20260518-0042). It is
// attached to the orchestrator-class loopback adapter (via WithPollRegistry)
// so the torque_inbox_poll tool shares opt-in state with the steering
// bridge. nil disables the tool's opt-in side (it reports polling as
// unavailable). Worker-class loopbacks never carry the tool — the
// restricted NewLoopback subset omits the broker tools entirely.
func loopbackBuilder(svc *service.Service, sessionsRef func() *agent.Manager, pollReg *steering.PollRegistry) agent.LoopbackBuilder {
	if svc == nil {
		return nil
	}
	return func(taskID, role string) (agent.LoopbackHandle, error) {
		// Per CW-20260509-0018: orchestrator-class roles drive plan walks
		// and need cross-task tools (torque_plan_get, torque_task_*,
		// torque_session_get, etc.). The legacy NewLoopback(taskID)
		// adapter exposes only the self-task subset (artifact_create,
		// comment_add, task_summary/blocked/review, task_subtodo_*) which
		// is correct for kind=agent worker tasks but insufficient for the
		// orchestrator. Dispatch on role:
		//   - orchestrator/planner/reviewer-end-agent → full surface
		//     (mcpadapter.New + WithSessions). The orchestrator template
		//     already calls these tools by their full-surface names.
		//   - everything else (including empty role from kind=agent
		//     workers) → restricted self-task subset (legacy behavior).
		var loopback *mcpadapter.Adapter
		if isOrchestratorClassRole(role) && sessionsRef != nil {
			// Full torque tool surface — NOT pinned to taskID. Unlike
			// NewLoopback, mcpadapter.New requires explicit task IDs on
			// every call (torque_task_get(id="..."), etc.). That
			// matches the orchestrator's actual usage pattern: it acts on
			// many task IDs (the plan task, planner sub-task, child
			// tasks), not just its own. The orchestrator template already
			// uses the explicit-id forms verbatim. Sessions wired so
			// torque_session_* + torque_plan_start work.
			//
			// WithPollRegistry shares the opt-in inbox-poll state
			// (CW-20260518-0042) with the in-process steering bridge so an
			// orchestrator — the archetypal "actively communicating" agent —
			// can opt into pulling its inbox via torque_inbox_poll.
			loopback = mcpadapter.New(svc, nil).
				WithSessions(sessionsRef()).
				WithPollRegistry(pollReg)
		} else {
			// A worker-class loopback (the restricted self-task subset) is
			// hard-bound to exactly one task: every tool call resolves to
			// that task with no task_id parameter. Without a bound task
			// there is nothing to resolve against — mcpadapter.NewLoopback
			// panics on an empty taskID by contract ("a loopback adapter
			// without a bound task is a programmer error"). Guard it here
			// so a task-less worker session (a bare torque_session_launch
			// or HTTP /sessions/launch with task_id omitted) fails Boot
			// with a clear error instead of panicking the request handler.
			if taskID == "" {
				return nil, fmt.Errorf("agent: a worker session requires a task_id — the loopback adapter binds to exactly one task (role=%q)", role)
			}
			loopback = mcpadapter.NewLoopback(svc, taskID)
		}

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("listen 127.0.0.1: %w", err)
		}
		tcpAddr, ok := ln.Addr().(*net.TCPAddr)
		if !ok {
			_ = ln.Close()
			return nil, fmt.Errorf("loopback listener returned non-TCP addr: %T", ln.Addr())
		}

		streamableHandler := server.NewStreamableHTTPServer(loopback.Server())

		httpSrv := &http.Server{
			Handler:           streamableHandler,
			ReadHeaderTimeout: 5 * time.Second,
		}

		h := &loopbackHandle{
			url:      fmt.Sprintf("http://127.0.0.1:%d/mcp", tcpAddr.Port),
			httpSrv:  httpSrv,
			listener: ln,
			serveErr: make(chan error, 1),
		}

		go func() {
			h.serveErr <- httpSrv.Serve(ln)
		}()

		return h, nil
	}
}

// isOrchestratorClassRole reports whether `role` (from agent.Options.Role,
// canonically the SessionMetaRoleValue stamped by planstart for
// orchestrators or the AgentProfile name for scheduler-dispatched
// kind=internal tasks) is one of the documented orchestrator-class roles
// that drive plan walks and need the full cross-task MCP tool surface
// (CW-20260509-0018).
//
// Reserved profile names: callers MUST NOT use these as kind=agent worker
// profiles, since doing so would silently widen the worker's MCP surface
// to the cross-task set. V1 trusts the operator to reserve the names; V2
// can add stricter validation at task-create time.
func isOrchestratorClassRole(role string) bool {
	switch role {
	case "orchestrator",
		"planner",
		"reviewer-end-agent":
		return true
	}
	return false
}

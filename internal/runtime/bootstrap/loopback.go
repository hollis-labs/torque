package bootstrap

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/mcpadapter"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/agent"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
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
// operations; nil yields a builder that returns (nil, nil) so test code
// paths see "no loopback" identical to the legacy cliexec(svc=nil) shape.
func loopbackBuilder(svc *service.Service) agent.LoopbackBuilder {
	if svc == nil {
		return nil
	}
	return func(taskID string) (agent.LoopbackHandle, error) {
		loopback := mcpadapter.NewLoopback(svc, taskID)

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

package cliexec

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/mcpadapter"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/mark3labs/mcp-go/server"
)

// loopbackHandle owns the per-task MCP loopback HTTP listener and its serving
// goroutine. The agent's per-task .mcp.json points at the listener's URL;
// every tool call resolves to the closure-bound mcpadapter (no task_id
// parameter required or accepted).
//
// Lifecycle: setupLoopback creates the listener, wraps the loopback adapter's
// MCPServer in mcp-go's StreamableHTTPServer (used as an http.Handler), and
// starts a serve goroutine on a stdlib http.Server we own directly. Shutdown
// drives that http.Server's graceful-close path; mcp-go's own Shutdown is
// not used because we run the listener ourselves (so mcp-go has no http.Server
// reference to drive).
type loopbackHandle struct {
	port     int
	url      string
	httpSrv  *http.Server
	listener net.Listener
	serveErr chan error
}

// setupLoopback constructs the loopback adapter (closure-bound to taskID),
// binds 127.0.0.1:0 (random unprivileged port), wraps the adapter's MCPServer
// in StreamableHTTPServer, and serves in a goroutine. Returns the handle on
// success; caller must Shutdown() before run completion to avoid goroutine
// leaks.
//
// If svc is nil, returns (nil, nil) — disables the loopback (test path).
// Production callsites should always supply a non-nil service.
func setupLoopback(svc *service.Service, taskID string) (*loopbackHandle, error) {
	if svc == nil {
		return nil, nil
	}

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
		port:     tcpAddr.Port,
		url:      fmt.Sprintf("http://127.0.0.1:%d/mcp", tcpAddr.Port),
		httpSrv:  httpSrv,
		listener: ln,
		serveErr: make(chan error, 1),
	}

	go func() {
		// http.Serve returns http.ErrServerClosed on graceful Shutdown;
		// callers treat that as the normal-exit signal.
		h.serveErr <- httpSrv.Serve(ln)
	}()

	return h, nil
}

// Shutdown gracefully closes the HTTP server (which releases the listener),
// then drains the serve goroutine. Bounded by the supplied context's deadline
// — typically 2s in production. Safe to call on a nil handle (no-op).
func (h *loopbackHandle) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	shutdownErr := h.httpSrv.Shutdown(ctx)
	// Drain the serve goroutine. http.ErrServerClosed is the normal-exit
	// signal; anything else surfaces as the returned error.
	select {
	case err := <-h.serveErr:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("loopback serve: %w", err)
		}
	case <-ctx.Done():
		// Bounded wait elapsed; force-close the listener to unblock Serve.
		_ = h.listener.Close()
		<-h.serveErr // serve goroutine will return now
		return fmt.Errorf("loopback shutdown timed out: %w", ctx.Err())
	}
	return shutdownErr
}

// Port returns the bound TCP port. Used to populate the boot dir's .mcp.json.
func (h *loopbackHandle) Port() int {
	if h == nil {
		return 0
	}
	return h.port
}

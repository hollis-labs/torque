package agent

import (
	"context"
)

// LoopbackHandle is the abstract per-task MCP loopback owned by Boot.
// The concrete impl (HTTP listener + MCPServer wiring) lives in
// bootstrap/loopback.go to keep the agent package free of any mcpadapter
// import — that direction would close the planstart → agent → mcpadapter →
// planstart import cycle introduced by the Boot unification (mcpadapter's
// plan_tools.go imports planstart for clockwork_plan_start).
//
// Boot calls Dependencies.Loopback(taskID) when non-nil to materialize a
// handle. Caller (Boot internals + Manager teardown) drives Shutdown when
// the session terminates.
type LoopbackHandle interface {
	// URL returns http://127.0.0.1:<port>/mcp — the value planted into
	// the boot dir's .mcp.json so the spawned agent can resolve loopback
	// tools without a task_id parameter.
	URL() string

	// Shutdown gracefully closes the HTTP server + drains the serve
	// goroutine. Safe on nil handles (no-op).
	Shutdown(ctx context.Context) error
}

// LoopbackBuilder is the factory injected into Dependencies. nil = loopback
// disabled (matches the legacy cliexec.New(svc=nil) test path); non-nil
// constructs a fresh per-task handle for each Boot call.
type LoopbackBuilder func(taskID string) (LoopbackHandle, error)

// setupLoopback materializes a handle when the deps factory is non-nil.
// Returns (nil, nil) when nil — the test path.
func setupLoopback(builder LoopbackBuilder, taskID string) (LoopbackHandle, error) {
	if builder == nil {
		return nil, nil
	}
	return builder(taskID)
}

// shutdownLoopbackHandle is the package-internal teardown helper used by
// Boot's rollback paths and Manager's per-session teardown. Bounded by a
// 2-second deadline (matches the legacy cliexec convention).
func shutdownLoopbackHandle(h LoopbackHandle) {
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), loopbackShutdownGrace)
	defer cancel()
	_ = h.Shutdown(ctx)
}

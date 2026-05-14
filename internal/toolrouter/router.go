// Package toolrouter routes MCP tool calls through permission + sandbox pipeline.
package toolrouter

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hollis-labs/torque/internal/permission"
	"github.com/hollis-labs/torque/internal/tool"
)

// ToolCall represents a single tool invocation request.
type ToolCall struct {
	SessionID  string
	TaskID     string
	ToolName   string
	Input      map[string]any
	WorkingDir string
}

// ApprovalHandler is called when a tool call requires user approval.
type ApprovalHandler func(req *permission.ApprovalRequest)

// Router connects tool calls to permission checks and tool execution.
type Router struct {
	registry        *tool.Registry
	permEngine      *permission.Engine
	approvalHandler ApprovalHandler
}

// New creates a new Router with the given registry and permission engine.
func New(registry *tool.Registry, permEngine *permission.Engine) *Router {
	return &Router{
		registry:   registry,
		permEngine: permEngine,
	}
}

// SetApprovalHandler sets the handler called when a tool call requires approval.
func (r *Router) SetApprovalHandler(handler ApprovalHandler) {
	r.approvalHandler = handler
}

// SetApprovalTimeout delegates to permEngine.SetApprovalTimeout.
func (r *Router) SetApprovalTimeout(d time.Duration) {
	r.permEngine.SetApprovalTimeout(d)
}

// RespondToApproval delegates to permEngine.Respond.
func (r *Router) RespondToApproval(requestID string, decision permission.Decision, scope permission.Scope, sessionID string) bool {
	return r.permEngine.Respond(requestID, decision, scope, sessionID)
}

// Route looks up the tool, validates input, checks permissions, and executes.
func (r *Router) Route(ctx context.Context, call ToolCall) (*tool.ToolResult, error) {
	// 1. Look up tool.
	t := r.registry.Get(call.ToolName)
	if t == nil {
		return nil, fmt.Errorf("tool %q not found", call.ToolName)
	}

	// 2. Validate input.
	if err := t.ValidateInput(call.Input); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	// 3. Build ToolMeta.
	meta := permission.ToolMeta{
		IsReadOnly:    t.IsReadOnly(call.Input),
		IsDestructive: t.IsDestructive(call.Input),
	}

	// 4. Permission check.
	result := r.permEngine.Check(ctx, call.SessionID, call.ToolName, call.Input, meta)

	// 5. Route based on decision.
	switch result.Decision {
	case permission.DecisionAllow:
		return r.executeTool(ctx, t, call)
	case permission.DecisionDeny:
		return nil, fmt.Errorf("tool %q denied: %s", call.ToolName, result.Reason)
	case permission.DecisionAsk:
		return r.handleAsk(ctx, t, call, result.Reason)
	default:
		return nil, fmt.Errorf("unknown permission decision %q for tool %q", result.Decision, call.ToolName)
	}
}

// handleAsk requests approval from the user and waits for a response.
func (r *Router) handleAsk(ctx context.Context, t tool.Tool, call ToolCall, reason string) (*tool.ToolResult, error) {
	req := r.permEngine.RequestApproval(call.SessionID, call.ToolName, call.Input, reason)

	if r.approvalHandler != nil {
		r.approvalHandler(req)
	} else {
		log.Printf("toolrouter: approval required for tool %q (request %s): %s", call.ToolName, req.ID, reason)
	}

	resp := r.permEngine.WaitForApproval(ctx, req)

	if resp.Decision == permission.DecisionAllow {
		return r.executeTool(ctx, t, call)
	}

	if resp.TimedOut {
		return nil, fmt.Errorf("tool %q denied: timed out waiting for approval", call.ToolName)
	}
	return nil, fmt.Errorf("tool %q denied: approval rejected", call.ToolName)
}

// executeTool builds the execution context and calls the tool.
func (r *Router) executeTool(ctx context.Context, t tool.Tool, call ToolCall) (*tool.ToolResult, error) {
	execCtx := tool.ExecutionContext{
		SessionID:  call.SessionID,
		TaskID:     call.TaskID,
		WorkingDir: call.WorkingDir,
	}
	return t.Call(ctx, call.Input, execCtx)
}

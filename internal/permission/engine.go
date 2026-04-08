package permission

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ToolMeta holds metadata about a tool being checked.
type ToolMeta struct {
	IsReadOnly    bool
	IsDestructive bool
}

// ApprovalRequest represents a pending approval request.
type ApprovalRequest struct {
	ID        string
	SessionID string
	ToolName  string
	Input     map[string]any
	Reason    string
	CreatedAt time.Time
	Response  chan ApprovalResponse
}

// ApprovalResponse is the response to an approval request.
type ApprovalResponse struct {
	Decision  Decision
	Scope     Scope
	TimedOut  bool
}

// Engine manages permission checking with rules, modes, and session grants.
type Engine struct {
	mu               sync.RWMutex
	mode             Mode
	rules            *RuleSet
	sessionGrants    map[string]map[string]Decision
	pendingApprovals sync.Map
	approvalTimeout  time.Duration
}

// NewEngine creates a new Engine with the given mode and rules.
func NewEngine(mode Mode, rules *RuleSet) *Engine {
	if rules == nil {
		rules = &RuleSet{}
	}
	return &Engine{
		mode:            mode,
		rules:           rules,
		sessionGrants:   make(map[string]map[string]Decision),
		approvalTimeout: 60 * time.Second,
	}
}

// SetMode sets the engine mode in a thread-safe way.
func (e *Engine) SetMode(mode Mode) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode = mode
}

// Mode returns the current engine mode in a thread-safe way.
func (e *Engine) Mode() Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mode
}

// SetRules updates the engine's ruleset in a thread-safe way.
func (e *Engine) SetRules(rules *RuleSet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = rules
}

// SetApprovalTimeout sets the timeout for approval requests.
func (e *Engine) SetApprovalTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.approvalTimeout = d
}

// Check evaluates whether a tool call should be allowed, denied, or requires approval.
func (e *Engine) Check(ctx context.Context, sessionID, toolName string, input map[string]any, meta ToolMeta) CheckResult {
	e.mu.RLock()
	mode := e.mode
	rules := e.rules
	// Copy session grants for this session.
	sessionDecision, hasSessionGrant := Decision(""), false
	if grants, ok := e.sessionGrants[sessionID]; ok {
		if d, ok := grants[toolName]; ok {
			sessionDecision = d
			hasSessionGrant = true
		}
	}
	e.mu.RUnlock()

	// Yolo mode: allow everything.
	if mode == ModeYolo {
		return CheckResult{Decision: DecisionAllow, Reason: "yolo mode"}
	}

	// Plan mode: deny writes, allow reads.
	if mode == ModePlan {
		if meta.IsReadOnly {
			return CheckResult{Decision: DecisionAllow, Reason: "plan mode: read-only allowed"}
		}
		return CheckResult{Decision: DecisionDeny, Reason: "plan mode: writes denied"}
	}

	// Session grants take priority over rules.
	if hasSessionGrant {
		return CheckResult{Decision: sessionDecision, Reason: "session grant"}
	}

	// Evaluate rules.
	if rules != nil {
		if result := rules.Evaluate(toolName, input); result != nil {
			return *result
		}
	}

	// Fall back to default behavior for this mode.
	return e.defaultDecision(mode, toolName, meta)
}

// defaultDecision returns the default CheckResult for a tool when no rules match.
func (e *Engine) defaultDecision(mode Mode, toolName string, meta ToolMeta) CheckResult {
	switch mode {
	case ModeAcceptEdits:
		if isFileEditTool(toolName) {
			return CheckResult{Decision: DecisionAllow, Reason: "accept-edits mode: file edit allowed"}
		}
		if meta.IsDestructive {
			return CheckResult{Decision: DecisionAsk, Reason: "accept-edits mode: destructive requires approval"}
		}
		if !meta.IsReadOnly && !isFileEditTool(toolName) {
			return CheckResult{Decision: DecisionAsk, Reason: "accept-edits mode: non-read-only non-edit requires approval"}
		}
		return CheckResult{Decision: DecisionAllow, Reason: "accept-edits mode: read-only allowed"}
	default: // ModeDefault
		if meta.IsDestructive {
			return CheckResult{Decision: DecisionAsk, Reason: "default mode: destructive requires approval"}
		}
		if meta.IsReadOnly {
			return CheckResult{Decision: DecisionAllow, Reason: "default mode: read-only allowed"}
		}
		return CheckResult{Decision: DecisionAllow, Reason: "default mode: non-destructive allowed"}
	}
}

// RequestApproval creates a new pending approval request and stores it.
func (e *Engine) RequestApproval(sessionID, toolName string, input map[string]any, reason string) *ApprovalRequest {
	req := &ApprovalRequest{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		ToolName:  toolName,
		Input:     input,
		Reason:    reason,
		CreatedAt: time.Now(),
		Response:  make(chan ApprovalResponse, 1),
	}
	e.pendingApprovals.Store(req.ID, req)
	return req
}

// WaitForApproval blocks until an approval response is received, the timer expires, or the context is cancelled.
func (e *Engine) WaitForApproval(ctx context.Context, req *ApprovalRequest) ApprovalResponse {
	e.mu.RLock()
	timeout := e.approvalTimeout
	e.mu.RUnlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-req.Response:
		e.pendingApprovals.Delete(req.ID)
		return resp
	case <-timer.C:
		e.pendingApprovals.Delete(req.ID)
		return ApprovalResponse{Decision: DecisionDeny, TimedOut: true}
	case <-ctx.Done():
		e.pendingApprovals.Delete(req.ID)
		return ApprovalResponse{Decision: DecisionDeny, TimedOut: false}
	}
}

// Respond sends a response to a pending approval request.
// Returns false if the request is not found, the session doesn't match, or it was already responded to.
func (e *Engine) Respond(requestID string, decision Decision, scope Scope, sessionID string) bool {
	val, ok := e.pendingApprovals.Load(requestID)
	if !ok {
		return false
	}
	req, ok := val.(*ApprovalRequest)
	if !ok {
		return false
	}
	if req.SessionID != sessionID {
		return false
	}

	// Record session grant if scope is session and decision is allow.
	if scope == ScopeSession && decision == DecisionAllow {
		e.mu.Lock()
		if e.sessionGrants[sessionID] == nil {
			e.sessionGrants[sessionID] = make(map[string]Decision)
		}
		e.sessionGrants[sessionID][req.ToolName] = decision
		e.mu.Unlock()
	}

	// Try to send the response; if channel is full the request was already responded to.
	select {
	case req.Response <- ApprovalResponse{Decision: decision, Scope: scope}:
		return true
	default:
		return false
	}
}

// ClearSessionGrants removes all session grants for the given session.
func (e *Engine) ClearSessionGrants(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.sessionGrants, sessionID)
}

// isFileEditTool returns true if the tool name is a known file editing tool.
func isFileEditTool(name string) bool {
	switch name {
	case "mcp__dev__edit", "mcp__dev__write", "dev_edit", "dev_write":
		return true
	}
	return false
}

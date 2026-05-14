// Package toolbroker is the executor-facing facade that composes
// go-toolbroker (intent-aware MCP tool selection) with torque's local
// permission engine + tool registry (internal/toolrouter, internal/permission,
// internal/tool). It is the concrete type threaded through bootstrap.Executors
// into both cliexec and executor-api, replacing the prior `interface{}` /
// "nil until Plan 4" placeholder (CW-20260503-0015 / Plan 4).
//
// The two halves serve different jobs:
//
//   - go-toolbroker (broker.LocalBroker) — selects which tools an agent should
//     see for a given intent. Output is a curated tool list shipped to the
//     model so the per-turn payload stays small and on-topic.
//   - internal/toolrouter.Router — enforces permissions on actual tool calls,
//     drives the approval flow, and dispatches execution. Calls flow:
//     ToolRouter.Route → permission.Engine.Check → tool.Tool.Call.
//
// This package owns the composition + the audit log, never the policy
// (delegated to internal/permission) and never the schema (delegated to
// internal/tool). It also enforces the per-task `Tools` allowlist passed in
// on each ToolCall — the task-record field is the source of truth.
//
// Naming: lives at internal/toolbroker/ (NOT internal/broker/) to stay clear
// of the unrelated envelope broker landing in S1.3.
package toolbroker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hollis-labs/go-toolbroker/broker"
	"github.com/hollis-labs/torque/internal/permission"
	"github.com/hollis-labs/torque/internal/tool"
	"github.com/hollis-labs/torque/internal/toolrouter"
)

// AuditEntry is one record of a tool-call attempt. Decision is the terminal
// outcome (allow → call ran, deny → blocked before exec). Err is set when
// either the permission stage or the tool's Call returned an error.
type AuditEntry struct {
	At        time.Time
	SessionID string
	TaskID    string
	ToolName  string
	Decision  string // "allow" | "deny"
	Reason    string
	Err       string
}

// AuditSink records tool-call attempts. The default in-memory sink is fine
// for tests and the current single-process binary; a SQLite-backed sink can
// satisfy the same interface when the audit log graduates to a durable
// table.
type AuditSink interface {
	Record(entry AuditEntry)
	Entries() []AuditEntry
}

// MemorySink is a thread-safe in-memory AuditSink.
type MemorySink struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// NewMemorySink returns an empty MemorySink.
func NewMemorySink() *MemorySink { return &MemorySink{} }

// Record appends an entry. Safe for concurrent use.
func (s *MemorySink) Record(e AuditEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
}

// Entries returns a copy of the recorded entries in arrival order.
func (s *MemorySink) Entries() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// ToolCall is the executor-facing tool invocation request. AllowedTools, when
// non-empty, is the per-task allowlist (TaskRecord.Tools): a call to any tool
// not in the list is denied before the permission engine runs. Empty means
// "no per-task restriction" — fall through to the permission engine.
type ToolCall struct {
	SessionID    string
	TaskID       string
	ToolName     string
	Input        map[string]any
	WorkingDir   string
	AllowedTools []string
}

// ToolRouter is the composite handle threaded into executors. It owns:
//
//   - the go-toolbroker LocalBroker (for SelectTools / intent-aware curation)
//   - the internal toolrouter.Router (permission engine + tool registry)
//   - an AuditSink (every Route call writes one entry)
//
// The zero value is unusable; construct via New or NewDefault.
type ToolRouter struct {
	broker *broker.LocalBroker
	router *toolrouter.Router
	audit  AuditSink
	clock  func() time.Time // overridable for tests
}

// New wires a ToolRouter from explicit parts. Used by tests and any caller
// that wants to swap pieces (e.g. an alternate AuditSink).
func New(b *broker.LocalBroker, r *toolrouter.Router, audit AuditSink) *ToolRouter {
	if audit == nil {
		audit = NewMemorySink()
	}
	return &ToolRouter{
		broker: b,
		router: r,
		audit:  audit,
		clock:  time.Now,
	}
}

// NewDefault builds a ToolRouter with default rules + an empty in-memory
// MCP-tool registry on the broker side, and a permission engine in
// ModeDefault on the router side. Production callers (bootstrap.Executors)
// use this; tools are registered later via RegisterMCP / on the underlying
// tool.Registry.
func NewDefault() *ToolRouter {
	lb := broker.NewLocalBroker(nil, broker.DefaultRules())
	reg := tool.NewRegistry()
	eng := permission.NewEngine(permission.ModeDefault, nil)
	rt := toolrouter.New(reg, eng)
	return New(lb, rt, NewMemorySink())
}

// Broker exposes the underlying go-toolbroker LocalBroker so callers (the
// agent-prompt assembler, the per-turn tool-payload composer, etc.) can
// invoke SelectTools / RegisterTools without round-tripping through this
// facade.
func (t *ToolRouter) Broker() *broker.LocalBroker { return t.broker }

// Router exposes the underlying toolrouter.Router so callers that need to
// adjust approval handlers / timeouts at runtime can reach in.
func (t *ToolRouter) Router() *toolrouter.Router { return t.router }

// Audit exposes the AuditSink so callers can inspect (or assert on, in
// tests) the recorded tool-call history.
func (t *ToolRouter) Audit() AuditSink { return t.audit }

// RegisterMCP registers an MCP-discovered tool definition with the broker
// so SelectTools can route it. This does NOT register an executable
// tool.Tool — actual tool execution is provided through the per-task MCP
// loopback (cliexec) or per-vendor SDK (executor-api). The broker side is
// metadata-only.
func (t *ToolRouter) RegisterMCP(defs []broker.ToolDefinition) {
	if t.broker != nil {
		t.broker.RegisterTools(defs)
	}
}

// SelectTools delegates to the broker for intent-aware tool selection.
func (t *ToolRouter) SelectTools(ctx context.Context, intent string, hints []string) (*broker.SelectResult, error) {
	if t.broker == nil {
		return &broker.SelectResult{Intent: intent, Rationale: "no broker configured"}, nil
	}
	return t.broker.SelectTools(ctx, intent, hints)
}

// Route enforces the per-task allowlist, the permission engine, and the
// tool-registry dispatch — in that order — and records one AuditEntry per
// call regardless of outcome. A deny never panics or terminates the caller's
// session: the deny surfaces as a non-nil error and the audit entry tags
// the decision.
func (t *ToolRouter) Route(ctx context.Context, call ToolCall) (*tool.ToolResult, error) {
	if t.router == nil {
		return nil, fmt.Errorf("toolbroker: router not configured")
	}

	// Per-task allowlist gate (TaskRecord.Tools is the source of truth).
	if len(call.AllowedTools) > 0 && !contains(call.AllowedTools, call.ToolName) {
		err := fmt.Errorf("tool %q denied: not in per-task allowlist", call.ToolName)
		t.recordDeny(call, "not in per-task allowlist", err)
		return nil, err
	}

	// Permission engine + dispatch.
	result, err := t.router.Route(ctx, toolrouter.ToolCall{
		SessionID:  call.SessionID,
		TaskID:     call.TaskID,
		ToolName:   call.ToolName,
		Input:      call.Input,
		WorkingDir: call.WorkingDir,
	})
	if err != nil {
		t.recordDeny(call, "router denied", err)
		return nil, err
	}
	t.recordAllow(call)
	return result, nil
}

// recordDeny / recordAllow are lightweight wrappers so the call sites stay
// readable and the audit-entry shape stays consistent.

func (t *ToolRouter) recordDeny(call ToolCall, reason string, err error) {
	if t.audit == nil {
		return
	}
	entry := AuditEntry{
		At:        t.clock(),
		SessionID: call.SessionID,
		TaskID:    call.TaskID,
		ToolName:  call.ToolName,
		Decision:  "deny",
		Reason:    reason,
	}
	if err != nil {
		entry.Err = err.Error()
	}
	t.audit.Record(entry)
}

func (t *ToolRouter) recordAllow(call ToolCall) {
	if t.audit == nil {
		return
	}
	t.audit.Record(AuditEntry{
		At:        t.clock(),
		SessionID: call.SessionID,
		TaskID:    call.TaskID,
		ToolName:  call.ToolName,
		Decision:  "allow",
	})
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

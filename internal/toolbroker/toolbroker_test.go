package toolbroker_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/permission"
	"github.com/hollis-labs/clockwork-manifold/internal/tool"
	"github.com/hollis-labs/clockwork-manifold/internal/toolbroker"
	"github.com/hollis-labs/clockwork-manifold/internal/toolrouter"
	"github.com/hollis-labs/go-toolbroker/broker"
)

// build wires a ToolRouter around a registry holding two test tools so each
// AC7 case can target either a read-only or a destructive tool.
func build(t *testing.T, mode permission.Mode, rules *permission.RuleSet) (*toolbroker.ToolRouter, *toolbroker.MemorySink) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Register(tool.NewTool("dev_read", "reads a file",
		tool.WithCategory(tool.CategoryCoreIO),
		tool.WithReadOnly(true),
		tool.WithCallFunc(func(ctx context.Context, in map[string]any, ec tool.ExecutionContext) (*tool.ToolResult, error) {
			return &tool.ToolResult{Output: "read"}, nil
		}),
	))
	reg.Register(tool.NewTool("dev_bash", "runs a shell command",
		tool.WithCategory(tool.CategoryCoreIO),
		tool.WithDestructiveFunc(func(in map[string]any) bool {
			cmd, _ := in["command"].(string)
			return tool.IsDestructiveCommand(cmd)
		}),
		tool.WithReadOnlyFunc(func(in map[string]any) bool {
			cmd, _ := in["command"].(string)
			return tool.IsReadOnlyCommand(cmd)
		}),
		tool.WithCallFunc(func(ctx context.Context, in map[string]any, ec tool.ExecutionContext) (*tool.ToolResult, error) {
			return &tool.ToolResult{Output: "executed"}, nil
		}),
	))
	eng := permission.NewEngine(mode, rules)
	router := toolrouter.New(reg, eng)
	lb := broker.NewLocalBroker(nil, nil)
	sink := toolbroker.NewMemorySink()
	return toolbroker.New(lb, router, sink), sink
}

// AC7 (a): tool-call permission denial path — explicit deny rule blocks the
// call, surfaces an error, audit-logs the deny.
func TestToolRouter_PermissionDenyBlocksAndAudits(t *testing.T) {
	rules := &permission.RuleSet{
		Rules: []permission.Rule{
			{Tool: "dev_bash", Pattern: "rm -rf", Behavior: permission.DecisionDeny},
		},
	}
	tr, sink := build(t, permission.ModeDefault, rules)

	_, err := tr.Route(context.Background(), toolbroker.ToolCall{
		SessionID: "s1",
		TaskID:    "t1",
		ToolName:  "dev_bash",
		Input:     map[string]any{"command": "rm -rf /tmp/important"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied")

	entries := sink.Entries()
	require.Len(t, entries, 1, "deny must record exactly one audit entry")
	assert.Equal(t, "deny", entries[0].Decision)
	assert.Equal(t, "dev_bash", entries[0].ToolName)
	assert.Equal(t, "t1", entries[0].TaskID)
	assert.NotEmpty(t, entries[0].Err)
}

// AC7 (b): allow-list path — read-only tools pass through and write a
// matching allow entry to the audit log.
func TestToolRouter_AllowReadOnlyAndAudits(t *testing.T) {
	tr, sink := build(t, permission.ModeDefault, nil)

	res, err := tr.Route(context.Background(), toolbroker.ToolCall{
		SessionID: "s1",
		TaskID:    "t1",
		ToolName:  "dev_read",
		Input:     map[string]any{"path": "/src/main.go"},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "read", res.Output)

	entries := sink.Entries()
	require.Len(t, entries, 1)
	assert.Equal(t, "allow", entries[0].Decision)
	assert.Equal(t, "dev_read", entries[0].ToolName)
}

// AC7 (c): per-call audit-log entry — each Route attempt produces exactly one
// AuditEntry, regardless of outcome. Three calls → three entries, in order.
func TestToolRouter_AuditEntryPerCall(t *testing.T) {
	tr, sink := build(t, permission.ModeDefault, nil)

	for i := 0; i < 3; i++ {
		_, _ = tr.Route(context.Background(), toolbroker.ToolCall{
			SessionID: "s1",
			TaskID:    "t1",
			ToolName:  "dev_read",
			Input:     map[string]any{"path": "/x"},
		})
	}
	assert.Len(t, sink.Entries(), 3, "every Route call writes one entry")
}

// AC7 (d): a denied call must not crash the session — subsequent calls on
// the same ToolRouter still succeed.
func TestToolRouter_DenyDoesNotCrashSession(t *testing.T) {
	rules := &permission.RuleSet{
		Rules: []permission.Rule{
			{Tool: "dev_bash", Pattern: "rm -rf", Behavior: permission.DecisionDeny},
		},
	}
	tr, sink := build(t, permission.ModeDefault, rules)

	// First call: denied.
	_, err := tr.Route(context.Background(), toolbroker.ToolCall{
		SessionID: "s1", TaskID: "t1", ToolName: "dev_bash",
		Input: map[string]any{"command": "rm -rf /tmp/x"},
	})
	require.Error(t, err)

	// Second call on the same router still works.
	res, err := tr.Route(context.Background(), toolbroker.ToolCall{
		SessionID: "s1", TaskID: "t1", ToolName: "dev_read",
		Input: map[string]any{"path": "/x"},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	entries := sink.Entries()
	require.Len(t, entries, 2)
	assert.Equal(t, "deny", entries[0].Decision)
	assert.Equal(t, "allow", entries[1].Decision)
}

// Per-task allowlist: when ToolCall.AllowedTools is non-empty, calls outside
// the list are denied before the permission engine runs.
func TestToolRouter_PerTaskAllowlistDenies(t *testing.T) {
	tr, sink := build(t, permission.ModeDefault, nil)

	_, err := tr.Route(context.Background(), toolbroker.ToolCall{
		SessionID:    "s1",
		TaskID:       "t1",
		ToolName:     "dev_bash",
		Input:        map[string]any{"command": "ls"},
		AllowedTools: []string{"dev_read"}, // dev_bash NOT in list
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "per-task allowlist")

	entries := sink.Entries()
	require.Len(t, entries, 1)
	assert.Equal(t, "deny", entries[0].Decision)
	assert.Contains(t, entries[0].Reason, "per-task allowlist")
}

// Per-task allowlist: a tool that IS on the allowlist still passes the
// permission engine and runs.
func TestToolRouter_PerTaskAllowlistAllows(t *testing.T) {
	tr, _ := build(t, permission.ModeDefault, nil)

	res, err := tr.Route(context.Background(), toolbroker.ToolCall{
		SessionID:    "s1",
		TaskID:       "t1",
		ToolName:     "dev_read",
		Input:        map[string]any{"path": "/x"},
		AllowedTools: []string{"dev_read"},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "read", res.Output)
}

// SelectTools delegates to go-toolbroker. With no rules + a few registered
// tools, all of them come back.
func TestToolRouter_SelectToolsDelegates(t *testing.T) {
	tr, _ := build(t, permission.ModeDefault, nil)
	tr.RegisterMCP([]broker.ToolDefinition{
		{Name: "alpha", Description: "first", Server: "test"},
		{Name: "beta", Description: "second", Server: "test"},
	})

	res, err := tr.SelectTools(context.Background(), "*", nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 2, res.Total)
}

// NewDefault returns a usable ToolRouter (broker + router + memory sink).
// Smoke test only — production wiring lives in bootstrap.Executors.
func TestNewDefault_ConstructsUsableRouter(t *testing.T) {
	tr := toolbroker.NewDefault()
	require.NotNil(t, tr)
	assert.NotNil(t, tr.Broker())
	assert.NotNil(t, tr.Router())
	assert.NotNil(t, tr.Audit())
}

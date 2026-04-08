package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTool is a compile-time check that the Tool interface can be implemented.
type mockTool struct{}

func (m *mockTool) Name() string        { return "mock" }
func (m *mockTool) Description() string { return "a mock tool" }
func (m *mockTool) Category() string    { return tool.CategoryCoreIO }
func (m *mockTool) Source() string      { return tool.SourceBuiltin }
func (m *mockTool) Tags() []string      { return []string{} }
func (m *mockTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (m *mockTool) Call(_ context.Context, _ map[string]any, _ tool.ExecutionContext) (*tool.ToolResult, error) {
	return &tool.ToolResult{Output: "ok"}, nil
}
func (m *mockTool) ValidateInput(_ map[string]any) error                  { return nil }
func (m *mockTool) IsConcurrencySafe(_ map[string]any) bool               { return true }
func (m *mockTool) IsReadOnly(_ map[string]any) bool                      { return true }
func (m *mockTool) IsDestructive(_ map[string]any) bool                   { return false }
func (m *mockTool) DefaultPermissions() []tool.PermissionRule             { return []tool.PermissionRule{} }

// Compile-time interface check.
var _ tool.Tool = (*mockTool)(nil)

func TestConstants(t *testing.T) {
	assert.Equal(t, "core-io", tool.CategoryCoreIO)
	assert.Equal(t, "search", tool.CategorySearch)
	assert.Equal(t, "mcp", tool.CategoryMCP)
	assert.Equal(t, "agent", tool.CategoryAgent)
	assert.Equal(t, "session", tool.CategorySession)
	assert.Equal(t, "execution", tool.CategoryExecution)

	assert.Equal(t, "builtin", tool.SourceBuiltin)
	assert.Equal(t, "mcp", tool.SourceMCP)
	assert.Equal(t, "plugin", tool.SourcePlugin)
	assert.Equal(t, "user", tool.SourceUser)
}

func TestToolResultFields(t *testing.T) {
	r := tool.ToolResult{
		Output:   "hello",
		IsError:  true,
		Metadata: map[string]string{"k": "v"},
	}
	assert.Equal(t, "hello", r.Output)
	assert.True(t, r.IsError)
	assert.Equal(t, "v", r.Metadata["k"])
}

func TestToolResultJSONRoundtrip(t *testing.T) {
	orig := tool.ToolResult{
		Output:   "some output",
		IsError:  false,
		Metadata: map[string]string{"x": "y"},
	}
	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded tool.ToolResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, orig.Output, decoded.Output)
	assert.Equal(t, orig.IsError, decoded.IsError)
	assert.Equal(t, orig.Metadata, decoded.Metadata)
}

func TestToolResultIsErrorOmitted(t *testing.T) {
	r := tool.ToolResult{Output: "ok"}
	data, err := json.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "is_error")
}

func TestPermissionRuleFields(t *testing.T) {
	p := tool.PermissionRule{Pattern: "glob:*", Behavior: "allow"}
	assert.Equal(t, "glob:*", p.Pattern)
	assert.Equal(t, "allow", p.Behavior)
}

func TestPermissionRuleJSONRoundtrip(t *testing.T) {
	orig := tool.PermissionRule{Pattern: "bash:ls *", Behavior: "deny"}
	data, err := json.Marshal(orig)
	require.NoError(t, err)
	var decoded tool.PermissionRule
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, orig, decoded)
}

func TestExecutionContext(t *testing.T) {
	ec := tool.ExecutionContext{
		SessionID:  "sess-1",
		TaskID:     "task-42",
		WorkingDir: "/tmp/work",
	}
	assert.Equal(t, "sess-1", ec.SessionID)
	assert.Equal(t, "task-42", ec.TaskID)
	assert.Equal(t, "/tmp/work", ec.WorkingDir)
}

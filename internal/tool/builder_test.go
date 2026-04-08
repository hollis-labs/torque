package tool_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTool_Defaults(t *testing.T) {
	tl := tool.NewTool("my-tool", "does stuff")

	assert.Equal(t, "my-tool", tl.Name())
	assert.Equal(t, "does stuff", tl.Description())
	assert.Equal(t, "", tl.Category())
	assert.Equal(t, tool.SourceBuiltin, tl.Source())
	assert.Equal(t, []string{}, tl.Tags())
	assert.Equal(t, json.RawMessage(`{"type":"object"}`), tl.InputSchema())
	assert.Equal(t, []tool.PermissionRule{}, tl.DefaultPermissions())
	assert.Nil(t, tl.ValidateInput(nil))
	assert.False(t, tl.IsConcurrencySafe(nil))
	assert.False(t, tl.IsReadOnly(nil))
	assert.False(t, tl.IsDestructive(nil))
}

func TestNewTool_CallNilErrors(t *testing.T) {
	tl := tool.NewTool("no-fn", "no call func")
	_, err := tl.Call(context.Background(), nil, tool.ExecutionContext{})
	assert.Error(t, err)
}

func TestNewTool_WithCategory(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithCategory(tool.CategorySearch))
	assert.Equal(t, tool.CategorySearch, tl.Category())
}

func TestNewTool_WithSource(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithSource(tool.SourceMCP))
	assert.Equal(t, tool.SourceMCP, tl.Source())
}

func TestNewTool_WithTags(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithTags("alpha", "beta"))
	assert.Equal(t, []string{"alpha", "beta"}, tl.Tags())
}

func TestNewTool_TagsCopy(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithTags("a", "b"))
	tags1 := tl.Tags()
	tags1[0] = "MUTATED"
	tags2 := tl.Tags()
	assert.Equal(t, "a", tags2[0], "Tags should return a copy, not the original slice")
}

func TestNewTool_WithSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)
	tl := tool.NewTool("t", "d", tool.WithSchema(schema))
	assert.Equal(t, schema, tl.InputSchema())
}

func TestNewTool_WithSchemaMap(t *testing.T) {
	m := map[string]any{"type": "object", "required": []any{"cmd"}}
	tl := tool.NewTool("t", "d", tool.WithSchemaMap(m))
	schema := tl.InputSchema()
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(schema, &decoded))
	assert.Equal(t, "object", decoded["type"])
}

func TestNewTool_WithTimeout(t *testing.T) {
	// We can verify the Timeout via the concrete type via a type assertion.
	tl := tool.NewTool("t", "d", tool.WithTimeout(30*time.Second))
	type timeoutGetter interface{ Timeout() time.Duration }
	tg, ok := tl.(timeoutGetter)
	require.True(t, ok, "toolImpl should expose Timeout()")
	assert.Equal(t, 30*time.Second, tg.Timeout())
}

func TestNewTool_WithCallFunc(t *testing.T) {
	called := false
	tl := tool.NewTool("t", "d", tool.WithCallFunc(func(ctx context.Context, input map[string]any, execCtx tool.ExecutionContext) (*tool.ToolResult, error) {
		called = true
		return &tool.ToolResult{Output: "done"}, nil
	}))
	res, err := tl.Call(context.Background(), nil, tool.ExecutionContext{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "done", res.Output)
}

func TestNewTool_WithConcurrencySafe(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithConcurrencySafe(true))
	assert.True(t, tl.IsConcurrencySafe(nil))
}

func TestNewTool_WithConcurrencySafeFunc(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithConcurrencySafeFunc(func(input map[string]any) bool {
		return input["safe"] == true
	}))
	assert.True(t, tl.IsConcurrencySafe(map[string]any{"safe": true}))
	assert.False(t, tl.IsConcurrencySafe(map[string]any{"safe": false}))
}

func TestNewTool_WithReadOnly(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithReadOnly(true))
	assert.True(t, tl.IsReadOnly(nil))
}

func TestNewTool_WithReadOnlyFunc(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithReadOnlyFunc(func(input map[string]any) bool {
		cmd, _ := input["cmd"].(string)
		return cmd == "ls"
	}))
	assert.True(t, tl.IsReadOnly(map[string]any{"cmd": "ls"}))
	assert.False(t, tl.IsReadOnly(map[string]any{"cmd": "rm -rf /"}))
}

func TestNewTool_WithDestructive(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithDestructive(true))
	assert.True(t, tl.IsDestructive(nil))
}

func TestNewTool_WithDestructiveFunc(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithDestructiveFunc(func(input map[string]any) bool {
		cmd, _ := input["cmd"].(string)
		return cmd == "rm"
	}))
	assert.True(t, tl.IsDestructive(map[string]any{"cmd": "rm"}))
	assert.False(t, tl.IsDestructive(map[string]any{"cmd": "ls"}))
}

func TestNewTool_WithPermissions(t *testing.T) {
	rules := []tool.PermissionRule{
		{Pattern: "bash:ls *", Behavior: "allow"},
		{Pattern: "bash:rm *", Behavior: "deny"},
	}
	tl := tool.NewTool("t", "d", tool.WithPermissions(rules...))
	perms := tl.DefaultPermissions()
	assert.Equal(t, rules, perms)
}

func TestNewTool_WithValidateFunc(t *testing.T) {
	tl := tool.NewTool("t", "d", tool.WithValidateFunc(func(input map[string]any) error {
		if input["cmd"] == nil {
			return assert.AnError
		}
		return nil
	}))
	assert.Error(t, tl.ValidateInput(map[string]any{}))
	assert.NoError(t, tl.ValidateInput(map[string]any{"cmd": "ls"}))
}

func TestNewTool_AllOptions(t *testing.T) {
	tl := tool.NewTool("full-tool", "full description",
		tool.WithCategory(tool.CategoryCoreIO),
		tool.WithSource(tool.SourcePlugin),
		tool.WithTags("a", "b", "c"),
		tool.WithSchema(json.RawMessage(`{"type":"object"}`)),
		tool.WithTimeout(10*time.Second),
		tool.WithConcurrencySafe(true),
		tool.WithReadOnly(true),
		tool.WithDestructive(false),
		tool.WithPermissions(tool.PermissionRule{Pattern: "*", Behavior: "allow"}),
		tool.WithCallFunc(func(_ context.Context, _ map[string]any, _ tool.ExecutionContext) (*tool.ToolResult, error) {
			return &tool.ToolResult{Output: "full"}, nil
		}),
		tool.WithValidateFunc(func(_ map[string]any) error { return nil }),
	)

	assert.Equal(t, "full-tool", tl.Name())
	assert.Equal(t, "full description", tl.Description())
	assert.Equal(t, tool.CategoryCoreIO, tl.Category())
	assert.Equal(t, tool.SourcePlugin, tl.Source())
	assert.Equal(t, []string{"a", "b", "c"}, tl.Tags())
	assert.True(t, tl.IsConcurrencySafe(nil))
	assert.True(t, tl.IsReadOnly(nil))
	assert.False(t, tl.IsDestructive(nil))
	assert.Len(t, tl.DefaultPermissions(), 1)

	res, err := tl.Call(context.Background(), nil, tool.ExecutionContext{})
	require.NoError(t, err)
	assert.Equal(t, "full", res.Output)
	assert.NoError(t, tl.ValidateInput(nil))
}

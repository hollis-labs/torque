package toolrouter_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/permission"
	"github.com/hollis-labs/torque/internal/tool"
	"github.com/hollis-labs/torque/internal/toolrouter"
)

// setupRouter creates a router with two standard test tools.
func setupRouter(mode permission.Mode, rules *permission.RuleSet) (*toolrouter.Router, *tool.Registry) {
	reg := tool.NewRegistry()

	devRead := tool.NewTool("dev_read", "reads a file",
		tool.WithCategory(tool.CategoryCoreIO),
		tool.WithReadOnly(true),
		tool.WithConcurrencySafe(true),
		tool.WithCallFunc(func(ctx context.Context, input map[string]any, execCtx tool.ExecutionContext) (*tool.ToolResult, error) {
			path, _ := input["path"].(string)
			return &tool.ToolResult{Output: "contents of " + path}, nil
		}),
	)

	devBash := tool.NewTool("dev_bash", "runs a shell command",
		tool.WithCategory(tool.CategoryCoreIO),
		tool.WithDestructiveFunc(func(input map[string]any) bool {
			cmd, _ := input["command"].(string)
			return tool.IsDestructiveCommand(cmd)
		}),
		tool.WithReadOnlyFunc(func(input map[string]any) bool {
			cmd, _ := input["command"].(string)
			return tool.IsReadOnlyCommand(cmd)
		}),
		tool.WithCallFunc(func(ctx context.Context, input map[string]any, execCtx tool.ExecutionContext) (*tool.ToolResult, error) {
			cmd, _ := input["command"].(string)
			return &tool.ToolResult{Output: "executed: " + cmd}, nil
		}),
	)

	reg.Register(devRead)
	reg.Register(devBash)

	engine := permission.NewEngine(mode, rules)
	router := toolrouter.New(reg, engine)
	return router, reg
}

func TestRouter_AllowReadOnly(t *testing.T) {
	router, _ := setupRouter(permission.ModeDefault, nil)

	result, err := router.Route(context.Background(), toolrouter.ToolCall{
		SessionID: "session-1",
		TaskID:    "task-1",
		ToolName:  "dev_read",
		Input:     map[string]any{"path": "/src/main.go"},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "contents of /src/main.go", result.Output)
}

func TestRouter_YoloMode_AllowAll(t *testing.T) {
	router, _ := setupRouter(permission.ModeYolo, nil)

	result, err := router.Route(context.Background(), toolrouter.ToolCall{
		SessionID: "session-1",
		TaskID:    "task-1",
		ToolName:  "dev_bash",
		Input:     map[string]any{"command": "rm -rf /tmp/stuff"},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "executed: rm -rf /tmp/stuff", result.Output)
}

func TestRouter_PlanMode_DenyWrites(t *testing.T) {
	router, _ := setupRouter(permission.ModePlan, nil)

	_, err := router.Route(context.Background(), toolrouter.ToolCall{
		SessionID: "session-1",
		TaskID:    "task-1",
		ToolName:  "dev_bash",
		Input:     map[string]any{"command": "go build"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied")
}

func TestRouter_PlanMode_AllowReads(t *testing.T) {
	router, _ := setupRouter(permission.ModePlan, nil)

	result, err := router.Route(context.Background(), toolrouter.ToolCall{
		SessionID: "session-1",
		TaskID:    "task-1",
		ToolName:  "dev_read",
		Input:     map[string]any{"path": "/src/main.go"},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "contents of /src/main.go", result.Output)
}

func TestRouter_DenyRule(t *testing.T) {
	rules := &permission.RuleSet{
		Rules: []permission.Rule{
			{Tool: "dev_bash", Pattern: "rm -rf", Behavior: permission.DecisionDeny},
		},
	}
	router, _ := setupRouter(permission.ModeDefault, rules)

	_, err := router.Route(context.Background(), toolrouter.ToolCall{
		SessionID: "session-1",
		TaskID:    "task-1",
		ToolName:  "dev_bash",
		Input:     map[string]any{"command": "rm -rf /tmp"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied")
}

func TestRouter_ToolNotFound(t *testing.T) {
	router, _ := setupRouter(permission.ModeDefault, nil)

	_, err := router.Route(context.Background(), toolrouter.ToolCall{
		SessionID: "session-1",
		TaskID:    "task-1",
		ToolName:  "nonexistent_tool",
		Input:     map[string]any{},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRouter_AskDecision_AutoDeny(t *testing.T) {
	router, _ := setupRouter(permission.ModeDefault, nil)
	router.SetApprovalTimeout(100 * time.Millisecond)

	_, err := router.Route(context.Background(), toolrouter.ToolCall{
		SessionID: "session-1",
		TaskID:    "task-1",
		ToolName:  "dev_bash",
		Input:     map[string]any{"command": "rm -rf /tmp/stuff"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied")
}

func TestRouter_AskDecision_Approved(t *testing.T) {
	router, _ := setupRouter(permission.ModeDefault, nil)
	router.SetApprovalTimeout(2 * time.Second)

	router.SetApprovalHandler(func(req *permission.ApprovalRequest) {
		go func() {
			time.Sleep(50 * time.Millisecond)
			router.RespondToApproval(req.ID, permission.DecisionAllow, permission.ScopeOnce, req.SessionID)
		}()
	})

	result, err := router.Route(context.Background(), toolrouter.ToolCall{
		SessionID: "session-1",
		TaskID:    "task-1",
		ToolName:  "dev_bash",
		Input:     map[string]any{"command": "rm -rf /tmp/stuff"},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "executed: rm -rf /tmp/stuff", result.Output)
}

func TestRouter_ValidateInput(t *testing.T) {
	reg := tool.NewRegistry()

	strictTool := tool.NewTool("strict_tool", "requires required_field",
		tool.WithCategory(tool.CategoryCoreIO),
		tool.WithValidateFunc(func(input map[string]any) error {
			if _, ok := input["required_field"]; !ok {
				return errors.New("required_field is missing")
			}
			return nil
		}),
		tool.WithCallFunc(func(ctx context.Context, input map[string]any, execCtx tool.ExecutionContext) (*tool.ToolResult, error) {
			return &tool.ToolResult{Output: "ok"}, nil
		}),
	)
	reg.Register(strictTool)

	engine := permission.NewEngine(permission.ModeDefault, nil)
	router := toolrouter.New(reg, engine)

	_, err := router.Route(context.Background(), toolrouter.ToolCall{
		SessionID: "session-1",
		TaskID:    "task-1",
		ToolName:  "strict_tool",
		Input:     map[string]any{"other_field": "value"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation")
}

func TestRouter_ExecutionContext(t *testing.T) {
	reg := tool.NewRegistry()

	var capturedCtx tool.ExecutionContext

	ctxTool := tool.NewTool("ctx_tool", "captures execution context",
		tool.WithCategory(tool.CategoryCoreIO),
		tool.WithReadOnly(true),
		tool.WithCallFunc(func(ctx context.Context, input map[string]any, execCtx tool.ExecutionContext) (*tool.ToolResult, error) {
			capturedCtx = execCtx
			return &tool.ToolResult{Output: fmt.Sprintf("session=%s task=%s dir=%s",
				execCtx.SessionID, execCtx.TaskID, execCtx.WorkingDir)}, nil
		}),
	)
	reg.Register(ctxTool)

	engine := permission.NewEngine(permission.ModeDefault, nil)
	router := toolrouter.New(reg, engine)

	call := toolrouter.ToolCall{
		SessionID:  "my-session",
		TaskID:     "my-task",
		ToolName:   "ctx_tool",
		Input:      map[string]any{},
		WorkingDir: "/workspace/project",
	}

	result, err := router.Route(context.Background(), call)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, call.SessionID, capturedCtx.SessionID)
	assert.Equal(t, call.TaskID, capturedCtx.TaskID)
	assert.Equal(t, call.WorkingDir, capturedCtx.WorkingDir)
	assert.True(t, strings.Contains(result.Output, "my-session"))
}

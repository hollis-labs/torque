package tool_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapMCPDef_ReadTool(t *testing.T) {
	def := tool.MCPToolDefinition{
		Name:        "dev_read",
		Description: "Read a file",
		InputSchema: map[string]any{"type": "object"},
	}
	tl := tool.WrapMCPDef(def, tool.CategoryCoreIO, tool.SourceBuiltin, []string{"io"})

	assert.Equal(t, "dev_read", tl.Name())
	assert.Equal(t, "Read a file", tl.Description())
	assert.Equal(t, tool.CategoryCoreIO, tl.Category())
	assert.Equal(t, tool.SourceBuiltin, tl.Source())
	assert.Equal(t, []string{"io"}, tl.Tags())
	assert.True(t, tl.IsReadOnly(nil))
	assert.True(t, tl.IsConcurrencySafe(nil))
	assert.False(t, tl.IsDestructive(nil))
}

func TestWrapMCPDef_WriteTool(t *testing.T) {
	def := tool.MCPToolDefinition{
		Name:        "dev_write",
		Description: "Write a file",
	}
	tl := tool.WrapMCPDef(def, tool.CategoryCoreIO, tool.SourceBuiltin, nil)

	assert.False(t, tl.IsReadOnly(nil))
	assert.False(t, tl.IsConcurrencySafe(nil))
}

func TestWrapMCPDef_BashSafety_LS(t *testing.T) {
	def := tool.MCPToolDefinition{Name: "dev_bash", Description: "run bash"}
	tl := tool.WrapMCPDef(def, tool.CategoryCoreIO, tool.SourceBuiltin, nil)

	input := map[string]any{"cmd": "ls /tmp"}
	assert.True(t, tl.IsReadOnly(input))
	assert.False(t, tl.IsDestructive(input))
	assert.True(t, tl.IsConcurrencySafe(input))
}

func TestWrapMCPDef_BashSafety_RM(t *testing.T) {
	def := tool.MCPToolDefinition{Name: "dev_bash", Description: "run bash"}
	tl := tool.WrapMCPDef(def, tool.CategoryCoreIO, tool.SourceBuiltin, nil)

	input := map[string]any{"cmd": "rm -rf /tmp/junk"}
	assert.False(t, tl.IsReadOnly(input))
	assert.True(t, tl.IsDestructive(input))
}

func TestWrapMCPDef_BashSafety_GitStatus(t *testing.T) {
	def := tool.MCPToolDefinition{Name: "dev_bash", Description: "run bash"}
	tl := tool.WrapMCPDef(def, tool.CategoryCoreIO, tool.SourceBuiltin, nil)

	input := map[string]any{"cmd": "git status"}
	assert.True(t, tl.IsReadOnly(input))
	assert.False(t, tl.IsDestructive(input))
}

func TestWrapMCPDef_BashSafety_Empty(t *testing.T) {
	def := tool.MCPToolDefinition{Name: "dev_bash", Description: "run bash"}
	tl := tool.WrapMCPDef(def, tool.CategoryCoreIO, tool.SourceBuiltin, nil)

	input := map[string]any{"cmd": ""}
	assert.False(t, tl.IsReadOnly(input))
	assert.False(t, tl.IsDestructive(input))
	assert.False(t, tl.IsConcurrencySafe(input))
}

func TestClassifyMCPTool(t *testing.T) {
	tests := []struct {
		name     string
		wantCat  string
		wantSrc  string
		wantTags []string
	}{
		{"dev_read", tool.CategoryCoreIO, tool.SourceBuiltin, []string{}},
		{"dev_write", tool.CategoryCoreIO, tool.SourceBuiltin, []string{}},
		{"dev_edit", tool.CategoryCoreIO, tool.SourceBuiltin, []string{}},
		{"dev_bash", tool.CategoryCoreIO, tool.SourceBuiltin, []string{}},
		{"dev_grep", tool.CategorySearch, tool.SourceBuiltin, []string{}},
		{"dev_glob", tool.CategorySearch, tool.SourceBuiltin, []string{}},
		{"web_fetch", tool.CategorySearch, tool.SourceBuiltin, []string{}},
		{"web_search", tool.CategorySearch, tool.SourceBuiltin, []string{}},
		{"mcp__github__list_issues", tool.CategoryMCP, tool.SourceMCP, []string{}},
		{"unknown_tool", tool.CategorySession, tool.SourceBuiltin, []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cat, src, tags := tool.ClassifyMCPTool(tc.name)
			assert.Equal(t, tc.wantCat, cat)
			assert.Equal(t, tc.wantSrc, src)
			assert.Equal(t, tc.wantTags, tags)
		})
	}
}

func TestWrapMCPDefs(t *testing.T) {
	defs := []tool.MCPToolDefinition{
		{Name: "dev_read", Description: "read"},
		{Name: "dev_grep", Description: "grep"},
		{Name: "mcp__gh__list", Description: "list"},
	}
	tools := tool.WrapMCPDefs(defs)
	require.Len(t, tools, 3)
	assert.Equal(t, "dev_read", tools[0].Name())
	assert.Equal(t, tool.CategoryCoreIO, tools[0].Category())
	assert.Equal(t, "dev_grep", tools[1].Name())
	assert.Equal(t, tool.CategorySearch, tools[1].Category())
	assert.Equal(t, tool.CategoryMCP, tools[2].Category())
	assert.Equal(t, tool.SourceMCP, tools[2].Source())
}

func TestToMCPDefinition_Roundtrip(t *testing.T) {
	orig := tool.MCPToolDefinition{
		Name:        "dev_read",
		Description: "reads files",
		InputSchema: map[string]any{"type": "object"},
	}
	tl := tool.WrapMCPDef(orig, tool.CategoryCoreIO, tool.SourceBuiltin, nil)
	got := tool.ToMCPDefinition(tl)

	assert.Equal(t, orig.Name, got.Name)
	assert.Equal(t, orig.Description, got.Description)
	assert.Equal(t, "object", got.InputSchema["type"])
}

func TestToMCPDefinitions(t *testing.T) {
	defs := []tool.MCPToolDefinition{
		{Name: "dev_read", Description: "read"},
		{Name: "dev_write", Description: "write"},
	}
	tools := tool.WrapMCPDefs(defs)
	back := tool.ToMCPDefinitions(tools)
	require.Len(t, back, 2)
	assert.Equal(t, "dev_read", back[0].Name)
	assert.Equal(t, "dev_write", back[1].Name)
}

func TestIsReadOnlyCommand(t *testing.T) {
	readOnly := []string{
		"ls", "ls /tmp", "cat file.txt", "head -n 5 file",
		"tail -f log", "grep pattern file", "find . -name *.go",
		"git log --oneline", "git status", "git diff HEAD",
		"go vet ./...", "go test ./...",
		"echo hello", "pwd", "whoami",
	}
	for _, cmd := range readOnly {
		assert.True(t, tool.IsReadOnlyCommand(cmd), "expected read-only: %q", cmd)
	}

	notReadOnly := []string{
		"rm file", "git push", "make build", "curl http://example.com",
	}
	for _, cmd := range notReadOnly {
		assert.False(t, tool.IsReadOnlyCommand(cmd), "expected NOT read-only: %q", cmd)
	}
}

func TestIsDestructiveCommand(t *testing.T) {
	destructive := []string{
		"rm file.txt", "rm -rf /tmp", "rmdir dir",
		"git reset --hard HEAD~1",
		"git clean -fd",
		"git push --force origin main",
		"git push -f origin main",
		"drop table users",
		"truncate table logs",
	}
	for _, cmd := range destructive {
		assert.True(t, tool.IsDestructiveCommand(cmd), "expected destructive: %q", cmd)
	}

	notDestructive := []string{
		"ls", "cat file", "git status", "go test ./...",
	}
	for _, cmd := range notDestructive {
		assert.False(t, tool.IsDestructiveCommand(cmd), "expected NOT destructive: %q", cmd)
	}
}

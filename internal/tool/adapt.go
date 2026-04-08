package tool

import (
	"context"
	"encoding/json"
	"strings"
)

// MCPToolDefinition is the wire format used by MCP servers to describe tools.
type MCPToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// WrapMCPDef wraps a single MCPToolDefinition as a Tool.
// category, source, and tags may be supplied by the caller; safety options
// are inferred from the tool name.
func WrapMCPDef(def MCPToolDefinition, category, source string, tags []string) Tool {
	schemaBytes, _ := json.Marshal(def.InputSchema)
	if len(schemaBytes) == 0 || string(schemaBytes) == "null" {
		schemaBytes = json.RawMessage(`{"type":"object"}`)
	}

	safetyOpts := inferSafetyOptions(def.Name)

	opts := []ToolOption{
		WithCategory(category),
		WithSource(source),
		WithTags(tags...),
		WithSchema(schemaBytes),
	}
	opts = append(opts, safetyOpts...)

	return NewTool(def.Name, def.Description, opts...)
}

// WrapMCPDefs wraps a slice of MCPToolDefinitions, auto-classifying each.
func WrapMCPDefs(defs []MCPToolDefinition) []Tool {
	tools := make([]Tool, 0, len(defs))
	for _, def := range defs {
		cat, src, tags := ClassifyMCPTool(def.Name)
		tools = append(tools, WrapMCPDef(def, cat, src, tags))
	}
	return tools
}

// ClassifyMCPTool returns category, source, and tags for an MCP tool name.
func ClassifyMCPTool(name string) (category, source string, tags []string) {
	switch {
	case name == "dev_read" || name == "dev_write" || name == "dev_edit" || name == "dev_bash":
		return CategoryCoreIO, SourceBuiltin, []string{}
	case name == "dev_grep" || name == "dev_glob":
		return CategorySearch, SourceBuiltin, []string{}
	case name == "web_fetch" || name == "web_search":
		return CategorySearch, SourceBuiltin, []string{}
	case strings.HasPrefix(name, "mcp__"):
		return CategoryMCP, SourceMCP, []string{}
	default:
		return CategorySession, SourceBuiltin, []string{}
	}
}

// ToMCPDefinition converts a Tool back to an MCPToolDefinition.
func ToMCPDefinition(t Tool) MCPToolDefinition {
	var schemaMap map[string]any
	_ = json.Unmarshal(t.InputSchema(), &schemaMap)
	return MCPToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: schemaMap,
	}
}

// ToMCPDefinitions converts a slice of Tools to MCPToolDefinitions.
func ToMCPDefinitions(tools []Tool) []MCPToolDefinition {
	defs := make([]MCPToolDefinition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, ToMCPDefinition(t))
	}
	return defs
}

// inferSafetyOptions returns ToolOptions that set safety predicates based on
// well-known tool names.
func inferSafetyOptions(name string) []ToolOption {
	switch name {
	case "dev_read", "dev_grep", "dev_glob":
		return []ToolOption{
			WithReadOnly(true),
			WithConcurrencySafe(true),
		}
	case "dev_write", "dev_edit":
		return []ToolOption{
			WithReadOnly(false),
			WithConcurrencySafe(false),
		}
	case "dev_bash":
		return []ToolOption{
			WithReadOnlyFunc(func(input map[string]any) bool {
				cmd, _ := input["cmd"].(string)
				if cmd == "" {
					return false
				}
				return IsReadOnlyCommand(cmd)
			}),
			WithDestructiveFunc(func(input map[string]any) bool {
				cmd, _ := input["cmd"].(string)
				if cmd == "" {
					return false
				}
				return IsDestructiveCommand(cmd)
			}),
			WithConcurrencySafeFunc(func(input map[string]any) bool {
				cmd, _ := input["cmd"].(string)
				if cmd == "" {
					return false
				}
				return IsReadOnlyCommand(cmd) && !IsDestructiveCommand(cmd)
			}),
		}
	case "web_fetch", "web_search":
		return []ToolOption{
			WithReadOnly(true),
			WithConcurrencySafe(true),
		}
	default:
		return nil
	}
}

// IsReadOnlyCommand returns true if the shell command is considered safe to
// run concurrently and does not modify state.
func IsReadOnlyCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	readOnlyPrefixes := []string{
		"ls ", "ls\t", "cat ", "cat\t", "head ", "head\t", "tail ", "tail\t",
		"grep ", "grep\t", "find ", "find\t",
		"git log", "git status", "git diff",
		"go vet", "go test",
		"echo ", "echo\t",
		"pwd", "whoami",
	}
	exactMatch := []string{"ls", "cat", "head", "tail", "grep", "find", "pwd", "whoami", "echo"}
	for _, m := range exactMatch {
		if cmd == m {
			return true
		}
	}
	for _, pfx := range readOnlyPrefixes {
		if strings.HasPrefix(cmd, pfx) {
			return true
		}
	}
	return false
}

// IsDestructiveCommand returns true if the shell command may cause data loss.
func IsDestructiveCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	destructivePrefixes := []string{
		"rm ", "rm\t", "rmdir ",
		"git reset --hard",
		"git clean",
		"git push --force",
		"git push -f",
		"drop ",
		"truncate ",
	}
	exactMatch := []string{"rm", "rmdir", "drop", "truncate"}
	for _, m := range exactMatch {
		if cmd == m {
			return true
		}
	}
	for _, pfx := range destructivePrefixes {
		if strings.HasPrefix(cmd, pfx) {
			return true
		}
	}
	return false
}

// Ensure WrapMCPDef result is a valid Tool at compile time (via a blank
// variable that exercises the Call path without running it).
var _ = func() Tool {
	return WrapMCPDef(MCPToolDefinition{Name: "dev_read", Description: "read"}, CategoryCoreIO, SourceBuiltin, nil)
}

// noopCall is used internally when a wrapped MCP tool has no real call func.
// The runner is responsible for routing the actual RPC; this satisfies the
// interface.
func noopCall(_ context.Context, _ map[string]any, _ ExecutionContext) (*ToolResult, error) {
	return &ToolResult{Output: ""}, nil
}

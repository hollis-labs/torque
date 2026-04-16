package mcpadapter_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCP_Template_Create_And_Get(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "clockwork_template_create", map[string]interface{}{
		"id":          "backend-fix",
		"name":        "Backend Fix",
		"description": "Fix {{issue}}",
		"kind":        "agent",
		"executor":    "cli",
	})
	require.False(t, isErr, text)
	var tpl map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &tpl))
	assert.Equal(t, "backend-fix", tpl["ID"])
	assert.Equal(t, float64(1), tpl["Version"])
	assert.Equal(t, "agent", tpl["Kind"])

	// Default auto_execute=true when omitted.
	assert.Equal(t, true, tpl["AutoExecute"])

	// Read back via get.
	getText, isErr := callTool(t, a, "clockwork_template_get", map[string]interface{}{
		"id": "backend-fix",
	})
	require.False(t, isErr)
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(getText), &got))
	assert.Equal(t, "Backend Fix", got["Name"])
}

func TestMCP_Template_Update_AppendsVersion(t *testing.T) {
	a := setupAdapter(t)
	_, _ = callTool(t, a, "clockwork_template_create", map[string]interface{}{
		"id": "t", "name": "v1", "description": "x", "kind": "agent", "executor": "cli",
	})

	text, isErr := callTool(t, a, "clockwork_template_update", map[string]interface{}{
		"id": "t", "name": "v2",
	})
	require.False(t, isErr, text)
	var tpl map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &tpl))
	assert.Equal(t, float64(2), tpl["Version"])
	assert.Equal(t, "v2", tpl["Name"])
}

func TestMCP_Template_Archive(t *testing.T) {
	a := setupAdapter(t)
	_, _ = callTool(t, a, "clockwork_template_create", map[string]interface{}{
		"id": "t", "name": "t", "description": "x", "kind": "agent", "executor": "cli",
	})
	text, isErr := callTool(t, a, "clockwork_template_archive", map[string]interface{}{
		"id": "t", "version": 1,
	})
	require.False(t, isErr, text)

	getText, _ := callTool(t, a, "clockwork_template_get", map[string]interface{}{
		"id": "t", "version": 1,
	})
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(getText), &got))
	assert.Equal(t, true, got["IsArchived"])
}

func TestMCP_Template_Delete_NoReferences(t *testing.T) {
	a := setupAdapter(t)
	_, _ = callTool(t, a, "clockwork_template_create", map[string]interface{}{
		"id": "t", "name": "t", "description": "x", "kind": "agent", "executor": "cli",
	})
	text, isErr := callTool(t, a, "clockwork_template_delete", map[string]interface{}{
		"id": "t",
	})
	require.False(t, isErr, text)

	_, isErr = callTool(t, a, "clockwork_template_get", map[string]interface{}{"id": "t"})
	assert.True(t, isErr, "deleted template should not be found")
}

func TestMCP_Template_List(t *testing.T) {
	a := setupAdapter(t)
	for _, id := range []string{"a", "b"} {
		_, _ = callTool(t, a, "clockwork_template_create", map[string]interface{}{
			"id": id, "name": id, "description": "x", "kind": "agent", "executor": "cli",
		})
	}
	text, isErr := callTool(t, a, "clockwork_template_list", map[string]interface{}{})
	require.False(t, isErr)
	var list []map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &list))
	assert.Len(t, list, 2)
}

func TestMCP_TaskCreateFromTemplate_RoundTrip(t *testing.T) {
	a := setupAdapter(t)

	_, _ = callTool(t, a, "clockwork_template_create", map[string]interface{}{
		"id":                "backend-fix",
		"name":              "Backend Fix",
		"description":       "Fix {{issue}}",
		"kind":              "agent",
		"executor":          "cli",
		"required_vars":     `["issue"]`,
		"metadata_template": `{"working_dir":"{{repo_path}}"}`,
	})

	text, isErr := callTool(t, a, "clockwork_task_create_from_template", map[string]interface{}{
		"template_id": "backend-fix",
		"title":       "Fix auth",
		"vars":        `{"issue":"auth-42","repo_path":"/tmp/repo"}`,
	})
	require.False(t, isErr, text)
	var task map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &task))
	assert.Equal(t, "Fix auth", task["Title"])
	assert.Equal(t, "Fix auth-42", task["Description"])
	assert.Equal(t, "agent", task["Kind"])
	assert.Equal(t, "cli", task["Executor"])
}

func TestMCP_TaskCreateFromTemplate_MissingVar_Errors(t *testing.T) {
	a := setupAdapter(t)
	_, _ = callTool(t, a, "clockwork_template_create", map[string]interface{}{
		"id": "x", "name": "x", "description": "x", "kind": "agent", "executor": "cli",
		"required_vars": `["a"]`,
	})
	_, isErr := callTool(t, a, "clockwork_task_create_from_template", map[string]interface{}{
		"template_id": "x", "title": "t",
	})
	assert.True(t, isErr, "missing required var should surface as a tool error")
}

package mcpadapter_test

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestProjectExternalReferenceViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)
	repo := t.TempDir()
	args := map[string]any{"name": "Registry project", "repo_path": repo, "external_authority": "registry", "external_id": "project:tether", "description": "original"}
	text, failed := callTool(t, a, "torque_project_create", args)
	require.False(t, failed, text)
	var first map[string]any
	parseData(t, text, &first)
	assert.Equal(t, true, first["created"])
	assert.Equal(t, false, first["found"])
	assert.Equal(t, "registry", first["ExternalAuthority"])
	args["description"] = "ignored on replay"
	text, failed = callTool(t, a, "torque_project_create", args)
	require.False(t, failed, text)
	var replay map[string]any
	parseData(t, text, &replay)
	assert.Equal(t, first["ID"], replay["ID"])
	assert.Equal(t, false, replay["created"])
	assert.Equal(t, true, replay["found"])
	assert.Equal(t, "original", replay["Description"])
	for _, field := range []string{"name", "repo_path"} {
		old := args[field]
		args[field] = "different-and-not-a-directory"
		text, failed = callTool(t, a, "torque_project_create", args)
		require.True(t, failed, text)
		code, _, _ := parseError(t, text)
		assert.Equal(t, "conflict", code)
		args[field] = old
	}
	ref := map[string]any{"external_authority": "registry", "external_id": "project:tether"}
	text, failed = callTool(t, a, "torque_project_get", ref)
	require.False(t, failed, text)
	var got map[string]any
	parseData(t, text, &got)
	assert.Equal(t, first["ID"], got["ID"])
	assert.Equal(t, "Registry project", got["Name"])
	assert.Equal(t, repo, got["RepoPath"])
	assert.Equal(t, first["UpdatedAt"], got["UpdatedAt"])
	require.NoError(t, svc.Project.Archive(first["ID"].(string)))
	text, failed = callTool(t, a, "torque_project_get", ref)
	require.False(t, failed, text)
	text, failed = callTool(t, a, "torque_project_create", args)
	require.False(t, failed, text)
	parseData(t, text, &got)
	assert.Equal(t, first["ID"], got["ID"])
	ref["external_id"] = "unknown"
	text, failed = callTool(t, a, "torque_project_get", ref)
	require.True(t, failed, text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "not_found", code)
	// A distinct authority can use the same external ID, and names are not unique.
	args["external_authority"] = "other"
	text, failed = callTool(t, a, "torque_project_create", args)
	require.False(t, failed, text)
	parseData(t, text, &got)
	assert.NotEqual(t, first["ID"], got["ID"])
	// No-reference requests retain the original shape and duplicate-name behavior.
	delete(args, "external_authority")
	delete(args, "external_id")
	var previous any
	for i := 0; i < 2; i++ {
		text, failed = callTool(t, a, "torque_project_create", args)
		require.False(t, failed, text)
		got = map[string]any{}
		parseData(t, text, &got)
		assert.NotContains(t, got, "created")
		assert.NotContains(t, got, "found")
		assert.NotContains(t, got, "ExternalAuthority")
		assert.NotContains(t, got, "ExternalID")
		if previous != nil {
			assert.NotEqual(t, previous, got["ID"])
		}
		previous = got["ID"]
	}
}

func TestProjectExternalReferenceInvalidSelectorsViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)
	for _, ref := range []map[string]any{
		{"external_authority": "registry"}, {"external_id": "project:tether"},
		{"external_authority": "registry", "external_id": " "},
		{"external_authority": "", "external_id": ""},
		{"external_authority": " ", "external_id": "id"},
	} {
		for _, tool := range []string{"torque_project_create", "torque_project_get"} {
			args := map[string]any{"name": "test", "repo_path": t.TempDir()}
			for k, v := range ref {
				args[k] = v
			}
			text, failed := callTool(t, a, tool, args)
			require.True(t, failed, text)
			code, _, _ := parseError(t, text)
			assert.Equal(t, "arg_invalid", code)
		}
	}
	for _, args := range []map[string]any{{}, {"id": "PRJ-unknown", "external_authority": "registry", "external_id": "id"}} {
		text, failed := callTool(t, a, "torque_project_get", args)
		require.True(t, failed, text)
		code, _, _ := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
	}
}

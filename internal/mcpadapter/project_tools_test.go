package mcpadapter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectGetViaMCP covers ENT-PROJECT's headline gap: torque_project_get
// didn't exist at all before this task. Verifies it returns the full record
// by id, and that an unknown id maps to not_found.
func TestProjectGetViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Torque",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr, "project_create should succeed: %s", text)
	var project map[string]interface{}
	parseData(t, text, &project)
	projectID := project["ID"].(string)

	text, isErr = callTool(t, a, "torque_project_get", map[string]interface{}{"id": projectID})
	require.False(t, isErr, "project_get should succeed: %s", text)
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, projectID, got["ID"])
	assert.Equal(t, "Torque", got["Name"])

	text, isErr = callTool(t, a, "torque_project_get", map[string]interface{}{"id": "PRJ-nonexistent"})
	assert.True(t, isErr, "project_get on unknown id should fail")
	code, _, _ := parseError(t, text)
	assert.Equal(t, "not_found", code)
}

// TestProjectCreateFullFieldsViaMCP covers ENT-PROJECT's `create` field
// expansion: agent_path, icon, and the path/permission arrays already in
// ProjectCreateInput but previously absent from the MCP schema.
func TestProjectCreateFullFieldsViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":          "Full Fields",
		"repo_path":     t.TempDir(),
		"agent_path":    "agents/coder.yaml",
		"icon":          "rocket",
		"read_paths":    `["/repo/src","/repo/docs"]`,
		"write_paths":   `["/repo/src"]`,
		"context_paths": `["/repo/README.md"]`,
		"permissions":   `{"shell":"ask"}`,
		"rules":         `["no-force-push"]`,
	})
	require.False(t, isErr, "project_create should succeed: %s", text)

	var project map[string]interface{}
	parseData(t, text, &project)
	assert.Equal(t, "agents/coder.yaml", project["AgentPath"])
	assert.Equal(t, "rocket", project["Icon"])

	readPaths := project["ReadPaths"].(map[string]interface{})
	assert.True(t, readPaths["Valid"].(bool))
	assert.Contains(t, readPaths["String"], "/repo/src")

	perms := project["Permissions"].(map[string]interface{})
	assert.True(t, perms["Valid"].(bool))
	assert.Contains(t, perms["String"], "shell")

	rules := project["Rules"].(map[string]interface{})
	assert.True(t, rules["Valid"].(bool))
	assert.Contains(t, rules["String"], "no-force-push")
}

// TestProjectUpdateTruePartialPatchViaMCP covers ENT-PROJECT's `update`
// acceptance criterion: true partial patch, presence-in-payload semantics.
// An omitted key never changes a field; an explicit empty value clears it
// (except repo_path, which the service layer refuses to clear).
func TestProjectUpdateTruePartialPatchViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":        "Original",
		"description": "Original description",
		"repo_path":   t.TempDir(),
		"icon":        "star",
		"read_paths":  `["/a"]`,
	})
	require.False(t, isErr, "project_create should succeed: %s", text)
	var project map[string]interface{}
	parseData(t, text, &project)
	projectID := project["ID"].(string)

	// A no-op update (no fields present besides id) reports updated=false and
	// changes nothing.
	text, isErr = callTool(t, a, "torque_project_update", map[string]interface{}{"id": projectID})
	require.False(t, isErr, "project_update with no fields should succeed: %s", text)
	var noop map[string]interface{}
	parseData(t, text, &noop)
	assert.Equal(t, false, noop["updated"])

	// Updating only status must leave name/description/icon/read_paths
	// untouched (omitted keys are not "change this").
	text, isErr = callTool(t, a, "torque_project_update", map[string]interface{}{
		"id":     projectID,
		"status": "inactive",
	})
	require.False(t, isErr, "project_update status should succeed: %s", text)

	text, isErr = callTool(t, a, "torque_project_get", map[string]interface{}{"id": projectID})
	require.False(t, isErr)
	var afterStatus map[string]interface{}
	parseData(t, text, &afterStatus)
	assert.Equal(t, "inactive", afterStatus["Status"])
	assert.Equal(t, "Original", afterStatus["Name"], "omitted name must not change")
	assert.Equal(t, "Original description", afterStatus["Description"], "omitted description must not change")
	assert.Equal(t, "star", afterStatus["Icon"], "omitted icon must not change")
	readPaths := afterStatus["ReadPaths"].(map[string]interface{})
	assert.True(t, readPaths["Valid"].(bool), "omitted read_paths must not be cleared")

	// An explicit empty string clears icon (presence-in-payload, not
	// value-based detection).
	text, isErr = callTool(t, a, "torque_project_update", map[string]interface{}{
		"id":   projectID,
		"icon": "",
	})
	require.False(t, isErr, "project_update clearing icon should succeed: %s", text)

	text, isErr = callTool(t, a, "torque_project_get", map[string]interface{}{"id": projectID})
	require.False(t, isErr)
	var afterIconClear map[string]interface{}
	parseData(t, text, &afterIconClear)
	assert.Equal(t, "", afterIconClear["Icon"], "explicit empty string must clear icon")

	// An explicit empty array clears read_paths.
	text, isErr = callTool(t, a, "torque_project_update", map[string]interface{}{
		"id":         projectID,
		"read_paths": `[]`,
	})
	require.False(t, isErr, "project_update clearing read_paths should succeed: %s", text)

	text, isErr = callTool(t, a, "torque_project_get", map[string]interface{}{"id": projectID})
	require.False(t, isErr)
	var afterPathsClear map[string]interface{}
	parseData(t, text, &afterPathsClear)
	clearedPaths := afterPathsClear["ReadPaths"].(map[string]interface{})
	assert.False(t, clearedPaths["Valid"].(bool), "explicit empty array must clear read_paths")

	// repo_path cannot be cleared to empty — the service layer rejects it.
	text, isErr = callTool(t, a, "torque_project_update", map[string]interface{}{
		"id":        projectID,
		"repo_path": "",
	})
	assert.True(t, isErr, "clearing repo_path must fail")
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "repo_path", field)

	// An invalid status enum is rejected.
	text, isErr = callTool(t, a, "torque_project_update", map[string]interface{}{
		"id":     projectID,
		"status": "deleted",
	})
	assert.True(t, isErr, "invalid status must fail")
	code, _, _ = parseError(t, text)
	assert.Equal(t, "arg_invalid", code)

	// New repo_path, agent_path, and permissions round-trip.
	newRepo := t.TempDir()
	text, isErr = callTool(t, a, "torque_project_update", map[string]interface{}{
		"id":          projectID,
		"repo_path":   newRepo,
		"agent_path":  "agents/reviewer.yaml",
		"permissions": `{"network":"deny"}`,
	})
	require.False(t, isErr, "project_update with new fields should succeed: %s", text)

	text, isErr = callTool(t, a, "torque_project_get", map[string]interface{}{"id": projectID})
	require.False(t, isErr)
	var final map[string]interface{}
	parseData(t, text, &final)
	assert.Equal(t, newRepo, final["RepoPath"])
	assert.Equal(t, "agents/reviewer.yaml", final["AgentPath"])
	perms := final["Permissions"].(map[string]interface{})
	assert.True(t, perms["Valid"].(bool))
	assert.Contains(t, perms["String"], "network")
}

// TestProjectListSortCursorViaMCP covers ENT-PROJECT's `list` wiring:
// status filter, sort_by/sort_dir, and cursor pagination (PRIM-001/
// PRIM-002).
func TestProjectListSortCursorViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	names := []string{"Charlie", "Alpha", "Bravo"}
	for _, name := range names {
		text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
			"name":      name,
			"repo_path": t.TempDir(),
		})
		require.False(t, isErr, "project_create should succeed: %s", text)
	}

	// status filter: torque_project_list actually wires status now (was
	// previously hardcoded to an empty filter).
	text, isErr := callTool(t, a, "torque_project_list", map[string]interface{}{"status": "inactive"})
	require.False(t, isErr, "project_list with status filter should succeed: %s", text)
	var noneEnv struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &noneEnv)
	assert.Len(t, noneEnv.Items, 0, "no project is inactive yet")

	// Default order is name ASC; walk it one page at a time via cursor.
	var seen []string
	var cursor string
	for {
		args := map[string]interface{}{"limit": "1"}
		if cursor != "" {
			args["cursor"] = cursor
		}
		text, isErr = callTool(t, a, "torque_project_list", args)
		require.False(t, isErr, "project_list should succeed: %s", text)

		var env struct {
			Items []map[string]interface{} `json:"items"`
			Meta  struct {
				Returned   int     `json:"returned"`
				HasMore    bool    `json:"has_more"`
				NextCursor *string `json:"next_cursor"`
			} `json:"meta"`
		}
		parseData(t, text, &env)
		require.Len(t, env.Items, 1)
		seen = append(seen, env.Items[0]["name"].(string))

		if !env.Meta.HasMore {
			assert.Nil(t, env.Meta.NextCursor)
			break
		}
		require.NotNil(t, env.Meta.NextCursor)
		cursor = *env.Meta.NextCursor
	}
	assert.Equal(t, []string{"Alpha", "Bravo", "Charlie"}, seen)

	// sort_dir=desc reverses the walk.
	text, isErr = callTool(t, a, "torque_project_list", map[string]interface{}{"sort_dir": "desc"})
	require.False(t, isErr, "project_list sort_dir=desc should succeed: %s", text)
	var descEnv struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &descEnv)
	require.Len(t, descEnv.Items, 3)
	assert.Equal(t, "Charlie", descEnv.Items[0]["name"])

	// An invalid sort_by is rejected.
	text, isErr = callTool(t, a, "torque_project_list", map[string]interface{}{"sort_by": "bogus"})
	assert.True(t, isErr, "invalid sort_by must fail")
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "sort_by", field)

	// A cursor issued for one sort_by can't be reused with a different one.
	text, isErr = callTool(t, a, "torque_project_list", map[string]interface{}{"limit": "1"})
	require.False(t, isErr)
	var firstPage struct {
		Meta struct {
			NextCursor *string `json:"next_cursor"`
		} `json:"meta"`
	}
	parseData(t, text, &firstPage)
	require.NotNil(t, firstPage.Meta.NextCursor)

	text, isErr = callTool(t, a, "torque_project_list", map[string]interface{}{
		"sort_by": "status",
		"cursor":  *firstPage.Meta.NextCursor,
	})
	assert.True(t, isErr, "cursor issued for sort_by=name must be rejected under sort_by=status")
	code, _, field = parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "cursor", field)
}

// TestProjectArchiveUnarchiveViaMCP covers ENT-PROJECT's archive/unarchive
// wiring on top of PRIM-004's already-built service methods: orthogonal to
// status, excluded from list by default, restorable, and include_archived
// surfaces it again.
func TestProjectArchiveUnarchiveViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Archivable",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr, "project_create should succeed: %s", text)
	var project map[string]interface{}
	parseData(t, text, &project)
	projectID := project["ID"].(string)

	text, isErr = callTool(t, a, "torque_project_archive", map[string]interface{}{"id": projectID})
	require.False(t, isErr, "project_archive should succeed: %s", text)
	var archiveResult map[string]interface{}
	parseData(t, text, &archiveResult)
	assert.Equal(t, true, archiveResult["archived"])

	// Archiving does not change status.
	text, isErr = callTool(t, a, "torque_project_get", map[string]interface{}{"id": projectID})
	require.False(t, isErr)
	var afterArchive map[string]interface{}
	parseData(t, text, &afterArchive)
	assert.Equal(t, "active", afterArchive["Status"])
	archivedAt := afterArchive["ArchivedAt"].(map[string]interface{})
	assert.True(t, archivedAt["Valid"].(bool))

	// Excluded from the default list.
	text, isErr = callTool(t, a, "torque_project_list", map[string]interface{}{})
	require.False(t, isErr)
	var defaultEnv struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &defaultEnv)
	assert.Len(t, defaultEnv.Items, 0, "archived project must be excluded from the default list")

	// Included when include_archived=true.
	text, isErr = callTool(t, a, "torque_project_list", map[string]interface{}{"include_archived": "true"})
	require.False(t, isErr)
	var includeEnv struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &includeEnv)
	assert.Len(t, includeEnv.Items, 1)

	// Unarchive restores default-list visibility.
	text, isErr = callTool(t, a, "torque_project_unarchive", map[string]interface{}{"id": projectID})
	require.False(t, isErr, "project_unarchive should succeed: %s", text)
	var unarchiveResult map[string]interface{}
	parseData(t, text, &unarchiveResult)
	assert.Equal(t, true, unarchiveResult["unarchived"])

	text, isErr = callTool(t, a, "torque_project_list", map[string]interface{}{})
	require.False(t, isErr)
	var restoredEnv struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &restoredEnv)
	assert.Len(t, restoredEnv.Items, 1)

	// Archiving an unknown id maps to not_found.
	text, isErr = callTool(t, a, "torque_project_archive", map[string]interface{}{"id": "PRJ-nonexistent"})
	assert.True(t, isErr, "archiving an unknown project must fail")
	code, _, _ := parseError(t, text)
	assert.Equal(t, "not_found", code)
}

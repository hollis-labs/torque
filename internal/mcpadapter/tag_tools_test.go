package mcpadapter_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type decodedTagPage struct {
	Items []map[string]any `json:"items"`
	Meta  struct {
		Truncated  bool    `json:"truncated"`
		Returned   int     `json:"returned"`
		Limit      int     `json:"limit"`
		Total      int     `json:"total"`
		HasMore    bool    `json:"has_more"`
		NextCursor *string `json:"next_cursor"`
	} `json:"meta"`
}

func TestFullStack_TagCatalogMCP_ListEmptyPagedAndLiteralSearch(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_tag_list", map[string]interface{}{})
	require.False(t, isErr, "empty list should not error: %s", text)
	var empty decodedTagPage
	parseData(t, text, &empty)
	assert.Empty(t, empty.Items)
	assert.Equal(t, 0, empty.Meta.Total)
	assert.Equal(t, 50, empty.Meta.Limit)

	for _, args := range []map[string]interface{}{
		{"slug": "alpha-1", "name": "Alpha", "color": "red", "description": "has space"},
		{"slug": "alpha-2", "name": "alpha", "color": "blue", "description": "100%literal"},
		{"slug": "alpha-0", "name": "ALPHA", "color": "blue", "description": "under_score"},
		{"slug": "slash", "name": `back\slash`, "color": "green"},
		{"slug": "unicode", "name": "Café", "color": "red"},
	} {
		text, isErr = callTool(t, a, "torque_tag_create", args)
		require.False(t, isErr, "create should not error: %s", text)
	}

	text, isErr = callTool(t, a, "torque_tag_list", map[string]interface{}{"limit": "2"})
	require.False(t, isErr, "page1 should not error: %s", text)
	var first decodedTagPage
	parseData(t, text, &first)
	require.Equal(t, 5, first.Meta.Total)
	require.True(t, first.Meta.HasMore)
	require.NotNil(t, first.Meta.NextCursor)
	assert.Equal(t, []string{"alpha-0", "alpha-1"}, []string{first.Items[0]["slug"].(string), first.Items[1]["slug"].(string)})

	text, isErr = callTool(t, a, "torque_tag_list", map[string]interface{}{"limit": 2})
	require.False(t, isErr, "native numeric limit should not error: %s", text)
	var nativeLimit decodedTagPage
	parseData(t, text, &nativeLimit)
	assert.Equal(t, 2, nativeLimit.Meta.Limit)
	text, isErr = callTool(t, a, "torque_tag_list", map[string]interface{}{"limit": " 2 "})
	require.False(t, isErr, "whitespace string limit should match HTTP: %s", text)
	var whitespaceLimit decodedTagPage
	parseData(t, text, &whitespaceLimit)
	assert.Equal(t, 2, whitespaceLimit.Meta.Limit)

	text, isErr = callTool(t, a, "torque_tag_list", map[string]interface{}{"limit": "2", "cursor": *first.Meta.NextCursor})
	require.False(t, isErr, "page2 should not error: %s", text)
	var second decodedTagPage
	parseData(t, text, &second)
	assert.Equal(t, []string{"alpha-2", "slash"}, []string{second.Items[0]["slug"].(string), second.Items[1]["slug"].(string)})

	for _, tc := range []struct {
		query string
		want  string
	}{
		{query: " ", want: "alpha-1"},
		{query: "%", want: "alpha-2"},
		{query: "_", want: "alpha-0"},
		{query: `\`, want: "slash"},
		{query: "Café", want: "unicode"},
	} {
		text, isErr = callTool(t, a, "torque_tag_list", map[string]interface{}{"query": tc.query})
		require.False(t, isErr, "query %q should not error: %s", tc.query, text)
		var got decodedTagPage
		parseData(t, text, &got)
		require.Equal(t, 1, got.Meta.Total, tc.query)
		assert.Equal(t, tc.want, got.Items[0]["slug"], tc.query)
	}

	text, isErr = callTool(t, a, "torque_tag_list", map[string]interface{}{"color": " blue "})
	require.False(t, isErr, "exact color filter should not error: %s", text)
	var exact decodedTagPage
	parseData(t, text, &exact)
	assert.Equal(t, 0, exact.Meta.Total)
}

func TestFullStack_TagCatalogMCP_LargeCatalogByteCapTraversalAndHistoricalSlug(t *testing.T) {
	a := setupAdapter(t)
	longDesc := strings.Repeat("d", 500)
	want := make(map[string]bool)
	for i := 0; i < 220; i++ {
		slug := fmt.Sprintf("cap-%03d", i)
		want[slug] = true
		text, isErr := callTool(t, a, "torque_tag_create", map[string]interface{}{
			"slug":        slug,
			"name":        fmt.Sprintf("Cap %03d", i),
			"description": longDesc,
			"color":       "zinc",
		})
		require.False(t, isErr, "create %s should not error: %s", slug, text)
	}

	seen := make(map[string]bool)
	args := map[string]interface{}{"limit": "200", "query": "Cap"}
	for page := 0; page < 10; page++ {
		text, isErr := callTool(t, a, "torque_tag_list", args)
		require.False(t, isErr, "page %d should not error: %s", page, text)
		var got decodedTagPage
		parseData(t, text, &got)
		if page == 0 {
			require.True(t, got.Meta.Truncated, "long tag metadata should trip MCP byte cap on first page")
			require.Equal(t, 220, got.Meta.Total)
		}
		for _, item := range got.Items {
			slug := item["slug"].(string)
			require.False(t, seen[slug], "duplicate slug across cursor pages: %s", slug)
			seen[slug] = true
		}
		if !got.Meta.HasMore {
			break
		}
		require.NotNil(t, got.Meta.NextCursor)
		args["cursor"] = *got.Meta.NextCursor
	}
	assert.Equal(t, want, seen)

	historical := "historical-long-slug-" + strings.Repeat("x", 70)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "historical slug", "description": "x", "tags": []interface{}{historical},
	})
	require.False(t, isErr, "task create should auto-create historical slug: %s", text)
	text, isErr = callTool(t, a, "torque_tag_get", map[string]interface{}{"slug": historical})
	require.False(t, isErr, "get should not revalidate historical long slug: %s", text)
	var gotLong map[string]any
	parseData(t, text, &gotLong)
	assert.Equal(t, historical, gotLong["slug"])

	text, isErr = callTool(t, a, "torque_tag_list", map[string]interface{}{"query": "historical-long-slug"})
	require.False(t, isErr, "list should include historical long slug: %s", text)
	var listed decodedTagPage
	parseData(t, text, &listed)
	require.Equal(t, 1, listed.Meta.Total)
	assert.Equal(t, historical, listed.Items[0]["slug"])
}

func TestFullStack_TagCatalogMCP_CreateGetUpdateFailures(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_tag_create", map[string]interface{}{
		"name":        "API",
		"description": "original",
		"color":       "red",
	})
	require.False(t, isErr, "create should not error: %s", text)
	var created map[string]any
	parseData(t, text, &created)
	assert.Equal(t, "api", created["slug"])

	text, isErr = callTool(t, a, "torque_tag_get", map[string]interface{}{"slug": "api"})
	require.False(t, isErr, "get should not error: %s", text)
	var got map[string]any
	parseData(t, text, &got)
	assert.Equal(t, "API", got["name"])

	text, isErr = callTool(t, a, "torque_tag_update", map[string]interface{}{
		"slug":        "api",
		"name":        "API v2",
		"description": "",
		"color":       "",
	})
	require.False(t, isErr, "update should not error: %s", text)
	var updated map[string]any
	parseData(t, text, &updated)
	assert.Equal(t, "api", updated["slug"])
	assert.Equal(t, "API v2", updated["name"])
	assert.Equal(t, "", updated["description"])
	assert.Equal(t, "zinc", updated["color"])

	for _, tc := range []struct {
		name  string
		tool  string
		args  map[string]interface{}
		code  string
		field string
	}{
		{name: "duplicate", tool: "torque_tag_create", args: map[string]interface{}{"name": "API"}, code: "conflict", field: "slug"},
		{name: "missing", tool: "torque_tag_get", args: map[string]interface{}{"slug": "missing"}, code: "not_found"},
		{name: "bad type", tool: "torque_tag_get", args: map[string]interface{}{"slug": 12}, code: "arg_invalid", field: "slug"},
		{name: "limit zero", tool: "torque_tag_list", args: map[string]interface{}{"limit": "0"}, code: "arg_invalid", field: "limit"},
		{name: "limit negative", tool: "torque_tag_list", args: map[string]interface{}{"limit": "-1"}, code: "arg_invalid", field: "limit"},
		{name: "limit fraction", tool: "torque_tag_list", args: map[string]interface{}{"limit": "1.9"}, code: "arg_invalid", field: "limit"},
		{name: "limit overflow", tool: "torque_tag_list", args: map[string]interface{}{"limit": "9223372036854775808"}, code: "arg_invalid", field: "limit"},
		{name: "unknown arg", tool: "torque_tag_list", args: map[string]interface{}{"unknown": "x"}, code: "arg_invalid", field: "unknown"},
	} {
		text, isErr = callTool(t, a, tc.tool, tc.args)
		require.True(t, isErr, "%s should error: %s", tc.name, text)
		code, _, field := parseError(t, text)
		assert.Equal(t, tc.code, code, tc.name)
		if tc.field != "" {
			assert.Equal(t, tc.field, field, tc.name)
		}
	}
}

func TestFullStack_TagCatalogMCP_DeleteAndMergeDestructiveSemantics(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "delete cascade", "description": "x", "tags": []interface{}{"remove-me"},
	})
	require.False(t, isErr, "task create should not error: %s", text)
	var task map[string]any
	parseData(t, text, &task)
	deleteTaskID := task["ID"].(string)

	text, isErr = callTool(t, a, "torque_tag_delete", map[string]interface{}{"slug": "remove-me"})
	require.False(t, isErr, "delete should not error: %s", text)
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": deleteTaskID})
	require.False(t, isErr, "task get should not error: %s", text)
	var afterDelete map[string]any
	parseData(t, text, &afterDelete)
	assert.Empty(t, afterDelete["Tags"].([]interface{}), "delete cascades task_tags links")

	var mergeTaskIDs []string
	for i, tags := range [][]interface{}{{"bug", "defect"}, {"bug"}} {
		text, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
			"title": fmt.Sprintf("merge %d", i), "description": "x", "tags": tags,
		})
		require.False(t, isErr, "task create should not error: %s", text)
		var created map[string]any
		parseData(t, text, &created)
		mergeTaskIDs = append(mergeTaskIDs, created["ID"].(string))
	}
	text, isErr = callTool(t, a, "torque_tag_update", map[string]interface{}{"slug": "defect", "name": "Defect", "color": "orange", "description": "dest wins"})
	require.False(t, isErr, "dest update should not error: %s", text)

	text, isErr = callTool(t, a, "torque_tag_merge", map[string]interface{}{"source_slug": "bug", "into_slug": "defect"})
	require.False(t, isErr, "merge should not error: %s", text)
	var merged map[string]any
	parseData(t, text, &merged)
	dest := merged["tag"].(map[string]any)
	assert.Equal(t, "defect", dest["slug"])
	assert.Equal(t, "Defect", dest["name"])
	assert.Equal(t, "orange", dest["color"])
	assert.Equal(t, "dest wins", dest["description"])

	for _, id := range mergeTaskIDs {
		text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id})
		require.False(t, isErr, "task get should not error: %s", text)
		var gotTask map[string]any
		parseData(t, text, &gotTask)
		tags := gotTask["Tags"].([]interface{})
		require.Len(t, tags, 1)
		assert.Equal(t, "defect", tags[0].(map[string]interface{})["Slug"])
	}

	text, isErr = callTool(t, a, "torque_tag_get", map[string]interface{}{"slug": "bug"})
	require.True(t, isErr, "source should be gone: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "not_found", code)

	text, isErr = callTool(t, a, "torque_tag_merge", map[string]interface{}{"source_slug": "defect", "into_slug": "defect"})
	require.True(t, isErr, "same-source merge should error: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "into_slug", field)

	text, isErr = callTool(t, a, "torque_tag_merge", map[string]interface{}{"source_slug": "missing", "into_slug": "defect"})
	require.True(t, isErr, "missing source should error: %s", text)
	code, _, _ = parseError(t, text)
	assert.Equal(t, "not_found", code)

	text, isErr = callTool(t, a, "torque_tag_get", map[string]interface{}{"slug": "defect"})
	require.False(t, isErr, "destination should survive rejected merge: %s", text)
	var afterRejected map[string]any
	parseData(t, text, &afterRejected)
	assert.Equal(t, "Defect", afterRejected["name"])
	assert.Equal(t, "orange", afterRejected["color"])

	text, isErr = callTool(t, a, "torque_tag_create", map[string]interface{}{"slug": "source2", "name": "Source Two"})
	require.False(t, isErr, "source2 create should not error: %s", text)
	text, isErr = callTool(t, a, "torque_tag_merge", map[string]interface{}{"source_slug": "source2", "into_slug": "missing-dest"})
	require.True(t, isErr, "missing destination should error: %s", text)
	code, _, _ = parseError(t, text)
	assert.Equal(t, "not_found", code)
	text, isErr = callTool(t, a, "torque_tag_get", map[string]interface{}{"slug": "source2"})
	require.False(t, isErr, "source should survive rejected destination merge: %s", text)
}

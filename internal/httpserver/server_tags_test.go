package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTP_TaskList_RepeatedTagPredicates verifies the HTTP endpoint normalizes
// duplicate tag inputs at the query boundary (CW-20260911-0081). The HTTP layer
// trims/discards empty CSV elements; repeated distinct requested tags must
// produce the same results regardless of repetition.
func TestHTTP_TaskList_RepeatedTagPredicates(t *testing.T) {
	ts := setupTestServer(t)

	// Create tasks with different tag combinations
	createTask := func(title string, tags []string) string {
		tagsJSON, _ := json.Marshal(tags)
		body := `{"title":"` + title + `","description":"x","tags":` + string(tagsJSON) + `}`
		resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var created map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		resp.Body.Close()
		return created["id"].(string)
	}

	getTitles := func(tagsParam string) []string {
		u, _ := url.Parse(ts.URL + "/api/v1/tasks")
		q := u.Query()
		q.Set("tags", tagsParam)
		u.RawQuery = q.Encode()

		resp, err := http.Get(u.String())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		resp.Body.Close()
		raw := result["tasks"].([]interface{})
		out := make([]string, 0, len(raw))
		for _, t := range raw {
			out = append(out, t.(map[string]interface{})["title"].(string))
		}
		return out
	}

	_ = createTask("Torque task", []string{"torque"})
	_ = createTask("Torque+API task", []string{"torque", "api"})
	_ = createTask("MCP task", []string{"mcp"})

	// Duplicate single tag: ?tags=torque,torque should match same as ?tags=torque
	titles := getTitles("torque,torque")
	assert.ElementsMatch(t, []string{"Torque task", "Torque+API task"}, titles,
		"duplicate single tag should match same as single instance")

	// Duplicate with genuinely distinct tags: ?tags=torque,api,torque should AND-match correctly
	titles = getTitles("torque,api,torque")
	assert.Equal(t, []string{"Torque+API task"}, titles,
		"duplicate mixed with distinct tags should apply AND-match correctly")

	// Empty/whitespace-only tags in CSV: ?tags=torque,,  ,api
	// HTTP trims each CSV element and filters empty strings
	titles = getTitles("torque,,  ,api")
	assert.Equal(t, []string{"Torque+API task"}, titles,
		"empty/whitespace CSV elements should be filtered out")

	// Leading/trailing whitespace: ?tags= torque ,api
	titles = getTitles(" torque ,api")
	assert.Equal(t, []string{"Torque+API task"}, titles,
		"whitespace should be trimmed from slugs")

	// All empty/whitespace tags: ?tags=,,
	// Should return all tasks since normalized filter is empty
	titles = getTitles(",,")
	assert.Len(t, titles, 3,
		"all-empty tag filter should not restrict results")
}

// TestHTTP_TaskList_TagPredicates_BoundaryCases verifies edge cases for tag
// filtering through the HTTP adapter: mixed valid+missing tags, case-sensitive
// mismatches, and whitespace/empty handling (CW-20260911-0081).
func TestHTTP_TaskList_TagPredicates_BoundaryCases(t *testing.T) {
	ts := setupTestServer(t)

	createTask := func(title string, tags []string) string {
		tagsJSON, _ := json.Marshal(tags)
		body := `{"title":"` + title + `","description":"x","tags":` + string(tagsJSON) + `}`
		resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		var created map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		resp.Body.Close()
		return created["id"].(string)
	}

	getTitles := func(tagsParam string) []string {
		u, _ := url.Parse(ts.URL + "/api/v1/tasks")
		q := u.Query()
		q.Set("tags", tagsParam)
		u.RawQuery = q.Encode()

		resp, err := http.Get(u.String())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		resp.Body.Close()
		raw := result["tasks"].([]interface{})
		out := make([]string, 0, len(raw))
		for _, t := range raw {
			out = append(out, t.(map[string]interface{})["title"].(string))
		}
		return out
	}

	_ = createTask("Torque task", []string{"torque"})
	_ = createTask("Torque+API task", []string{"torque", "api"})

	tests := []struct {
		name     string
		tags     string
		expected []string
	}{
		{
			name:     "valid tag + missing tag: AND-match requires both, so empty result",
			tags:     "torque,nonexistent",
			expected: []string{},
		},
		{
			name:     "case-sensitive mismatch: 'Torque' != 'torque', no match",
			tags:     "Torque",
			expected: []string{},
		},
		{
			name:     "case-sensitive mismatch with valid tag: 'Torque,api' no match",
			tags:     "Torque,api",
			expected: []string{},
		},
		{
			name:     "all whitespace elements: normalized to empty filter",
			tags:     "   ,  ,    ",
			expected: []string{"Torque task", "Torque+API task"},
		},
		{
			name:     "duplicate + missing: torque,torque,missing should return empty",
			tags:     "torque,torque,missing",
			expected: []string{},
		},
		{
			name:     "whitespace + duplicate + valid: ' torque ', 'torque', 'api'",
			tags:     " torque ,torque,api",
			expected: []string{"Torque+API task"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			titles := getTitles(tc.tags)
			assert.ElementsMatch(t, tc.expected, titles, tc.name)
		})
	}
}

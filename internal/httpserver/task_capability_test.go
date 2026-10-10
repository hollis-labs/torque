package httpserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskCreate_CapabilityProfileRoundTrip(t *testing.T) {
	ts := setupTestServer(t)

	// Create task with capability_profile
	reqBody := []byte(`{
		"title": "Task with capabilities",
		"role": "specialist",
		"tier": "advanced",
		"capability_profile": {
			"mcp": ["server1", "server2"],
			"tools": ["toolA"]
		}
	}`)

	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	var createRes map[string]any
	require.NoError(t, json.Unmarshal(body, &createRes))

	data := createRes
	id := data["id"].(string)
	fmt.Println(string(body))

	assert.Equal(t, "specialist", data["role"])
	assert.Equal(t, "advanced", data["tier"])
	fmt.Println(string(body))
	caps := data["capability_profile"].(map[string]any)
	assert.Equal(t, []any{"server1", "server2"}, caps["mcp"])

	// Get task
	resp2, err := http.Get(ts.URL + "/api/v1/tasks/" + id)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	body2, _ := io.ReadAll(resp2.Body)
	var getRes map[string]any
	require.NoError(t, json.Unmarshal(body2, &getRes))

	data2 := getRes
	assert.Equal(t, "specialist", data2["role"])
	assert.Equal(t, "advanced", data2["tier"])
	caps2 := data2["capability_profile"].(map[string]any)
	assert.Equal(t, []any{"server1", "server2"}, caps2["mcp"])
}

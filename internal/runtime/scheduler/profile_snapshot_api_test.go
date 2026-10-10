package scheduler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"encoding/json"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfileSnapshotRedaction_REST_API(t *testing.T) {
	sched, store, _ := setupScheduler(t)

	sched.Profiles = config.ProfileMap{
		"mock": {
			Provider:         "openai",
			Model:            "gpt-4",
			Executor:         "cli",
			RuntimeKind:      "pty",
			PermissionMode:   "acceptEdits",
			SystemPrompt:     "sentinel-system-prompt",
			APIKey:           "sentinel-api-key",
			Args:             []string{"--secret=sentinel-args"},
			EnvStripPrefixes: []string{"sentinel-env"},
			BaseURL:          "https://sentinel-url.com",
		},
	}

	store.CreateTask(&sqlstore.TaskRecord{
		ID:            "CW-0001",
		Title:         "T1",
		Status:        "todo",
		Priority:      1,
		Executor:      "mock",
		LaunchProfile: "",
		AgentProfile:  "mock",
	})

	require.NoError(t, sched.Tick(context.Background()))

	// Set up REST API
	svc := service.New(store)
	handler := httpserver.New(svc, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/runs/1")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)
	assert.Contains(t, bodyStr, "openai")
	assert.Contains(t, bodyStr, "gpt-4")
	assert.Contains(t, bodyStr, "cli")
	assert.Contains(t, bodyStr, "pty")
	assert.Contains(t, bodyStr, "acceptEdits")
	assert.NotContains(t, bodyStr, "sentinel-api-key")
	assert.NotContains(t, bodyStr, "sentinel-system-prompt")
	assert.NotContains(t, bodyStr, "sentinel-args")
	assert.NotContains(t, bodyStr, "sentinel-env")
	assert.NotContains(t, bodyStr, "sentinel-url")

	// Set up MCP adapter
	adapter := mcpadapter.New(svc, sched)

	mcpRes, err := adapter.Server().CallTool(context.Background(), "torque_run_get", map[string]any{"id": float64(1)})
	require.NoError(t, err)

	// Since CallTool returns a JSON-RPC response map or string depending on implementation,
	// we can just format it to JSON and check for the string.
	importJSON := true
	_ = importJSON // Just ensuring encoding/json can be used
	mcpBytes, err := json.Marshal(mcpRes)
	require.NoError(t, err)

	mcpStr := string(mcpBytes)
	assert.Contains(t, mcpStr, "openai")
	assert.Contains(t, mcpStr, "gpt-4")
	assert.Contains(t, mcpStr, "cli")
	assert.Contains(t, mcpStr, "pty")
	assert.Contains(t, mcpStr, "acceptEdits")
	assert.NotContains(t, mcpStr, "sentinel-api-key")
	assert.NotContains(t, mcpStr, "sentinel-system-prompt")
	assert.NotContains(t, mcpStr, "sentinel-args")
	assert.NotContains(t, mcpStr, "sentinel-env")
	assert.NotContains(t, mcpStr, "sentinel-url")
}

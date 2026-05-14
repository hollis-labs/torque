package mcpadapter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupAdapterWithCatalog(t *testing.T) *mcpadapter.Adapter {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)

	catalogSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]modelsdev.Provider{
			"anthropic": {
				ID:   "anthropic",
				Name: "Anthropic",
				Models: map[string]modelsdev.Model{
					"claude-sonnet-4-6": {
						ID:           "claude-sonnet-4-6",
						Name:         "Claude Sonnet 4.6",
						Cost:         modelsdev.Pricing{Input: 3, Output: 15},
						Limit:        modelsdev.Limits{ContextWindow: 200000, MaxOutputTokens: 8192},
						Capabilities: modelsdev.Capabilities{ToolCall: true, Reasoning: true},
					},
				},
			},
			"openai": {
				ID:   "openai",
				Name: "OpenAI",
				Models: map[string]modelsdev.Model{
					"gpt-5": {ID: "gpt-5", Name: "GPT-5"},
				},
			},
		})
	}))
	t.Cleanup(catalogSrv.Close)

	svc.Models = modelcatalog.New(
		modelsdev.WithURL(catalogSrv.URL),
		modelsdev.WithHTTPClient(catalogSrv.Client()),
		modelsdev.WithCacheDir(t.TempDir()),
		modelsdev.WithCacheTTL(24*time.Hour),
	)
	require.NoError(t, svc.Models.Refresh(context.Background()))

	return mcpadapter.New(svc, nil)
}

func TestFullStack_ModelsList(t *testing.T) {
	a := setupAdapterWithCatalog(t)

	text, isErr := callTool(t, a, "torque_models_list", map[string]interface{}{})
	require.False(t, isErr, "got error: %s", text)

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []map[string]interface{} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &env))
	require.True(t, env.OK)
	assert.Len(t, env.Data.Items, 2)
}

func TestFullStack_ModelsList_FilterByProvider(t *testing.T) {
	a := setupAdapterWithCatalog(t)

	text, isErr := callTool(t, a, "torque_models_list", map[string]interface{}{
		"provider": "anthropic",
	})
	require.False(t, isErr, "got error: %s", text)

	var env struct {
		Data struct {
			Items []map[string]interface{} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &env))
	require.Len(t, env.Data.Items, 1)
	assert.Equal(t, "claude-sonnet-4-6", env.Data.Items[0]["id"])
	assert.Equal(t, true, env.Data.Items[0]["tool_call"])
}

func TestFullStack_ModelsGet(t *testing.T) {
	a := setupAdapterWithCatalog(t)

	text, isErr := callTool(t, a, "torque_models_get", map[string]interface{}{
		"provider": "anthropic",
		"model":    "claude-sonnet-4-6",
	})
	require.False(t, isErr, "got error: %s", text)

	var env struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &env))
	assert.Equal(t, "Claude Sonnet 4.6", env.Data["name"])
}

func TestFullStack_ModelsGet_NotFound(t *testing.T) {
	a := setupAdapterWithCatalog(t)

	text, isErr := callTool(t, a, "torque_models_get", map[string]interface{}{
		"provider": "anthropic",
		"model":    "missing",
	})
	require.True(t, isErr)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "not_found", code)
}

func TestFullStack_ModelsList_ColdCache(t *testing.T) {
	// Use the standard adapter (no Models wired) — list should return empty.
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_models_list", map[string]interface{}{})
	require.False(t, isErr, "got error: %s", text)

	var env struct {
		Data struct {
			Items []map[string]interface{} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &env))
	assert.Empty(t, env.Data.Items)
}

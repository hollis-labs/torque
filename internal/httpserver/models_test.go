package httpserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/httpserver"
	"github.com/hollis-labs/clockwork-manifold/internal/modelcatalog"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupTestServerWithCatalog(t *testing.T) *httptest.Server {
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
						ID:    "claude-sonnet-4-6",
						Name:  "Claude Sonnet 4.6",
						Cost:  modelsdev.Pricing{Input: 3, Output: 15},
						Limit: modelsdev.Limits{ContextWindow: 200000, MaxOutputTokens: 8192},
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

	handler := httpserver.New(svc, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestHTTP_ListModels(t *testing.T) {
	ts := setupTestServerWithCatalog(t)

	resp, err := http.Get(ts.URL + "/api/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Models []map[string]interface{} `json:"models"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Len(t, body.Models, 2)
}

func TestHTTP_ListModels_FilterByProvider(t *testing.T) {
	ts := setupTestServerWithCatalog(t)

	resp, err := http.Get(ts.URL + "/api/v1/models?provider=anthropic")
	require.NoError(t, err)
	defer resp.Body.Close()

	var body struct {
		Models []map[string]interface{} `json:"models"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Models, 1)
	assert.Equal(t, "claude-sonnet-4-6", body.Models[0]["id"])
	assert.Equal(t, "anthropic", body.Models[0]["provider_id"])
}

func TestHTTP_GetModel(t *testing.T) {
	ts := setupTestServerWithCatalog(t)

	resp, err := http.Get(ts.URL + "/api/v1/models/anthropic/claude-sonnet-4-6")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "Claude Sonnet 4.6", body["name"])
	assert.Equal(t, "anthropic", body["provider_id"])
	assert.Equal(t, "claude-sonnet-4-6", body["id"])
}

func TestHTTP_GetModel_NotFound(t *testing.T) {
	ts := setupTestServerWithCatalog(t)

	resp, err := http.Get(ts.URL + "/api/v1/models/anthropic/missing")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHTTP_ListModels_ColdCache(t *testing.T) {
	// No catalog wired in — should return empty list, not 503.
	ts := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Models []map[string]interface{} `json:"models"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Empty(t, body.Models)
}

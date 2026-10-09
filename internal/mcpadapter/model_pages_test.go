package mcpadapter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hollis-labs/torque/internal/service/pagination"

	"github.com/hollis-labs/substrate/llm-core/modelsdev"
	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/stretchr/testify/require"
)

func TestFullStack_ModelPagesProviderIdentityAndTotals(t *testing.T) {
	a, old, svc := setupAdjacentQueryParitySurfaces(t)
	old.Close()
	providers := map[string]modelsdev.Provider{}
	for _, id := range []string{"peer-a", "peer-b"} {
		models := map[string]modelsdev.Model{}
		for i := 0; i < 105; i++ {
			key := fmt.Sprintf("model-%03d", i)
			models[key] = modelsdev.Model{ID: key, Name: fmt.Sprintf("same-%02d", i%3), Cost: modelsdev.Pricing{Input: []float64{10, 0, 2}[i%3]}, Limit: modelsdev.Limits{ContextWindow: []int{10000, 0, 2000}[i%3], MaxOutputTokens: []int{1000, 0, 200}[i%3]}}
		}
		providers[id] = modelsdev.Provider{ID: id, Name: id, Models: models}
	}
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { require.NoError(t, json.NewEncoder(w).Encode(providers)) }))
	defer catalog.Close()
	svc.Models = modelcatalog.New(modelsdev.WithURL(catalog.URL), modelsdev.WithHTTPClient(catalog.Client()), modelsdev.WithCacheDir(t.TempDir()))
	require.NoError(t, svc.Models.Refresh(context.Background()))
	ts := httptest.NewServer(httpserver.New(svc, nil))
	defer ts.Close()
	identity := func(items []map[string]any) []string {
		out := []string{}
		for _, v := range items {
			out = append(out, fmt.Sprint(v["provider_id"])+":"+fmt.Sprint(v["id"]))
		}
		return out
	}
	params := url.Values{}
	args := map[string]any{}
	h := decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1/models")
	m := mcpAdjacentPage(t, a, "torque_models_list", args)
	require.Len(t, h.Items, 50)
	require.Len(t, m.Items, 50)
	require.Nil(t, h.Meta.Total)
	params.Set("limit", "999")
	params.Set("include_total", "true")
	args["limit"] = "999"
	args["include_total"] = "true"
	h = decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1/models?"+params.Encode())
	m = mcpAdjacentPage(t, a, "torque_models_list", args)
	require.Len(t, h.Items, 200)
	require.Equal(t, 210, *h.Meta.Total)
	require.Equal(t, identity(h.Items), identity(m.Items))
	for _, by := range []string{"name", "provider_id", "id", "cost", "context", "output"} {
		for _, dir := range []string{"asc", "desc"} {
			params.Del("cursor")
			delete(args, "cursor")
			params.Set("limit", "50")
			args["limit"] = "50"
			params.Set("sort_by", by)
			args["sort_by"] = by
			params.Set("sort_dir", dir)
			args["sort_dir"] = dir
			seen := map[string]bool{}
			var first []string
			var previous *float64
			zeros := 0
			for i := 0; i < 5; i++ {
				h = decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1/models?"+params.Encode())
				m = mcpAdjacentPage(t, a, "torque_models_list", args)
				require.Equal(t, identity(h.Items), identity(m.Items))
				if i == 0 {
					first = identity(h.Items)
				}
				for _, row := range h.Items {
					var value float64
					switch by {
					case "cost":
						value = row["cost"].(map[string]any)["input"].(float64)
					case "context":
						value = row["limit"].(map[string]any)["context"].(float64)
					case "output":
						value = row["limit"].(map[string]any)["output"].(float64)
					default:
						continue
					}
					if value == 0 {
						zeros++
					}
					if previous != nil {
						if dir == "asc" {
							require.GreaterOrEqual(t, value, *previous, by)
						} else {
							require.LessOrEqual(t, value, *previous, by)
						}
					} else if dir == "asc" {
						require.Zero(t, value, by)
					} else {
						require.Greater(t, value, float64(0), by)
					}
					copied := value
					previous = &copied
				}
				for _, id := range identity(h.Items) {
					require.False(t, seen[id])
					seen[id] = true
				}
				if !h.Meta.HasMore {
					break
				}
				params.Set("cursor", *h.Meta.NextCursor)
				args["cursor"] = *m.Meta.NextCursor
			}
			params.Del("cursor")
			delete(args, "cursor")
			params.Set("offset", "1")
			args["offset"] = "1"
			params.Set("limit", "49")
			args["limit"] = "49"
			h = decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1/models?"+params.Encode())
			m = mcpAdjacentPage(t, a, "torque_models_list", args)
			require.Equal(t, first[1:], identity(h.Items), by)
			require.Equal(t, identity(h.Items), identity(m.Items))
			params.Del("offset")
			delete(args, "offset")
			require.Len(t, seen, 210)
			if previous != nil {
				require.Equal(t, 70, zeros, by)
				if dir == "desc" {
					require.Zero(t, *previous, by)
				}
			}
		}
	}
	params.Del("cursor")
	delete(args, "cursor")
	params.Set("provider", "peer-a")
	args["provider"] = "peer-a"
	h = decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1/models?"+params.Encode())
	m = mcpAdjacentPage(t, a, "torque_models_list", args)
	require.Equal(t, 105, *h.Meta.Total)
	require.Equal(t, 105, *m.Meta.Total)
	params.Set("search", "missing")
	args["search"] = "missing"
	h = decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1/models?"+params.Encode())
	m = mcpAdjacentPage(t, a, "torque_models_list", args)
	require.Empty(t, h.Items)
	require.Equal(t, 0, *h.Meta.Total)
	require.Empty(t, m.Items)
}

func TestFullStack_ModelNumericCursorValidation(t *testing.T) {
	a, ts, _ := setupAdjacentQueryParitySurfaces(t)
	defer ts.Close()
	for _, test := range []struct{ by, value string }{{"cost", "NaN"}, {"cost", "Inf"}, {"cost", "bad"}, {"context", "1.5"}, {"output", "bad"}} {
		cursor := pagination.Encode(test.by, "asc", test.value, "[\"peer\",\"model\"]")
		resp, err := http.Get(ts.URL + "/api/v1/models?" + url.Values{"sort_by": {test.by}, "cursor": {cursor}}.Encode())
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, test.by)
		text, isErr := callTool(t, a, "torque_models_list", map[string]any{"sort_by": test.by, "cursor": cursor})
		require.True(t, isErr, text)
		require.Contains(t, text, "cursor")
	}
	cursor := pagination.Encode("cost", "asc", "0", "[\"peer\",\"model\"]")
	for _, args := range []map[string]any{{"sort_by": "context", "cursor": cursor}, {"sort_by": "cost", "sort_dir": "desc", "cursor": cursor}} {
		text, isErr := callTool(t, a, "torque_models_list", args)
		require.True(t, isErr, text)
		require.Contains(t, text, "cursor")
	}
}

package mcpadapter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
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
			models[key] = modelsdev.Model{ID: key, Name: fmt.Sprintf("same-%02d", i%3)}
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
	for _, by := range []string{"name", "provider_id", "id"} {
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
			for i := 0; i < 5; i++ {
				h = decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1/models?"+params.Encode())
				m = mcpAdjacentPage(t, a, "torque_models_list", args)
				require.Equal(t, identity(h.Items), identity(m.Items))
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
			require.Len(t, seen, 210)
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

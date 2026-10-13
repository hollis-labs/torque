package service_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
	"github.com/stretchr/testify/require"
)

func TestForumActivityPagesAndAuthoredTime(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("forums"))
	paths := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.Query().Get("cursor"))
		switch r.URL.Path {
		case "/forums/work":
			fmt.Fprintf(w, `{"forum":{"namespace":%q}}`, service.TorqueForumNamespace)
		case "/forums/work/topics":
			require.Equal(t, "id,post_count", r.URL.Query().Get("fields"))
			if r.URL.Query().Get("cursor") == "" {
				fmt.Fprint(w, `{"items":[{"id":"t1","post_count":2}],"has_more":true,"next_cursor":"topics-2"}`)
			} else {
				fmt.Fprint(w, `{"items":[{"id":"t2","post_count":1},{"id":"empty","post_count":0}]}`)
			}
		case "/forums/topics/t1/posts":
			require.Equal(t, "id,topic_id,created_at,provenance", r.URL.Query().Get("fields"))
			if r.URL.Query().Get("cursor") == "" {
				fmt.Fprint(w, `{"items":[{"id":"p2","topic_id":"t1","created_at":"2026-10-13T10:00:00Z","provenance":{"id":"Chris","kind":"user"}}],"has_more":true,"next_cursor":"posts-2"}`)
			} else {
				fmt.Fprint(w, `{"items":[{"id":"p1","topic_id":"t1","created_at":"2026-10-12T10:00:00Z","last_seq":9999,"provenance":{"id":"old"}}]}`)
			}
		case "/forums/topics/t2/posts":
			fmt.Fprint(w, `{"items":[{"id":"p3","topic_id":"t2","created_at":"2026-10-13T11:00:00Z","provenance":{"id":"latest","kind":"agent"}}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	svc.Forums.Client = tetherclient.NewForums(upstream.URL, "operator")
	activity, err := svc.Forums.Activity(context.Background(), "work")
	require.NoError(t, err)
	require.Equal(t, 3, activity.PostCount)
	require.Equal(t, "p3", activity.LastPost.ID)
	require.Equal(t, "latest", activity.LastPost.Provenance.ID)
	require.Contains(t, paths, "/forums/work/topics?topics-2")
	require.Contains(t, paths, "/forums/topics/t1/posts?posts-2")
}

func TestForumActivityEmptyAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, page string
		status                int
		wantError             bool
	}{
		{"empty", service.TorqueForumNamespace, `{"items":[]}`, 200, false},
		{"outside root", "app/other", `{"items":[]}`, 200, true},
		{"missing cursor", service.TorqueForumNamespace, `{"items":[],"has_more":true}`, 200, true},
		{"repeated cursor", service.TorqueForumNamespace, `{"items":[],"has_more":true,"next_cursor":"same"}`, 200, true},
		{"unavailable", service.TorqueForumNamespace, `{"error":"offline"}`, 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := setupService(t)
			require.NoError(t, svc.Feature.Enable("forums"))
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/forums/work" {
					fmt.Fprintf(w, `{"forum":{"namespace":%q}}`, tc.namespace)
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.page)
			}))
			defer upstream.Close()
			svc.Forums.Client = tetherclient.NewForums(upstream.URL, "operator")
			activity, err := svc.Forums.Activity(context.Background(), "work")
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Zero(t, activity.PostCount)
				require.Nil(t, activity.LastPost)
			}
			require.NoError(t, svc.Feature.Disable("forums"))
			_, err = svc.Forums.Activity(context.Background(), "work")
			var disabled *service.FeatureDisabledError
			require.ErrorAs(t, err, &disabled)
		})
	}
}

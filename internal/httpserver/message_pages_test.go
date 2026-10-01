package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/messaging"
	"github.com/stretchr/testify/require"
)

func TestHTTP_MessagePagesDrainAndReadContinuation(t *testing.T) {
	ts := setupMessagingServer(t)
	to := "msg://agent/test/paging"
	for i := 0; i < 205; i++ {
		resp, err := http.Post(ts.URL+"/api/v1/messages", "application/json", strings.NewReader(`{"kind":"notice","from":"msg://agent/test/sender","to":"`+to+`","thread_id":"paging"}`))
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, 201, resp.StatusCode)
	}
	get := func(path string) messaging.MessagePageEnvelope {
		resp, err := http.Get(ts.URL + "/api/v1" + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		var page messaging.MessagePageEnvelope
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
		return page
	}
	first := get("/messages/thread/paging?limit=999&include_total=true")
	require.Len(t, first.Items, 200)
	require.Equal(t, 205, *first.Meta.Total)
	require.True(t, first.Meta.HasMore)
	require.NotNil(t, first.Meta.NextCursor)
	last := get("/messages/thread/paging?limit=999&include_total=true&cursor=" + url.QueryEscape(*first.Meta.NextCursor))
	require.Len(t, last.Items, 5)
	require.Equal(t, 205, *last.Meta.Total)
	require.False(t, last.Meta.HasMore)
	drain := get("/messages/inbox?to=" + url.QueryEscape(to) + "&limit=999&include_total=true")
	require.Len(t, drain.Items, 200)
	require.Equal(t, 205, *drain.Meta.Total)
	require.True(t, drain.Meta.HasMore)
	require.Nil(t, drain.Meta.NextCursor)
	next := get("/messages/inbox?to=" + url.QueryEscape(to) + "&limit=999&include_total=true")
	require.Len(t, next.Items, 5)
	require.Equal(t, 5, *next.Meta.Total)
	require.False(t, next.Meta.HasMore)
	require.Nil(t, next.Meta.NextCursor)
	delivered := map[string]bool{}
	for _, v := range drain.Items {
		delivered[v.ID] = true
		require.NotNil(t, v.DeliveredAt)
	}
	for _, v := range next.Items {
		require.False(t, delivered[v.ID])
		require.NotNil(t, v.DeliveredAt)
	}
	for _, bad := range []string{"limit=-1", "sort_by=status", "include_total=maybe", "cursor=broken", "offset=1&cursor=broken"} {
		resp, err := http.Get(ts.URL + "/api/v1/messages/thread/paging?" + bad)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, 400, resp.StatusCode)
	}
	// Read history is still complete after delivery, and offset retains its metadata.
	offset := get("/messages/thread/paging?offset=200&limit=50&include_total=true")
	require.Len(t, offset.Items, 5)
	require.Equal(t, 205, *offset.Meta.Total)
	require.NotNil(t, offset.Meta.OffsetMeta)
	require.Equal(t, 200, offset.Meta.Offset)
	require.Nil(t, offset.Meta.NextOffset)
}

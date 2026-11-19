package tetherclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamVerbsOverHTTP(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer op-secret", r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" key="+r.Header.Get("Idempotency-Key"))
		switch r.URL.Path {
		case "/teams/form":
			var f TeamForm
			require.NoError(t, json.Unmarshal(body, &f))
			assert.Equal(t, 1, f.Launch.Counts["workers"])
			_, _ = w.Write([]byte(`{"run":{"id":"run-1","status":"working"}}`))
		case "/teams/delegate":
			var m map[string]string
			require.NoError(t, json.Unmarshal(body, &m))
			assert.Equal(t, "@orchestrator", m["address"])
			assert.Equal(t, "run-1", m["run_id"])
			_, _ = w.Write([]byte(`{}`))
		case "/teams/dissolve":
			_, _ = w.Write([]byte(`{}`))
		case "/teams/roster":
			assert.Equal(t, "run-1", r.URL.Query().Get("run_id"))
			_, _ = w.Write([]byte(`{"runs":[{"run":{"id":"run-1","status":"working"},"members":[{"id":"m1","slot":"workers","status":"stopped"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "op-secret")
	ctx := context.Background()

	res, err := c.FormTeam(ctx, "k1", TeamForm{Team: map[string]any{"id": "t"}, Launch: TeamLaunch{Counts: map[string]int{"workers": 1}}})
	require.NoError(t, err)
	assert.Equal(t, "run-1", res.RunID())
	_, err = c.DelegateTeam(ctx, "k1-delegate", "run-1", "@orchestrator", "brief")
	require.NoError(t, err)
	require.NoError(t, c.DissolveTeam(ctx, "k1-dissolve", "run-1"))
	info, err := c.TeamRun(ctx, "run-1")
	require.NoError(t, err)
	assert.True(t, info.Ended(), "a roster with no active member is ended")
	assert.Equal(t, []string{
		"POST /teams/form key=k1", "POST /teams/delegate key=k1-delegate",
		"POST /teams/dissolve key=k1-dissolve", "GET /teams/roster key=",
	}, seen)
}

func TestTeamErrorRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"denied","message":"token op-secret refused"}}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "op-secret").FormTeam(context.Background(), "k", TeamForm{})
	var te *TeamError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, 403, te.Status)
	assert.Equal(t, "denied", te.Code)
	assert.NotContains(t, err.Error(), "op-secret")
}

func TestTeamRunEndedByStatus(t *testing.T) {
	assert.True(t, TeamRunInfo{Status: "completed"}.Ended())
	assert.False(t, TeamRunInfo{Status: "working", Members: []TeamMember{{Status: "active"}}}.Ended())
	assert.False(t, TeamRunInfo{Status: "working"}.Ended())
	// The owner seat stays active after the agents stop.
	owner := TeamMember{Slot: "coordinator", Status: "active"}
	assert.True(t, TeamRunInfo{Status: "working", Members: []TeamMember{owner, {Slot: "workers", Status: "stopped"}}}.Ended())
	assert.False(t, TeamRunInfo{Status: "working", Members: []TeamMember{owner}}.Ended())
}

func TestTeamRunEndedRequiresEveryMemberStoppedOrReleased(t *testing.T) {
	owner := TeamMember{Slot: "coordinator", Status: "active"}
	mk := func(sts ...string) TeamRunInfo {
		r := TeamRunInfo{Status: "working", Members: []TeamMember{owner}}
		for _, s := range sts {
			r.Members = append(r.Members, TeamMember{Slot: "workers", Status: s})
		}
		return r
	}
	assert.True(t, mk("stopped", "released").Ended())
	assert.False(t, mk("stopped", "provisioning").Ended(), "provisioning is live")
	assert.False(t, mk("stopped", "starting-up").Ended(), "an unknown status is live")
	assert.False(t, mk("stopped", "active").Ended())
	assert.False(t, mk().Ended(), "no agent members is not ended")
}

func TestFindTeamRunPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "" {
			_, _ = w.Write([]byte(`{"runs":[{"run":{"id":"r1","team_id":"a"}}],"next_after":"r1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"runs":[{"run":{"id":"r2","team_id":"b"}}]}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "t")
	id, err := c.FindTeamRun(context.Background(), "b")
	require.NoError(t, err)
	assert.Equal(t, "r2", id)
	_, err = c.FindTeamRun(context.Background(), "zzz")
	assert.ErrorIs(t, err, ErrRunNotFound)
}

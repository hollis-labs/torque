package service_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/hollis-labs/torque/internal/tetherclient"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func seedWorkstream(t *testing.T, svc *service.Service, id, project string, priority int) {
	t.Helper()
	st := svc.Store()
	require.NoError(t, st.CreateTask(&sqlstore.TaskRecord{
		ID: id, Title: id, Status: "todo", Kind: "parent", Priority: priority, Executor: "cli",
	}))
	require.NoError(t, st.CreateTask(&sqlstore.TaskRecord{
		ID: id + "-c1", Title: "child", Status: "todo", Kind: "agent", Executor: "cli", Manual: true,
		ParentID: sqlNull(id),
	}))
	if project != "" {
		_, err := st.DB().Exec(`UPDATE tasks SET project_id = ? WHERE id = ?`, project, id)
		require.NoError(t, err)
	}
}

func TestWorkstreamClaimNext(t *testing.T) {
	ctx := context.Background()
	svc := setupService(t)
	st := svc.Store()
	goOn := false
	svc.Workstream.SetGo(func() bool { return goOn })
	svc.Task.SetSourceControl(&stubSC{state: "MERGED"})

	in := func(run, key string) service.ClaimInput {
		return service.ClaimInput{TeamID: "team-a", TeamRunID: run, Actor: "coord", IdempotencyKey: key}
	}

	_, err := st.DB().Exec(`INSERT INTO projects (id, name) VALUES ('P1','p1'), ('P2','p2')`)
	require.NoError(t, err)

	// Requires a run id.
	r, err := svc.Workstream.ClaimNext(ctx, service.ClaimInput{})
	require.NoError(t, err)
	require.Equal(t, service.ClaimRefuse, r.Outcome)

	// Paused: nothing launches even with cleared work.
	seedWorkstream(t, svc, "WS-1", "P1", 2)
	seedWorkstream(t, svc, "WS-2", "P1", 1)
	seedWorkstream(t, svc, "WS-3", "P2", 3)
	r, err = svc.Workstream.ClaimNext(ctx, in("run-1", "k1"))
	require.NoError(t, err)
	require.Equal(t, service.ClaimDefer, r.Outcome)
	require.Equal(t, service.DeferPaused, r.Reason)

	// Go: highest priority (lowest number) first, task moves to doing.
	goOn = true
	r, err = svc.Workstream.ClaimNext(ctx, in("run-1", "k1"))
	require.NoError(t, err)
	require.Equal(t, service.ClaimDispatch, r.Outcome)
	require.Equal(t, "WS-2", r.TaskID)
	got, _ := svc.Task.Get("WS-2")
	require.Equal(t, "doing", got.Status)

	// Same key returns the same claim, no second task taken.
	again, err := svc.Workstream.ClaimNext(ctx, in("run-1", "k1"))
	require.NoError(t, err)
	require.Equal(t, r.ClaimID, again.ClaimID)

	// Project cap (1): P1 is full, so the next team gets P2's workstream.
	r2, err := svc.Workstream.ClaimNext(ctx, in("run-2", "k2"))
	require.NoError(t, err)
	require.Equal(t, "WS-3", r2.TaskID)

	// Everything left is capped: defer with project_cap, not no_cleared_work.
	r3, err := svc.Workstream.ClaimNext(ctx, in("run-3", "k3"))
	require.NoError(t, err)
	require.Equal(t, service.ClaimDefer, r3.Outcome)
	require.Equal(t, service.DeferProjectCap, r3.Reason)

	// Pause again: a held claim does not stop, but no new work launches.
	goOn = false
	r4, _ := svc.Workstream.ClaimNext(ctx, in("run-4", "k4"))
	require.Equal(t, service.DeferPaused, r4.Reason)
	goOn = true

	// Heartbeat extends the lease.
	until, err := svc.Workstream.Heartbeat(ctx, r.ClaimID)
	require.NoError(t, err)
	require.True(t, until.After(time.Now()))

	// Handoff returns the task to the queue and frees the cap.
	rel, err := svc.Workstream.Release(ctx, r.ClaimID, service.ReleaseHandoff, "")
	require.NoError(t, err)
	require.Equal(t, "todo", rel.TaskStatus)
	_, err = svc.Workstream.Release(ctx, r.ClaimID, service.ReleaseHandoff, "")
	require.Error(t, err, "double release refused")
	r5, err := svc.Workstream.ClaimNext(ctx, in("run-5", "k5"))
	require.NoError(t, err)
	require.Equal(t, "WS-2", r5.TaskID)

	// Completed passes the landing gate; failed blocks.
	rel, err = svc.Workstream.Release(ctx, r5.ClaimID, service.ReleaseCompleted, `["pr"]`)
	require.NoError(t, err)
	require.Equal(t, "done", rel.TaskStatus)
	rel, err = svc.Workstream.Release(ctx, r2.ClaimID, service.ReleaseFailed, "")
	require.NoError(t, err)
	require.Equal(t, "blocked", rel.TaskStatus)

	// Bad outcome is a validation error.
	_, err = svc.Workstream.Release(ctx, r2.ClaimID, "bogus", "")
	require.Error(t, err)
}

func TestWorkstreamCompletedRespectsLandingGate(t *testing.T) {
	ctx := context.Background()
	svc := setupService(t)
	svc.Workstream.SetGo(func() bool { return true })
	svc.Task.SetSourceControl(&stubSC{state: "OPEN"})
	seedWorkstream(t, svc, "WS-L", "", 1)
	_, err := svc.Store().DB().Exec(`UPDATE tasks SET on_done_merge = 'pr', metadata = '{"landing":{"pr_url":"u"}}' WHERE id = 'WS-L'`)
	require.NoError(t, err)

	r, err := svc.Workstream.ClaimNext(ctx, service.ClaimInput{TeamRunID: "run-1"})
	require.NoError(t, err)
	require.Equal(t, service.ClaimDispatch, r.Outcome)
	rel, err := svc.Workstream.Release(ctx, r.ClaimID, service.ReleaseCompleted, "")
	require.NoError(t, err)
	require.Equal(t, "blocked", rel.TaskStatus)
	require.Contains(t, rel.Reason, "not MERGED")
}

func sqlNull(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

type fakeTether struct {
	cap    *tetherclient.Capacity
	capErr error
	runs   map[string]*tetherclient.RunState
	runErr error
}

func (f *fakeTether) Capacity(context.Context, string) (*tetherclient.Capacity, error) {
	return f.cap, f.capErr
}

func (f *fakeTether) Run(_ context.Context, id string) (*tetherclient.RunState, error) {
	if f.runErr != nil {
		return nil, f.runErr
	}
	if r, ok := f.runs[id]; ok {
		return r, nil
	}
	return nil, tetherclient.ErrRunNotFound
}

func seedProjects(t *testing.T, svc *service.Service, ids ...string) {
	t.Helper()
	for _, id := range ids {
		_, err := svc.Store().DB().Exec(`INSERT INTO projects (id, name) VALUES (?, ?)`, id, id)
		require.NoError(t, err)
	}
}

func TestWorkstreamClaimRespectsTetherCapacity(t *testing.T) {
	ctx := context.Background()
	svc := setupService(t)
	svc.Workstream.SetGo(func() bool { return true })
	seedProjects(t, svc, "P1", "P2")
	seedWorkstream(t, svc, "WS-1", "P1", 1)
	seedWorkstream(t, svc, "WS-2", "P2", 2)
	full := &fakeTether{cap: &tetherclient.Capacity{EnvironmentID: "agent-os", MaxSessions: 2, ActiveSessions: 2, Admission: "open"}}
	in := func(key string) service.ClaimInput {
		return service.ClaimInput{TeamRunID: "run-1", EnvironmentID: "agent-os", IdempotencyKey: key}
	}
	deferReason := func(f *fakeTether, key string) string {
		svc.Workstream.SetTether(f)
		r, err := svc.Workstream.ClaimNext(ctx, in(key))
		require.NoError(t, err)
		require.Equal(t, service.ClaimDefer, r.Outcome)
		return r.Reason
	}

	require.Equal(t, service.DeferEnvironmentCap, deferReason(full, "a"))
	require.Equal(t, service.DeferEnvironmentPaused, deferReason(&fakeTether{cap: &tetherclient.Capacity{Admission: "paused", Reason: "maintenance"}}, "b"))
	require.Equal(t, service.DeferCapacityUnknown, deferReason(&fakeTether{capErr: errors.New("boom")}, "c"), "fails closed")
	require.Equal(t, service.DeferCapacityUnknown, deferReason(&fakeTether{cap: &tetherclient.Capacity{EnvironmentID: "other", MaxSessions: 9, Admission: "open"}}, "d"), "wrong environment")

	// A claim with no environment_id does not consult Tether at all.
	svc.Workstream.SetTether(&fakeTether{capErr: errors.New("must not be called")})
	r, err := svc.Workstream.ClaimNext(ctx, service.ClaimInput{TeamRunID: "run-0"})
	require.NoError(t, err)
	require.Equal(t, service.ClaimDispatch, r.Outcome)
	require.Equal(t, "WS-1", r.TaskID)

	// Room in the environment: dispatch. Then the same key returns the same
	// lease even once the environment is full.
	svc.Workstream.SetTether(&fakeTether{cap: &tetherclient.Capacity{EnvironmentID: "agent-os", MaxSessions: 3, ActiveSessions: 1, Admission: "open"}})
	r, err = svc.Workstream.ClaimNext(ctx, in("e"))
	require.NoError(t, err)
	require.Equal(t, service.ClaimDispatch, r.Outcome)
	require.Equal(t, "WS-2", r.TaskID)
	svc.Workstream.SetTether(full)
	again, err := svc.Workstream.ClaimNext(ctx, in("e"))
	require.NoError(t, err)
	require.Equal(t, r.ClaimID, again.ClaimID)
}

func TestWorkstreamSweepExpiredClaims(t *testing.T) {
	ctx := context.Background()
	svc := setupService(t)
	svc.Workstream.SetGo(func() bool { return true })
	runs := []string{"run-ended", "run-active", "run-gone", "run-fresh"}
	for i, run := range runs {
		p := "P" + string(rune('1'+i))
		seedProjects(t, svc, p)
		seedWorkstream(t, svc, "WS-"+run, p, i+1)
	}
	claimIDs := map[string]string{}
	for _, run := range runs {
		r, err := svc.Workstream.ClaimNext(ctx, service.ClaimInput{TeamRunID: run})
		require.NoError(t, err)
		require.Equal(t, service.ClaimDispatch, r.Outcome)
		claimIDs[run] = r.ClaimID
	}
	for _, run := range []string{"run-ended", "run-active", "run-gone"} {
		_, err := svc.Store().DB().Exec(`UPDATE workstream_claims SET lease_expires_at = '2020-01-01 00:00:00' WHERE id = ?`, claimIDs[run])
		require.NoError(t, err)
	}
	status := func(run string) string {
		got, err := svc.Task.Get("WS-" + run)
		require.NoError(t, err)
		return got.Status
	}

	// Without a reader nothing is requeued: silence is not death.
	out, err := svc.Workstream.SweepExpiredClaims(ctx)
	require.NoError(t, err)
	require.Len(t, out, 3)
	for _, e := range out {
		require.Equal(t, service.SweepUnverified, e.Action)
	}
	require.Equal(t, "doing", status("run-ended"))

	// An unreadable Tether leaves everything alone.
	svc.Workstream.SetTether(&fakeTether{runErr: errors.New("tether down")})
	out, err = svc.Workstream.SweepExpiredClaims(ctx)
	require.NoError(t, err)
	for _, e := range out {
		require.Equal(t, service.SweepUnverified, e.Action)
	}

	// With run state: ended and missing runs requeue; a live run is left alone;
	// an unexpired lease is not looked at.
	svc.Workstream.SetTether(&fakeTether{runs: map[string]*tetherclient.RunState{
		"run-ended":  {Status: "dissolved", EndedAt: "2026-10-11T07:00:00Z"},
		"run-active": {Status: "running"},
		"run-fresh":  {Status: "running"},
	}})
	out, err = svc.Workstream.SweepExpiredClaims(ctx)
	require.NoError(t, err)
	actions := map[string]string{}
	for _, e := range out {
		actions[e.TeamRunID] = e.Action
	}
	require.Equal(t, map[string]string{
		"run-ended": service.SweepRequeued, "run-active": service.SweepRunActive, "run-gone": service.SweepRequeued,
	}, actions)
	require.Equal(t, "todo", status("run-ended"))
	require.Equal(t, "todo", status("run-gone"))
	require.Equal(t, "doing", status("run-active"))
	require.Equal(t, "doing", status("run-fresh"))

	// A requeued claim is released, so a second sweep does not touch it again.
	out, err = svc.Workstream.SweepExpiredClaims(ctx)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "run-active", out[0].TeamRunID)
}

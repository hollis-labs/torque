package service_test

import (
	"context"
	"database/sql"
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

package service_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/tetherclient"
	"github.com/stretchr/testify/require"
)

type fakeSessions map[string]string // id -> state; missing = 404

func (f fakeSessions) GetSession(_ context.Context, id string) (*tetherclient.SessionState, error) {
	st, ok := f[id]
	if !ok {
		return nil, &tetherclient.APIError{Status: http.StatusNotFound}
	}
	return &tetherclient.SessionState{ID: id, State: st}, nil
}

func setUnitRun(t *testing.T, svc *service.Service, id, run, state, deadline string) {
	_, e := svc.Store().ReadDB().Exec(`UPDATE tasks SET metadata=json_set(COALESCE(metadata,'{}'),'$.unit_run',json(?)) WHERE id=?`,
		fmt.Sprintf(`{"run_id":%q,"state":%q,"started_at":"2026-11-21T10:00:00Z","deadline":%q}`, run, state, deadline), id)
	require.NoError(t, e)
}

func TestAssignmentOwnerProjectionWhenNothingStronger(t *testing.T) {
	svc := setupService(t)
	task, e := svc.Task.Create(service.TaskCreateInput{Title: "owned", Metadata: map[string]any{"owner_urn": "msg://agent/agent-mux/agt_x", "owner_name": "X"}})
	require.NoError(t, e)
	a, e := svc.Assignment.Get(task)
	require.NoError(t, e)
	require.Equal(t, "owner", a.Source)
	require.Equal(t, "active", a.State)
	require.Equal(t, "msg://agent/agent-mux/agt_x", a.AssigneeURN)
	v := a.View()
	require.Nil(t, v["tether_session_id"])
	require.Nil(t, v["ended_at"])

	plain, e := svc.Task.Create(service.TaskCreateInput{Title: "nobody"})
	require.NoError(t, e)
	a, e = svc.Assignment.Get(plain)
	require.NoError(t, e)
	require.Nil(t, a)
}

func TestAssignmentUnitRunFailedExpiresAndKeepsHolder(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()
	var posted []map[string]any
	svc.Assignment.SetStatusSink(func(_ context.Context, tk *sqlstore.TaskRecord, a *service.TaskAssignment, text string) error {
		posted = append(posted, map[string]any{"task": tk.ID, "reason": a.EndReason, "urn": a.AssigneeURN, "text": text})
		return nil
	})
	task, e := svc.Task.Create(service.TaskCreateInput{Title: "unit work", Metadata: map[string]any{"ops_id": "ops-system-of-record"}})
	require.NoError(t, e)
	require.NoError(t, svc.Task.Transition(ctx, task.ID, "doing"))
	setUnitRun(t, svc, task.ID, "team-abc", "active", "2026-11-22T00:00:00Z")
	require.NoError(t, svc.Assignment.Reconcile(ctx))
	rec, _ := svc.Task.Get(task.ID)
	a, _ := svc.Assignment.Get(rec)
	require.Equal(t, "dispatch", a.Source)
	require.Equal(t, "active", a.State)
	require.Equal(t, "team-abc", a.TeamRunID)
	require.Equal(t, "2026-11-22T00:00:00Z", a.LeaseExpiresAt)

	setUnitRun(t, svc, task.ID, "team-abc", "failed", "2026-11-22T00:00:00Z")
	require.NoError(t, svc.Assignment.Reconcile(ctx))
	require.NoError(t, svc.Assignment.Reconcile(ctx)) // announced once
	rec, _ = svc.Task.Get(task.ID)
	a, _ = svc.Assignment.Get(rec)
	require.Equal(t, "expired", a.State)
	require.Equal(t, "session_ended", a.EndReason)
	require.Equal(t, "team-abc", a.TeamRunID, "the stale holder is kept")
	require.NotEmpty(t, a.EndedAt)

	require.Len(t, posted, 1)
	require.Equal(t, "session_ended", posted[0]["reason"])
	require.Equal(t, "msg://agent/agent-mux/team-orchestrator#team-abc", posted[0]["urn"])
	comments, e := svc.Store().ListComments(task.ID)
	require.NoError(t, e)
	require.Len(t, comments, 1)
	require.Contains(t, comments[0].Content, "Assignment expired")
}

func TestAssignmentLeaseLapsesAndDoneReleases(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	svc.Assignment.SetNowForTest(func() time.Time { return now })
	a1, _ := svc.Task.Create(service.TaskCreateInput{Title: "lapses"})
	a2, _ := svc.Task.Create(service.TaskCreateInput{Title: "finishes"})
	for _, id := range []string{a1.ID, a2.ID} {
		require.NoError(t, svc.Task.Transition(ctx, id, "doing"))
	}
	setUnitRun(t, svc, a1.ID, "team-1", "active", "2026-11-21T13:00:00Z")
	setUnitRun(t, svc, a2.ID, "team-2", "active", "2026-11-21T13:00:00Z")
	require.NoError(t, svc.Assignment.Reconcile(ctx))
	require.NoError(t, svc.Task.Transition(ctx, a2.ID, "review"))
	now = now.Add(2 * time.Hour)
	require.NoError(t, svc.Assignment.Reconcile(ctx))

	r1, _ := svc.Task.Get(a1.ID)
	g1, _ := svc.Assignment.Get(r1)
	require.Equal(t, "expired", g1.State)
	require.Equal(t, "lease_lapsed", g1.EndReason)
	r2, _ := svc.Task.Get(a2.ID)
	g2, _ := svc.Assignment.Get(r2)
	require.Equal(t, "released", g2.State)
	require.Equal(t, "done", g2.EndReason)

	// An expired holder is not resurrected while the unit record still says active.
	require.NoError(t, svc.Assignment.Reconcile(ctx))
	g1, _ = svc.Assignment.Get(r1)
	require.Equal(t, "expired", g1.State)
}

func TestAssignmentDeadSessionExpires(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()
	svc.Assignment.SetSessions(fakeSessions{"live-1": "running"})
	live, _ := svc.Task.Create(service.TaskCreateInput{Title: "live"})
	dead, _ := svc.Task.Create(service.TaskCreateInput{Title: "dead"})
	for _, tk := range []*sqlstore.TaskRecord{live, dead} {
		require.NoError(t, svc.Task.Transition(ctx, tk.ID, "doing"))
	}
	for id, sess := range map[string]string{live.ID: "live-1", dead.ID: "gone-2"} {
		run, e := svc.Store().CreateRun(&sqlstore.RunRecord{TaskID: id, Executor: "cli", Status: "running"})
		require.NoError(t, e)
		_, e = svc.Store().ReadDB().Exec(`UPDATE runs SET tether_session_id=? WHERE id=?`, sess, run)
		require.NoError(t, e)
	}
	require.NoError(t, svc.Assignment.Reconcile(ctx))
	require.NoError(t, svc.Assignment.Reconcile(ctx))
	rl, _ := svc.Task.Get(live.ID)
	gl, _ := svc.Assignment.Get(rl)
	require.Equal(t, "active", gl.State)
	require.Equal(t, "live-1", gl.TetherSessionID)
	rd, _ := svc.Task.Get(dead.ID)
	gd, _ := svc.Assignment.Get(rd)
	require.Equal(t, "expired", gd.State)
	require.Equal(t, "session_ended", gd.EndReason)
	require.Equal(t, "msg://session/local/gone-2", gd.AssigneeURN)
}

func TestAssignmentRecordsGatewayCallerOnDoingAndExpiresWithSession(t *testing.T) {
	svc := setupService(t)
	svc.Assignment.SetSessions(fakeSessions{"sess-live": "running"})
	task, e := svc.Task.Create(service.TaskCreateInput{Title: "hand-started"})
	require.NoError(t, e)
	anon, e := svc.Task.Create(service.TaskCreateInput{Title: "no caller"})
	require.NoError(t, e)
	ctx := service.WithCaller(context.Background(), service.Caller{AgentURN: "msg://agent/agent-mux/agt_worker", SessionID: "sess-live"})
	require.NoError(t, svc.Task.Transition(ctx, task.ID, "doing"))
	require.NoError(t, svc.Task.Transition(context.Background(), anon.ID, "doing"))

	rec, _ := svc.Task.Get(task.ID)
	a, _ := svc.Assignment.Get(rec)
	require.Equal(t, "doing", a.Source)
	require.Equal(t, "msg://agent/agent-mux/agt_worker", a.AssigneeURN)
	require.Equal(t, "sess-live", a.TetherSessionID)
	ra, _ := svc.Task.Get(anon.ID)
	a2, _ := svc.Assignment.Get(ra)
	require.Nil(t, a2, "no caller, no owner: unknown, not invented")

	require.NoError(t, svc.Assignment.Reconcile(context.Background()))
	a, _ = svc.Assignment.Get(rec)
	require.Equal(t, "active", a.State)
	svc.Assignment.SetSessions(fakeSessions{})
	require.NoError(t, svc.Assignment.Reconcile(context.Background()))
	a, _ = svc.Assignment.Get(rec)
	require.Equal(t, "expired", a.State)
	require.Equal(t, "session_ended", a.EndReason)
}

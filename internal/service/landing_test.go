package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/landing"
	"github.com/hollis-labs/torque/internal/service"
)

type stubSC struct{ state string }

func (s *stubSC) PR(context.Context, string) (landing.PRState, error) {
	return landing.PRState{URL: "u", State: s.state}, nil
}

func TestLandingGate(t *testing.T) {
	ctx := context.Background()
	svc := setupService(t)
	sc := &stubSC{state: "OPEN"}
	svc.Task.SetSourceControl(sc)

	mk := func(merge string, meta map[string]any) string {
		task, err := svc.Task.Create(service.TaskCreateInput{Title: "t", OnDoneMerge: merge, Metadata: meta})
		require.NoError(t, err)
		return task.ID
	}

	t.Run("no policy transitions freely", func(t *testing.T) {
		require.NoError(t, svc.Task.Transition(ctx, mk("none", nil), "done"))
	})

	t.Run("open PR blocks and escalates", func(t *testing.T) {
		id := mk("pr", map[string]any{"landing": map[string]any{"pr_url": "u"}})
		err := svc.Task.Transition(ctx, id, "done")
		var terr *service.TransitionError
		require.ErrorAs(t, err, &terr)
		got, _ := svc.Task.Get(id)
		require.Equal(t, "blocked", got.Status)
		require.Contains(t, got.BlockedReason, "not MERGED")
		comments, _ := svc.Comment.List("task", id)
		require.Len(t, comments, 1)
		require.Equal(t, "torque-landing", comments[0].Author)

		sc.state = "MERGED"
		require.NoError(t, svc.Task.Transition(ctx, id, "done"))
		sc.state = "OPEN"
	})

	t.Run("waiver passes", func(t *testing.T) {
		id := mk("pr", map[string]any{"landing": map[string]any{"waived_by": "chris", "waive_reason": "docs only"}})
		require.NoError(t, svc.Task.Transition(ctx, id, "done"))
	})

	t.Run("force overrides", func(t *testing.T) {
		id := mk("pr", nil)
		require.NoError(t, svc.Task.ForceTransition(ctx, id, "done"))
	})

	t.Run("with comment path is gated too", func(t *testing.T) {
		id := mk("pr", nil)
		require.Error(t, svc.Task.TransitionWithComment(ctx, id, "done", "finished", "agent", false))
		got, _ := svc.Task.Get(id)
		require.Equal(t, "blocked", got.Status)
	})
}

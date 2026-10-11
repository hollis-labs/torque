package service_test

import (
	"context"
	"database/sql"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"os"
	"os/exec"
	"path/filepath"
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

func TestQualityGateVocabulary(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Task.Create(service.TaskCreateInput{Title: "ok", QualityGates: []string{"deliverables", "subtodos", "ci_green"}})
	require.NoError(t, err)

	for _, bad := range []string{"verify", "tests-pass"} {
		_, err = svc.Task.Create(service.TaskCreateInput{Title: "bad", QualityGates: []string{bad}})
		var verr *service.ValidationError
		require.ErrorAs(t, err, &verr, bad)
		require.Equal(t, "quality_gates", verr.Field)
		require.Contains(t, verr.Message, "valid gates: deliverables, subtodos, ci_green")
	}

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "upd"})
	require.NoError(t, err)
	bogus := sql.NullString{String: `["nope"]`, Valid: true}
	err = svc.Task.Update(task.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{QualityGates: &bogus}})
	require.Error(t, err)
	good := sql.NullString{String: `["ci_green"]`, Valid: true}
	require.NoError(t, svc.Task.Update(task.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{QualityGates: &good}}))

	_, err = svc.Template.Create(service.TemplateCreateInput{ID: "tpl", Name: "t", QualityGates: []string{"nope"}})
	require.Error(t, err)
}

func TestLandingGate_DirtyWorkingDirBlocksEvenWhenMerged(t *testing.T) {
	ctx := context.Background()
	svc := setupService(t)
	svc.Task.SetSourceControl(&stubSC{state: "MERGED"})

	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-q", "-m", "x"}} {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	require.NoError(t, os.WriteFile(filepath.Join(repo, "left.txt"), []byte("x"), 0o644))

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title: "t", OnDoneMerge: "pr", WorkingDir: repo,
		Metadata: map[string]any{"landing": map[string]any{"pr_url": "u"}},
	})
	require.NoError(t, err)
	require.Error(t, svc.Task.Transition(ctx, task.ID, "done"))
	got, _ := svc.Task.Get(task.ID)
	require.Equal(t, "blocked", got.Status)
	require.Contains(t, got.BlockedReason, "uncommitted change")

	require.NoError(t, os.Remove(filepath.Join(repo, "left.txt")))
	require.NoError(t, svc.Task.Transition(ctx, task.ID, "done"))
}

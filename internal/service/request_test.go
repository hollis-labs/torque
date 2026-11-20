package service_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type requestCommentObserver struct{ count int }

func (o *requestCommentObserver) ObserveComment(_ context.Context, _ *sqlstore.CommentRecord) {
	o.count++
}

func TestRequestClaimSurvivesObserverReplacementAndTransitionWithComment(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()
	r, err := svc.Store().SubmitRequest(ctx, sqlstore.RequestSubmission{RequesterURN: "msg://user/local/owner", RecipientURN: "msg://agent/local/worker", IdempotencyKey: "claim", Subject: "Work"})
	require.NoError(t, err)
	old, replacement := &requestCommentObserver{}, &requestCommentObserver{}
	svc.Comment.SetObserver(old)
	// Bootstrap may replace observers. Request acknowledgment lives in the
	// transactional writer, so lifecycle behavior does not depend on composition.
	svc.Comment.SetObserver(replacement)
	svc.Task.SetCommentObserver(replacement)
	require.NoError(t, svc.Task.TransitionWithComment(ctx, r.TaskID, "doing", "STATUS started · checking", "worker", false))
	got, err := svc.Store().GetRequest(ctx, r.TaskID)
	require.NoError(t, err)
	require.NotNil(t, got.AckAt)
	assert.Equal(t, "acknowledged", got.State)
	assert.Zero(t, old.count)
	assert.Equal(t, 1, replacement.count)
	_, err = svc.Comment.AddForTask(r.TaskID, "worker", "STATUS started · retry")
	require.NoError(t, err)
	after, err := svc.Store().GetRequest(ctx, r.TaskID)
	require.NoError(t, err)
	assert.Equal(t, got, after)
	assert.Equal(t, 2, replacement.count)
}

func TestRequestLateStartedCannotReopenClosedRequest(t *testing.T) {
	for _, state := range []string{"done", "declined", "blocked"} {
		t.Run(state, func(t *testing.T) {
			svc := setupService(t)
			ctx := context.Background()
			r, err := svc.Store().SubmitRequest(ctx, sqlstore.RequestSubmission{RequesterURN: "msg://user/local/owner", RecipientURN: "msg://agent/local/worker", IdempotencyKey: state, Subject: "Work"})
			require.NoError(t, err)
			closed, err := svc.Store().TransitionRequest(ctx, r.TaskID, sqlstore.RequestTransition{State: state, Summary: "Final result"})
			require.NoError(t, err)
			before, err := svc.Task.Get(r.TaskID)
			require.NoError(t, err)
			_, err = svc.Comment.AddForTask(r.TaskID, "worker", "STATUS started · late message")
			require.NoError(t, err)
			after, err := svc.Store().GetRequest(ctx, r.TaskID)
			require.NoError(t, err)
			assert.Equal(t, closed, after)
			task, err := svc.Task.Get(r.TaskID)
			require.NoError(t, err)
			assert.Equal(t, before.Status, task.Status)
		})
	}
}

func TestRequestClaimCannotReopenIndependentlyTerminalTask(t *testing.T) {
	svc := setupService(t)
	ctx := context.Background()
	r, err := svc.Store().SubmitRequest(ctx, sqlstore.RequestSubmission{RequesterURN: "msg://user/local/owner", RecipientURN: "msg://agent/local/worker", IdempotencyKey: "terminal-task", Subject: "Work"})
	require.NoError(t, err)
	// Even a transition carrying STATUS started must preserve a terminal task.
	require.NoError(t, svc.Task.TransitionWithComment(ctx, r.TaskID, "done", "STATUS started · late", "worker", false))
	got, err := svc.Store().GetRequest(ctx, r.TaskID)
	require.NoError(t, err)
	assert.Nil(t, got.AckAt)
	task, err := svc.Task.Get(r.TaskID)
	require.NoError(t, err)
	assert.Equal(t, "done", task.Status)
	_, err = svc.Store().TransitionRequest(ctx, r.TaskID, sqlstore.RequestTransition{State: "acknowledged"})
	require.ErrorIs(t, err, sqlstore.ErrRequestConflict)
	// An actual final reply may still close the lifecycle without reopening it.
	_, err = svc.Store().TransitionRequest(ctx, r.TaskID, sqlstore.RequestTransition{State: "blocked", Summary: "Blocked final reply"})
	require.NoError(t, err)
	task, err = svc.Task.Get(r.TaskID)
	require.NoError(t, err)
	assert.Equal(t, "done", task.Status)
}

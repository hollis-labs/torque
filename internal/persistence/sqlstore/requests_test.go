package sqlstore_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requestTask(t *testing.T, s *sqlstore.Store, id string) sqlstore.RequestRegistration {
	t.Helper()
	task := sampleTask(id)
	task.Manual = true
	require.NoError(t, s.CreateTask(task))
	return sqlstore.RequestRegistration{RequesterURN: "msg://user/local/owner", RecipientURN: "msg://agent/local/worker", MailMessageID: "mail-" + id}
}

func TestRequestLifecycleDurableAndIdempotent(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	in := requestTask(t, s, "REQ-1")
	r, err := s.RegisterRequest(ctx, "REQ-1", in)
	require.NoError(t, err)
	assert.Equal(t, "submitted", r.State)
	assert.WithinDuration(t, r.SubmittedAt.Add(15*time.Minute), r.DueAckAt, time.Millisecond)
	again, err := s.RegisterRequest(ctx, "REQ-1", in)
	require.NoError(t, err)
	assert.Equal(t, r, again)
	claimed, err := s.TransitionRequest(ctx, r.TaskID, sqlstore.RequestTransition{State: "acknowledged"})
	require.NoError(t, err)
	require.NotNil(t, claimed.AckAt)
	working, err := s.TransitionRequest(ctx, r.TaskID, sqlstore.RequestTransition{State: "in_progress"})
	require.NoError(t, err)
	assert.Equal(t, claimed.AckAt, working.AckAt)
	done := sqlstore.RequestTransition{State: "done", Summary: "DONE · REQ-1\nOutcome: delivered", Author: in.RecipientURN}
	closed, err := s.TransitionRequest(ctx, r.TaskID, done)
	require.NoError(t, err)
	require.NotNil(t, closed.ClosedAt)
	retried, err := s.TransitionRequest(ctx, r.TaskID, done)
	require.NoError(t, err)
	assert.Equal(t, closed, retried)
	comments, err := s.ListCommentsForEntity(sqlstore.EntityTypeTask, r.TaskID)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	task, err := s.GetTask(r.TaskID)
	require.NoError(t, err)
	assert.Equal(t, "done", task.Status)
	var metadata map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(task.Metadata.String), &metadata))
	require.Contains(t, metadata, "request")
	var marker sqlstore.RequestRecord
	require.NoError(t, json.Unmarshal(metadata["request"], &marker))
	assert.Equal(t, *closed, marker)
	_, err = s.TransitionRequest(ctx, r.TaskID, sqlstore.RequestTransition{State: "in_progress"})
	require.ErrorIs(t, err, sqlstore.ErrRequestConflict)
	events, err := s.OutboxAfter(ctx, 0, 100)
	require.NoError(t, err)
	kinds := []string{}
	for _, e := range events {
		if len(e.Event.Kind) > 8 && string(e.Event.Kind)[:8] == "request." {
			kinds = append(kinds, string(e.Event.Kind))
			require.NoError(t, e.Event.Validate())
		}
	}
	assert.Equal(t, []string{"request.submitted", "request.acknowledged", "request.state_changed", "request.state_changed", "request.state_changed", "request.closed"}, kinds)
}

func TestRequestStartedCommentClaimsAtomically(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	in := requestTask(t, s, "REQ-C")
	_, err := s.RegisterRequest(ctx, "REQ-C", in)
	require.NoError(t, err)
	// Ordinary prose and non-task comments cannot claim.
	require.NoError(t, s.AddComment(&sqlstore.CommentRecord{EntityID: "REQ-C", Content: "Quoted STATUS started"}))
	r, err := s.GetRequest(ctx, "REQ-C")
	require.NoError(t, err)
	assert.Nil(t, r.AckAt)
	w, err := s.BeginWriteTx(ctx)
	require.NoError(t, err)
	require.NoError(t, w.AddComment(&sqlstore.CommentRecord{EntityID: "REQ-C", Content: "STATUS started · REQ-C\nWorking", Author: in.RecipientURN}))
	require.NoError(t, w.Rollback())
	r, err = s.GetRequest(ctx, "REQ-C")
	require.NoError(t, err)
	assert.Nil(t, r.AckAt)
	require.NoError(t, s.AddComment(&sqlstore.CommentRecord{EntityID: "REQ-C", Content: "STATUS started · REQ-C\nWorking", Author: in.RecipientURN}))
	r, err = s.GetRequest(ctx, "REQ-C")
	require.NoError(t, err)
	assert.Equal(t, "acknowledged", r.State)
	require.NotNil(t, r.AckAt)
}

func TestRequestAckAlertPagingAndRace(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"REQ-A", "REQ-B", "REQ-C"} {
		in := requestTask(t, s, id)
		in.DueAckAt = time.Now().Add(-time.Minute)
		_, err := s.RegisterRequest(ctx, id, in)
		require.NoError(t, err)
	}
	page, err := s.ListRequests(ctx, true, time.Now(), "", 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, "REQ-A", page[0].TaskID)
	page, err = s.ListRequests(ctx, true, time.Now(), page[0].TaskID, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, "REQ-B", page[0].TaskID)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.TransitionRequest(ctx, "REQ-A", sqlstore.RequestTransition{State: "unacknowledged_alert", Reason: "recipient_offline"})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	r, err := s.GetRequest(ctx, "REQ-A")
	require.NoError(t, err)
	assert.Equal(t, "submitted", r.State)
	require.NotNil(t, r.AlertedAt)
	_, err = s.TransitionRequest(ctx, "REQ-A", sqlstore.RequestTransition{State: "acknowledged"})
	require.NoError(t, err)
	events, err := s.OutboxAfter(ctx, 0, 100)
	require.NoError(t, err)
	alerts := 0
	for _, e := range events {
		if e.Event.Kind == "request.unacknowledged_alert" {
			alerts++
		}
	}
	assert.Equal(t, 1, alerts)
	page, err = s.ListRequests(ctx, true, time.Now(), "", 100)
	require.NoError(t, err)
	require.Len(t, page, 2)
}

func TestRequestRejectsInvalidAssociationAndRollsBack(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	in := requestTask(t, s, "REQ-X")
	_, err := s.RegisterRequest(ctx, "missing", in)
	require.ErrorIs(t, err, sqlstore.ErrRequestNotFound)
	_, err = s.RegisterRequest(ctx, "REQ-X", sqlstore.RequestRegistration{})
	require.ErrorIs(t, err, sqlstore.ErrRequestInvalid)
	r, err := s.RegisterRequest(ctx, "REQ-X", in)
	require.NoError(t, err)
	conflict := in
	conflict.RecipientURN = "msg://agent/local/other"
	_, err = s.RegisterRequest(ctx, r.TaskID, conflict)
	require.ErrorIs(t, err, sqlstore.ErrRequestConflict)
	in2 := requestTask(t, s, "REQ-Y")
	in2.MailMessageID = in.MailMessageID
	_, err = s.RegisterRequest(ctx, "REQ-Y", in2)
	require.ErrorIs(t, err, sqlstore.ErrRequestConflict)
	_, err = s.TransitionRequest(ctx, r.TaskID, sqlstore.RequestTransition{State: "done"})
	require.ErrorIs(t, err, sqlstore.ErrRequestInvalid)
	r, err = s.GetRequest(ctx, r.TaskID)
	require.NoError(t, err)
	assert.Nil(t, r.AckAt)
	assert.Equal(t, "submitted", r.State)
}

func TestRequestFastFinalReplyAndAckSuppressesAlert(t *testing.T) {
	for _, state := range []string{"done", "declined", "blocked"} {
		t.Run(state, func(t *testing.T) {
			s := setupTestStore(t)
			ctx := context.Background()
			in := requestTask(t, s, "REQ-FAST")
			_, err := s.RegisterRequest(ctx, "REQ-FAST", in)
			require.NoError(t, err)
			r, err := s.TransitionRequest(ctx, "REQ-FAST", sqlstore.RequestTransition{State: state, Summary: "Final result"})
			require.NoError(t, err)
			assert.Equal(t, state, r.State)
			require.NotNil(t, r.AckAt)
			require.NotNil(t, r.ClosedAt)
			_, err = s.TransitionRequest(ctx, "REQ-FAST", sqlstore.RequestTransition{State: "unacknowledged_alert"})
			require.NoError(t, err)
			r, err = s.GetRequest(ctx, "REQ-FAST")
			require.NoError(t, err)
			assert.Nil(t, r.AlertedAt)
			events, err := s.OutboxAfter(ctx, 0, 100)
			require.NoError(t, err)
			count := 0
			for _, e := range events {
				if e.Event.Kind == "request.acknowledged" {
					count++
					var payload map[string]any
					require.NoError(t, json.Unmarshal(e.Event.Payload, &payload))
					assert.Equal(t, "acknowledged", payload["state"])
				}
			}
			assert.Equal(t, 1, count)
		})
	}
}

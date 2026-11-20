package sqlstore_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleSubmission() sqlstore.RequestSubmission {
	return sqlstore.RequestSubmission{RequesterURN: "msg://user/local/owner", RecipientURN: "msg://agent/local/worker", SendKey: "sender-scoped-stable-key", Subject: "Check reports", Body: "Check yesterday's reports", Deliverable: "Report summary"}
}

func TestRequestSubmitBeforeSendAndAttach(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	in := sampleSubmission()
	r, err := s.SubmitRequest(ctx, in)
	require.NoError(t, err)
	require.NotEmpty(t, r.TaskID)
	assert.Empty(t, r.MailMessageID)
	assert.Equal(t, in.SendKey, r.SendKey)
	task, err := s.GetTask(r.TaskID)
	require.NoError(t, err)
	assert.True(t, task.Manual)
	assert.Equal(t, in.Subject, task.Title)
	assert.Contains(t, task.Description, in.Deliverable)
	online := false
	retry := in
	retry.RecipientOnline = &online
	again, err := s.SubmitRequest(ctx, retry)
	require.NoError(t, err)
	assert.Equal(t, r, again, "online snapshot must not move on retry")
	// A fast recipient may claim before the sender attaches the transport ID.
	require.NoError(t, s.AddComment(&sqlstore.CommentRecord{EntityID: r.TaskID, Content: "STATUS started · checking", Author: in.RecipientURN}))
	attached, err := s.AttachRequestMail(ctx, r.TaskID, "transport-generated-id")
	require.NoError(t, err)
	assert.Equal(t, "transport-generated-id", attached.MailMessageID)
	require.NotNil(t, attached.AckAt)
	assert.Equal(t, "acknowledged", attached.State)
	twice, err := s.AttachRequestMail(ctx, r.TaskID, attached.MailMessageID)
	require.NoError(t, err)
	assert.Equal(t, attached, twice)
	_, err = s.AttachRequestMail(ctx, r.TaskID, "different-id")
	require.ErrorIs(t, err, sqlstore.ErrRequestConflict)
	_, err = s.AttachRequestMail(ctx, r.TaskID, "")
	require.ErrorIs(t, err, sqlstore.ErrRequestInvalid)
	afterSend, err := s.SubmitRequest(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, attached, afterSend)
	events, err := s.OutboxAfter(ctx, 0, 100)
	require.NoError(t, err)
	submitted := 0
	for _, e := range events {
		if e.Event.Kind == "request.submitted" {
			submitted++
		}
	}
	assert.Equal(t, 1, submitted)
}

func TestRequestSubmitConcurrentRetriesAndImmutableIdentity(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	in := sampleSubmission()
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.SubmitRequest(ctx, in)
			assert.NoError(t, err)
			if r != nil {
				ids <- r.TaskID
			}
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		assert.Equal(t, first, id)
	}
	for _, change := range []func(*sqlstore.RequestSubmission){
		func(v *sqlstore.RequestSubmission) { v.RecipientURN = "msg://agent/local/other" },
		func(v *sqlstore.RequestSubmission) { v.Subject = "Changed" },
		func(v *sqlstore.RequestSubmission) { v.Body = "Changed" },
		func(v *sqlstore.RequestSubmission) { v.Deliverable = "Changed" },
		func(v *sqlstore.RequestSubmission) { v.TaskID = first },
	} {
		retry := in
		change(&retry)
		_, err := s.SubmitRequest(ctx, retry)
		require.ErrorIs(t, err, sqlstore.ErrRequestConflict)
	}
	differentSender := in
	differentSender.RequesterURN = "msg://user/local/other"
	other, err := s.SubmitRequest(ctx, differentSender)
	require.NoError(t, err)
	assert.NotEqual(t, first, other.TaskID)
	_, err = s.AttachRequestMail(ctx, first, "same-mail")
	require.NoError(t, err)
	_, err = s.AttachRequestMail(ctx, other.TaskID, "same-mail")
	require.ErrorIs(t, err, sqlstore.ErrRequestConflict)
}

func TestRequestSubmitOfflineExplicitLinkAndAtomicFailure(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	in := sampleSubmission()
	offline := false
	in.RecipientOnline = &offline
	in.DueAckAt = time.Now().Add(time.Hour)
	r, err := s.SubmitRequest(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, r.SubmittedAt, r.DueAckAt)
	assert.False(t, r.RecipientOnline)
	pending, err := s.ListRequests(ctx, true, time.Now(), "", 100)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	requestTask(t, s, "LINK-1")
	link := sampleSubmission()
	link.TaskID = "LINK-1"
	link.SendKey = "explicit-link"
	linked, err := s.SubmitRequest(ctx, link)
	require.NoError(t, err)
	assert.Equal(t, "LINK-1", linked.TaskID)
	changedKey := link
	changedKey.SendKey = "other-key"
	_, err = s.SubmitRequest(ctx, changedKey)
	require.ErrorIs(t, err, sqlstore.ErrRequestConflict)
	missing := link
	missing.TaskID = "MISSING"
	missing.SendKey = "missing-link"
	_, err = s.SubmitRequest(ctx, missing)
	require.ErrorIs(t, err, sqlstore.ErrRequestNotFound)
	// A failed transaction must not reserve the send key.
	missing.TaskID = ""
	recovered, err := s.SubmitRequest(ctx, missing)
	require.NoError(t, err)
	assert.NotEmpty(t, recovered.TaskID)
	multiple := in
	multiple.SendKey = "multi"
	multiple.RecipientURN += " ,msg://agent/local/other"
	_, err = s.SubmitRequest(ctx, multiple)
	require.ErrorIs(t, err, sqlstore.ErrRequestInvalid)
}

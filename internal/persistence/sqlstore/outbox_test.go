package sqlstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutboxAfter_FillsLogAssignedFieldsAndPages(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"CW-OBR-1", "CW-OBR-2", "CW-OBR-3"} {
		require.NoError(t, store.CreateTask(sampleTask(id)))
		require.NoError(t, store.TransitionTask(id, "doing"))
	}

	all, err := store.OutboxAfter(ctx, 0, 100)
	require.NoError(t, err)
	require.Len(t, all, 3)
	for _, r := range all {
		assert.EqualValues(t, r.Seq, r.Event.Sequence, "sequence is the outbox seq")
		assert.EqualValues(t, 1, r.Event.Generation)
		require.NotNil(t, r.Event.RecordedAt)
		require.NoError(t, r.Event.Validate(), "a stored event validates once the log has assigned sequence")
	}

	page, err := store.OutboxAfter(ctx, all[0].Seq, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, all[1].Seq, page[0].Seq)

	head, err := store.OutboxHead(ctx)
	require.NoError(t, err)
	assert.Equal(t, all[2].Seq, head)
}

func TestOutboxTrimBefore_RemovesOnlyTheHeadAndAdvancesPrunedThrough(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	require.NoError(t, store.CreateTask(sampleTask("CW-OBT-1")))
	require.NoError(t, store.TransitionTask("CW-OBT-1", "doing"))
	time.Sleep(15 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(15 * time.Millisecond)
	require.NoError(t, store.TransitionTask("CW-OBT-1", "review"))

	n, err := store.OutboxTrimBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	left, err := store.OutboxAfter(ctx, 0, 10)
	require.NoError(t, err)
	require.Len(t, left, 1)
	meta, err := store.OutboxMeta(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), meta.PrunedThrough, "the trim point is recorded so a reader behind it gets a gap")
	assert.Greater(t, left[0].Seq, meta.PrunedThrough)

	// Trimming never rewinds: a second trim with an older cutoff is a no-op.
	n, err = store.OutboxTrimBefore(ctx, cutoff.Add(-time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	head, err := store.OutboxHead(ctx)
	require.NoError(t, err)
	assert.Equal(t, left[0].Seq, head)
}

func TestOutboxSupported_SQLiteOnly(t *testing.T) {
	assert.True(t, setupTestStore(t).OutboxSupported())
}

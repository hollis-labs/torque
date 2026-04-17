package sqlstore_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubtodosEncodeDecodeRoundTrip(t *testing.T) {
	items := []sqlstore.Subtodo{
		{ID: "item-1", Text: "write test", Required: true, Done: false},
		{ID: "item-2", Text: "ship code", Required: true, Done: true, Evidence: "commit-abc"},
	}
	ns, err := sqlstore.EncodeSubtodos(items)
	require.NoError(t, err)
	require.True(t, ns.Valid)

	got, err := sqlstore.DecodeSubtodos(ns)
	require.NoError(t, err)
	assert.Equal(t, items, got)
}

func TestSubtodosEncodeEmptyReturnsInvalid(t *testing.T) {
	ns, err := sqlstore.EncodeSubtodos(nil)
	require.NoError(t, err)
	assert.False(t, ns.Valid)

	got, err := sqlstore.DecodeSubtodos(sql.NullString{})
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSetGetSubtodos(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-20260417-0100")
	require.NoError(t, store.CreateTask(task))

	got, err := store.GetSubtodos(task.ID)
	require.NoError(t, err)
	assert.Nil(t, got)

	items := []sqlstore.Subtodo{
		{ID: "a", Text: "first", Required: true},
		{ID: "b", Text: "second"},
	}
	require.NoError(t, store.SetSubtodos(task.ID, items))

	got, err = store.GetSubtodos(task.ID)
	require.NoError(t, err)
	assert.Equal(t, items, got)
}

func TestClearSubtodos(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-20260417-0101")
	require.NoError(t, store.CreateTask(task))
	require.NoError(t, store.SetSubtodos(task.ID, []sqlstore.Subtodo{{ID: "x", Text: "x"}}))

	require.NoError(t, store.ClearSubtodos(task.ID))

	got, err := store.GetSubtodos(task.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSetSubtodoDoneMarksItemAndRecordsEvidence(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-20260417-0102")
	require.NoError(t, store.CreateTask(task))

	items := []sqlstore.Subtodo{
		{ID: "item-1", Text: "do thing", Required: true},
		{ID: "item-2", Text: "other", Required: false},
	}
	require.NoError(t, store.SetSubtodos(task.ID, items))

	require.NoError(t, store.SetSubtodoDone(task.ID, "item-1", "commit-abc123"))

	got, err := store.GetSubtodos(task.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.True(t, got[0].Done)
	assert.Equal(t, "commit-abc123", got[0].Evidence)
	assert.False(t, got[1].Done)
}

func TestSetSubtodoDoneUnknownItem(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-20260417-0103")
	require.NoError(t, store.CreateTask(task))
	require.NoError(t, store.SetSubtodos(task.ID, []sqlstore.Subtodo{{ID: "x", Text: "x"}}))

	err := store.SetSubtodoDone(task.ID, "missing", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestSubtodosOnUnknownTask(t *testing.T) {
	store := setupTestStore(t)
	_, err := store.GetSubtodos("CW-19991231-9999")
	assert.True(t, errors.Is(err, sqlstore.ErrTaskNotFound))

	err = store.SetSubtodos("CW-19991231-9999", []sqlstore.Subtodo{{ID: "x", Text: "x"}})
	assert.True(t, errors.Is(err, sqlstore.ErrTaskNotFound))
}

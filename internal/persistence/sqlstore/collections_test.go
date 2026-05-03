package sqlstore_test

import (
	"errors"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper: create a task with a given ID inside the test store. Uses sampleTask
// to keep facet defaults consistent with the rest of the test suite.
func collTestTask(t *testing.T, store *sqlstore.Store, id string) {
	t.Helper()
	require.NoError(t, store.CreateTask(sampleTask(id)))
}

func TestCreateAndGetCollection(t *testing.T) {
	store := setupTestStore(t)

	id, err := store.NextCollectionID()
	require.NoError(t, err)
	require.Contains(t, id, "COL-")

	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{
		ID:          id,
		Name:        "Roadmap",
		Description: "Quarterly themes",
	}))

	got, err := store.GetCollection(id)
	require.NoError(t, err)
	assert.Equal(t, "Roadmap", got.Name)
	assert.Equal(t, "Quarterly themes", got.Description)
	assert.False(t, got.ArchivedAt.Valid, "fresh collection should not be archived")
}

func TestGetCollectionNotFound(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.GetCollection("COL-DOES-NOT-EXIST")
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrCollectionNotFound),
		"err should wrap ErrCollectionNotFound: %v", err)
}

func TestNextCollectionIDIsSequential(t *testing.T) {
	store := setupTestStore(t)

	id1, err := store.NextCollectionID()
	require.NoError(t, err)
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: id1, Name: "A"}))

	id2, err := store.NextCollectionID()
	require.NoError(t, err)
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: id2, Name: "B"}))

	assert.NotEqual(t, id1, id2, "subsequent IDs must differ")
}

func TestListCollectionsActiveVsArchived(t *testing.T) {
	store := setupTestStore(t)

	id1, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: id1, Name: "Active 1"}))

	id2, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: id2, Name: "Will archive"}))
	require.NoError(t, store.ArchiveCollection(id2))

	active, err := store.ListCollections(sqlstore.CollectionFilter{Status: "active"})
	require.NoError(t, err)
	assert.Len(t, active, 1)
	assert.Equal(t, id1, active[0].ID)

	archived, err := store.ListCollections(sqlstore.CollectionFilter{Status: "archived"})
	require.NoError(t, err)
	assert.Len(t, archived, 1)
	assert.Equal(t, id2, archived[0].ID)
	assert.True(t, archived[0].ArchivedAt.Valid)

	all, err := store.ListCollections(sqlstore.CollectionFilter{Status: "all"})
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestUpdateCollection(t *testing.T) {
	store := setupTestStore(t)

	id, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: id, Name: "Old"}))

	newName := "New"
	newDesc := "Updated description"
	require.NoError(t, store.UpdateCollection(id, sqlstore.CollectionUpdate{
		Name:        &newName,
		Description: &newDesc,
	}))

	got, err := store.GetCollection(id)
	require.NoError(t, err)
	assert.Equal(t, "New", got.Name)
	assert.Equal(t, "Updated description", got.Description)
}

func TestArchiveAndUnarchiveCollection(t *testing.T) {
	store := setupTestStore(t)

	id, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: id, Name: "C"}))

	require.NoError(t, store.ArchiveCollection(id))
	got, err := store.GetCollection(id)
	require.NoError(t, err)
	assert.True(t, got.ArchivedAt.Valid)

	require.NoError(t, store.UnarchiveCollection(id))
	got, err = store.GetCollection(id)
	require.NoError(t, err)
	assert.False(t, got.ArchivedAt.Valid, "unarchive should clear archived_at")
}

func TestAddTaskToCollectionAppendsAndStampsInbox(t *testing.T) {
	store := setupTestStore(t)

	colID, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colID, Name: "Bucket"}))

	collTestTask(t, store, "T-A")
	collTestTask(t, store, "T-B")

	require.NoError(t, store.AddTaskToCollection("T-A", colID, 0))
	require.NoError(t, store.AddTaskToCollection("T-B", colID, 0))

	tA, err := store.GetTask("T-A")
	require.NoError(t, err)
	assert.True(t, tA.CollectionID.Valid)
	assert.Equal(t, colID, tA.CollectionID.String)
	assert.True(t, tA.CollectionPosition.Valid)
	assert.Equal(t, int64(1), tA.CollectionPosition.Int64)
	assert.True(t, tA.AddedToCollectionsAt.Valid, "added_to_collections_at should be set on first add")

	tB, err := store.GetTask("T-B")
	require.NoError(t, err)
	assert.Equal(t, int64(2), tB.CollectionPosition.Int64, "second append should land at position 2")
}

func TestAddTaskToCollectionExplicitPosition(t *testing.T) {
	store := setupTestStore(t)

	colID, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colID, Name: "Bucket"}))
	collTestTask(t, store, "T-A")

	require.NoError(t, store.AddTaskToCollection("T-A", colID, 5))

	tA, err := store.GetTask("T-A")
	require.NoError(t, err)
	assert.Equal(t, int64(5), tA.CollectionPosition.Int64)
}

func TestRemoveTaskFromCollectionPreservesInbox(t *testing.T) {
	store := setupTestStore(t)

	colID, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colID, Name: "Bucket"}))
	collTestTask(t, store, "T-A")
	require.NoError(t, store.AddTaskToCollection("T-A", colID, 0))

	require.NoError(t, store.RemoveTaskFromCollection("T-A"))

	tA, err := store.GetTask("T-A")
	require.NoError(t, err)
	assert.False(t, tA.CollectionID.Valid, "collection_id should be cleared")
	assert.False(t, tA.CollectionPosition.Valid, "collection_position should be cleared")
	assert.True(t, tA.AddedToCollectionsAt.Valid, "added_to_collections_at must be preserved (write-once)")
}

func TestReorderCollectionTasks(t *testing.T) {
	store := setupTestStore(t)

	colID, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colID, Name: "Bucket"}))

	for _, id := range []string{"T-1", "T-2", "T-3"} {
		collTestTask(t, store, id)
		require.NoError(t, store.AddTaskToCollection(id, colID, 0))
	}

	// Reverse order: T-3, T-2, T-1.
	require.NoError(t, store.ReorderCollectionTasks(colID, []string{"T-3", "T-2", "T-1"}))

	got, err := store.ListCollectionTasks(colID)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "T-3", got[0].ID)
	assert.Equal(t, "T-2", got[1].ID)
	assert.Equal(t, "T-1", got[2].ID)
	assert.Equal(t, int64(1), got[0].CollectionPosition.Int64)
	assert.Equal(t, int64(2), got[1].CollectionPosition.Int64)
	assert.Equal(t, int64(3), got[2].CollectionPosition.Int64)
}

func TestReorderRejectsForeignTask(t *testing.T) {
	store := setupTestStore(t)

	colA, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colA, Name: "A"}))
	colB, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colB, Name: "B"}))

	collTestTask(t, store, "T-X")
	require.NoError(t, store.AddTaskToCollection("T-X", colA, 0))
	collTestTask(t, store, "T-Y")
	require.NoError(t, store.AddTaskToCollection("T-Y", colB, 0))

	// Reorder colA with a task belonging to colB → must fail loud.
	err := store.ReorderCollectionTasks(colA, []string{"T-X", "T-Y"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "belong to collection")
}

func TestMoveTaskToCollection(t *testing.T) {
	store := setupTestStore(t)

	colA, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colA, Name: "A"}))
	colB, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colB, Name: "B"}))

	collTestTask(t, store, "T-MOVE")
	require.NoError(t, store.AddTaskToCollection("T-MOVE", colA, 0))

	require.NoError(t, store.MoveTaskToCollection("T-MOVE", colB, 0))

	got, err := store.GetTask("T-MOVE")
	require.NoError(t, err)
	assert.Equal(t, colB, got.CollectionID.String)
	assert.Equal(t, int64(1), got.CollectionPosition.Int64,
		"first task in target collection should be at position 1")
	assert.True(t, got.AddedToCollectionsAt.Valid)
}

func TestAddTaskToInboxAndListInbox(t *testing.T) {
	store := setupTestStore(t)

	collTestTask(t, store, "T-INBOX-1")
	collTestTask(t, store, "T-INBOX-2")
	collTestTask(t, store, "T-LEGACY") // never added — must NOT appear

	require.NoError(t, store.AddTaskToInbox("T-INBOX-1"))
	require.NoError(t, store.AddTaskToInbox("T-INBOX-2"))

	inbox, err := store.ListInboxTasks()
	require.NoError(t, err)
	require.Len(t, inbox, 2)

	ids := []string{inbox[0].ID, inbox[1].ID}
	assert.Contains(t, ids, "T-INBOX-1")
	assert.Contains(t, ids, "T-INBOX-2")
	assert.NotContains(t, ids, "T-LEGACY", "legacy tasks (added_to_collections_at IS NULL) must not appear in inbox view")
}

func TestAddTaskToInboxIsIdempotentAndWriteOnce(t *testing.T) {
	store := setupTestStore(t)

	collTestTask(t, store, "T-IDEM")

	require.NoError(t, store.AddTaskToInbox("T-IDEM"))
	first, err := store.GetTask("T-IDEM")
	require.NoError(t, err)
	require.True(t, first.AddedToCollectionsAt.Valid)
	firstStamp := first.AddedToCollectionsAt.Time

	// Second add: must NOT change the timestamp (write-once).
	require.NoError(t, store.AddTaskToInbox("T-IDEM"))
	second, err := store.GetTask("T-IDEM")
	require.NoError(t, err)
	assert.True(t, second.AddedToCollectionsAt.Valid)
	assert.True(t, second.AddedToCollectionsAt.Time.Equal(firstStamp),
		"added_to_collections_at must be write-once on first call")
}

func TestRemoveFromCollectionReturnsToInbox(t *testing.T) {
	store := setupTestStore(t)

	colID, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colID, Name: "Bucket"}))

	collTestTask(t, store, "T-RT")
	require.NoError(t, store.AddTaskToCollection("T-RT", colID, 0))

	// Should NOT appear in inbox while in a collection.
	inbox, err := store.ListInboxTasks()
	require.NoError(t, err)
	assert.Empty(t, inbox)

	// After removal, it returns to inbox.
	require.NoError(t, store.RemoveTaskFromCollection("T-RT"))
	inbox, err = store.ListInboxTasks()
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	assert.Equal(t, "T-RT", inbox[0].ID)
}

func TestListCollectionTasksOrdered(t *testing.T) {
	store := setupTestStore(t)

	colID, _ := store.NextCollectionID()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: colID, Name: "C"}))

	// Insert positions out of order; ListCollectionTasks must reorder ASC.
	collTestTask(t, store, "T-3")
	require.NoError(t, store.AddTaskToCollection("T-3", colID, 3))
	collTestTask(t, store, "T-1")
	require.NoError(t, store.AddTaskToCollection("T-1", colID, 1))
	collTestTask(t, store, "T-2")
	require.NoError(t, store.AddTaskToCollection("T-2", colID, 2))

	got, err := store.ListCollectionTasks(colID)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "T-1", got[0].ID)
	assert.Equal(t, "T-2", got[1].ID)
	assert.Equal(t, "T-3", got[2].ID)
}

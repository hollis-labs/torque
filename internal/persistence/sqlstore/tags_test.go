package sqlstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleTag(slug string) *sqlstore.TagRecord {
	return &sqlstore.TagRecord{
		Slug:        slug,
		Name:        slug,
		Description: "",
		Color:       "zinc",
	}
}

func TestCreateAndGetTag(t *testing.T) {
	store := setupTestStore(t)

	tag := sampleTag("bug")
	tag.Name = "Bug"
	tag.Description = "Something is broken"
	tag.Color = "red"

	require.NoError(t, store.CreateTag(tag))

	got, err := store.GetTag("bug")
	require.NoError(t, err)
	assert.Equal(t, "bug", got.Slug)
	assert.Equal(t, "Bug", got.Name)
	assert.Equal(t, "Something is broken", got.Description)
	assert.Equal(t, "red", got.Color)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestGetTagNotFound(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.GetTag("nonexistent")
	assert.Error(t, err)
}

func TestListTagsEmpty(t *testing.T) {
	store := setupTestStore(t)

	tags, err := store.ListTags()
	require.NoError(t, err)
	assert.Empty(t, tags)
}

func TestListTagsSortedByName(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "ui", Name: "UI", Color: "zinc"}))
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "bug", Name: "Bug", Color: "red"}))
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "frontend", Name: "Frontend", Color: "blue"}))

	tags, err := store.ListTags()
	require.NoError(t, err)
	require.Len(t, tags, 3)
	assert.Equal(t, "Bug", tags[0].Name)
	assert.Equal(t, "Frontend", tags[1].Name)
	assert.Equal(t, "UI", tags[2].Name)

	// CreatedAt/UpdatedAt are populated
	assert.WithinDuration(t, time.Now(), tags[0].CreatedAt, 5*time.Second)
}

func TestUpdateTag(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{
		Slug: "bug", Name: "Bug", Description: "orig", Color: "zinc",
	}))

	newName := "Bug Report"
	newColor := "red"
	require.NoError(t, store.UpdateTag("bug", sqlstore.TagUpdate{
		Name:  &newName,
		Color: &newColor,
	}))

	got, err := store.GetTag("bug")
	require.NoError(t, err)
	assert.Equal(t, "Bug Report", got.Name)
	assert.Equal(t, "red", got.Color)
	assert.Equal(t, "orig", got.Description) // unchanged
	assert.True(t, got.UpdatedAt.After(got.CreatedAt) || got.UpdatedAt.Equal(got.CreatedAt))
}

func TestUpdateTagNotFound(t *testing.T) {
	store := setupTestStore(t)

	newName := "x"
	err := store.UpdateTag("nope", sqlstore.TagUpdate{Name: &newName})
	assert.Error(t, err)
}

func TestUpdateTagNoChanges(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "bug", Name: "Bug", Color: "zinc"}))

	// Empty update should be a no-op, not an error
	err := store.UpdateTag("bug", sqlstore.TagUpdate{})
	assert.NoError(t, err)
}

func TestDeleteTag(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "bug", Name: "Bug", Color: "zinc"}))

	require.NoError(t, store.DeleteTag("bug"))

	_, err := store.GetTag("bug")
	assert.Error(t, err)
}

func TestDeleteTagNotFound(t *testing.T) {
	store := setupTestStore(t)

	err := store.DeleteTag("nope")
	assert.Error(t, err)
}

// seedTaskAndTags is a test helper that creates a minimal task row and the
// referenced tag rows. Uses the sampleTask() helper from tasks_test.go (same
// external test package) to create the task via store.CreateTask. Tag creation
// is idempotent: if a tag already exists it is left alone, so the helper can
// be called multiple times with overlapping tag slugs.
func seedTaskAndTags(t *testing.T, store *sqlstore.Store, taskID string, tagSlugs ...string) {
	t.Helper()
	task := sampleTask(taskID)
	require.NoError(t, store.CreateTask(task))
	for _, slug := range tagSlugs {
		if _, err := store.GetTag(slug); err == nil {
			continue
		}
		require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: slug, Name: slug, Color: "zinc"}))
	}
}

func TestSetTaskTagsReplaceAll(t *testing.T) {
	store := setupTestStore(t)
	seedTaskAndTags(t, store, "CW-0001", "bug", "ui", "frontend")

	require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug", "ui"}))

	linked, err := store.ListTaskTags("CW-0001")
	require.NoError(t, err)
	require.Len(t, linked, 2)
	assert.Equal(t, "bug", linked[0].Slug)
	assert.Equal(t, "ui", linked[1].Slug)

	// Replace with a different set
	require.NoError(t, store.SetTaskTags("CW-0001", []string{"frontend"}))
	linked, err = store.ListTaskTags("CW-0001")
	require.NoError(t, err)
	require.Len(t, linked, 1)
	assert.Equal(t, "frontend", linked[0].Slug)
}

func TestSetTaskTagsPreservesOrder(t *testing.T) {
	store := setupTestStore(t)
	seedTaskAndTags(t, store, "CW-0001", "z-zzz", "a-aaa", "m-mmm")

	// Explicitly non-alphabetical order
	require.NoError(t, store.SetTaskTags("CW-0001", []string{"z-zzz", "a-aaa", "m-mmm"}))

	linked, err := store.ListTaskTags("CW-0001")
	require.NoError(t, err)
	require.Len(t, linked, 3)
	assert.Equal(t, "z-zzz", linked[0].Slug)
	assert.Equal(t, "a-aaa", linked[1].Slug)
	assert.Equal(t, "m-mmm", linked[2].Slug)
}

func TestSetTaskTagsEmpty(t *testing.T) {
	store := setupTestStore(t)
	seedTaskAndTags(t, store, "CW-0001", "bug")

	require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug"}))
	require.NoError(t, store.SetTaskTags("CW-0001", []string{}))

	linked, err := store.ListTaskTags("CW-0001")
	require.NoError(t, err)
	assert.Empty(t, linked)
}

func TestListTaskTagsEmpty(t *testing.T) {
	store := setupTestStore(t)
	seedTaskAndTags(t, store, "CW-0001")

	linked, err := store.ListTaskTags("CW-0001")
	require.NoError(t, err)
	assert.Empty(t, linked)
}

func TestDeleteTagCascadesTaskTags(t *testing.T) {
	store := setupTestStore(t)
	seedTaskAndTags(t, store, "CW-0001", "bug")
	require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug"}))

	require.NoError(t, store.DeleteTag("bug"))

	linked, err := store.ListTaskTags("CW-0001")
	require.NoError(t, err)
	assert.Empty(t, linked)
}

func TestMergeTagsMovesLinks(t *testing.T) {
	store := setupTestStore(t)
	seedTaskAndTags(t, store, "CW-0001", "bug", "defect")
	seedTaskAndTags(t, store, "CW-0002", "bug", "defect")

	// Task 1 has only "bug"
	require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug"}))
	// Task 2 has only "defect"
	require.NoError(t, store.SetTaskTags("CW-0002", []string{"defect"}))

	// Merge bug into defect
	require.NoError(t, store.MergeTags("bug", "defect"))

	// Source tag is gone
	_, err := store.GetTag("bug")
	assert.Error(t, err)

	// Task 1 now has "defect" (the rewritten row)
	linked1, err := store.ListTaskTags("CW-0001")
	require.NoError(t, err)
	require.Len(t, linked1, 1)
	assert.Equal(t, "defect", linked1[0].Slug)

	// Task 2 still has "defect"
	linked2, err := store.ListTaskTags("CW-0002")
	require.NoError(t, err)
	require.Len(t, linked2, 1)
	assert.Equal(t, "defect", linked2[0].Slug)
}

func TestMergeTagsDeduplicates(t *testing.T) {
	store := setupTestStore(t)
	seedTaskAndTags(t, store, "CW-0001", "bug", "defect")

	// Task has BOTH bug and defect
	require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug", "defect"}))

	// Merge bug into defect — the task already has defect, so the bug row
	// should be deleted (not cause a PK conflict).
	require.NoError(t, store.MergeTags("bug", "defect"))

	linked, err := store.ListTaskTags("CW-0001")
	require.NoError(t, err)
	require.Len(t, linked, 1)
	assert.Equal(t, "defect", linked[0].Slug)
}

func TestMergeTagsSourceNotFound(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "defect", Name: "Defect", Color: "zinc"}))

	err := store.MergeTags("nope", "defect")
	assert.Error(t, err)
}

func TestMergeTagsDestNotFound(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "bug", Name: "Bug", Color: "zinc"}))

	err := store.MergeTags("bug", "nope")
	assert.Error(t, err)
}

func TestGetTagReturnsSentinelOnMissing(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.GetTag("nope")
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrTagNotFound), "expected ErrTagNotFound, got %v", err)
}

func TestUpdateTagReturnsSentinelOnMissing(t *testing.T) {
	store := setupTestStore(t)

	newName := "X"
	err := store.UpdateTag("nope", sqlstore.TagUpdate{Name: &newName})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrTagNotFound), "expected ErrTagNotFound, got %v", err)
}

func TestDeleteTagReturnsSentinelOnMissing(t *testing.T) {
	store := setupTestStore(t)

	err := store.DeleteTag("nope")
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrTagNotFound), "expected ErrTagNotFound, got %v", err)
}

func TestCreateTagIfNotExists(t *testing.T) {
	store := setupTestStore(t)

	// First call inserts
	require.NoError(t, store.CreateTagIfNotExists(&sqlstore.TagRecord{
		Slug: "bug", Name: "Bug", Color: "red",
	}))
	got, err := store.GetTag("bug")
	require.NoError(t, err)
	assert.Equal(t, "Bug", got.Name)
	assert.Equal(t, "red", got.Color)

	// Second call with a different name/color is a silent no-op — existing row wins
	require.NoError(t, store.CreateTagIfNotExists(&sqlstore.TagRecord{
		Slug: "bug", Name: "Bug Report", Color: "orange",
	}))
	got2, err := store.GetTag("bug")
	require.NoError(t, err)
	assert.Equal(t, "Bug", got2.Name, "first-seen name should be preserved")
	assert.Equal(t, "red", got2.Color, "first-seen color should be preserved")
}

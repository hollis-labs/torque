package sqlstore_test

import (
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

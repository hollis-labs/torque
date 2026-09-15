package service_test

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

type fakeSQLError struct {
	state string
	msg   string
}

func (e fakeSQLError) Error() string    { return e.msg }
func (e fakeSQLError) SQLState() string { return e.state }

func setupTagServiceTest(t *testing.T) (*service.Service, *sqlstore.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)
	return svc, store
}

// sampleTask creates a minimal valid TaskRecord for use in tests that need
// to exercise task→tag linking. Matches the minimal shape required by the
// sqlstore layer's CreateTask flow after Task 9.
func sampleTask(id string) *sqlstore.TaskRecord {
	return &sqlstore.TaskRecord{
		ID:       id,
		Title:    "Test task",
		Status:   "todo",
		Priority: 2,
		Executor: "cli",
	}
}

func TestTagServiceCreateWithName(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	tag, err := svc.Tag.Create(service.TagCreateInput{Name: "Frontend Bug"})
	require.NoError(t, err)
	assert.Equal(t, "frontend-bug", tag.Slug)
	assert.Equal(t, "Frontend Bug", tag.Name)
	assert.Equal(t, "zinc", tag.Color)
}

func TestIsTagUniqueConstraintError(t *testing.T) {
	assert.True(t, service.IsTagUniqueConstraintError(fmt.Errorf("wrapped: %w", fakeSQLError{state: "23505", msg: "duplicate key"})))
	assert.True(t, service.IsTagUniqueConstraintError(errors.New("UNIQUE constraint failed: tags.slug")))
	assert.True(t, service.IsTagUniqueConstraintError(errors.New("pq: duplicate key value violates unique constraint (SQLSTATE 23505)")))
	assert.False(t, service.IsTagUniqueConstraintError(fakeSQLError{state: "23503", msg: "foreign key"}))
	assert.False(t, service.IsTagUniqueConstraintError(errors.New("ordinary error mentions row 23505 but is not a SQLSTATE")))
}

func TestTagServiceCreateWithExplicitSlug(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	tag, err := svc.Tag.Create(service.TagCreateInput{
		Name:  "User Interface",
		Slug:  "ui",
		Color: "blue",
	})
	require.NoError(t, err)
	assert.Equal(t, "ui", tag.Slug)
	assert.Equal(t, "User Interface", tag.Name)
	assert.Equal(t, "blue", tag.Color)
}

func TestTagServiceCreateRequiresName(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	_, err := svc.Tag.Create(service.TagCreateInput{Name: ""})
	assert.Error(t, err)
}

func TestTagServiceCreateRejectsEmptySlug(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	// Name that slugifies to empty
	_, err := svc.Tag.Create(service.TagCreateInput{Name: "!!!"})
	assert.Error(t, err)
}

func TestTagServiceCreateRejectsBadColor(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	_, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug", Color: "turquoise"})
	assert.Error(t, err)
}

func TestTagServiceCreateRejectsBadExplicitSlug(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	_, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug", Slug: "Bug!"})
	assert.Error(t, err)
}

func TestTagServiceCreateRejectsLongName(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	longName := ""
	for i := 0; i < 65; i++ {
		longName += "a"
	}
	_, err := svc.Tag.Create(service.TagCreateInput{Name: longName})
	assert.Error(t, err)
}

func TestTagServiceCreateRejectsLongDescription(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	longDesc := ""
	for i := 0; i < 501; i++ {
		longDesc += "a"
	}
	_, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug", Description: longDesc})
	assert.Error(t, err)
}

func TestTagServiceUpdate(t *testing.T) {
	svc, _ := setupTagServiceTest(t)
	_, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
	require.NoError(t, err)

	newName := "Bug Report"
	newColor := "red"
	updated, err := svc.Tag.Update("bug", service.TagUpdateInput{
		Name:  &newName,
		Color: &newColor,
	})
	require.NoError(t, err)
	assert.Equal(t, "Bug Report", updated.Name)
	assert.Equal(t, "red", updated.Color)
}

func TestTagServiceUpdateRejectsBadColor(t *testing.T) {
	svc, _ := setupTagServiceTest(t)
	_, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
	require.NoError(t, err)

	bad := "turquoise"
	_, err = svc.Tag.Update("bug", service.TagUpdateInput{Color: &bad})
	assert.Error(t, err)
}

func TestTagServiceList(t *testing.T) {
	svc, _ := setupTagServiceTest(t)
	_, _ = svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
	_, _ = svc.Tag.Create(service.TagCreateInput{Name: "UI"})

	tags, err := svc.Tag.List()
	require.NoError(t, err)
	assert.Len(t, tags, 2)
}

func TestTagServiceDelete(t *testing.T) {
	svc, _ := setupTagServiceTest(t)
	_, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
	require.NoError(t, err)

	require.NoError(t, svc.Tag.Delete("bug"))

	_, err = svc.Tag.Get("bug")
	assert.Error(t, err)
}

func TestTagServiceMerge(t *testing.T) {
	svc, store := setupTagServiceTest(t)
	_, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
	require.NoError(t, err)
	_, err = svc.Tag.Create(service.TagCreateInput{Name: "Defect"})
	require.NoError(t, err)

	// Seed a task
	task := sampleTask("CW-MERGE-001")
	require.NoError(t, store.CreateTask(task))
	require.NoError(t, store.SetTaskTags("CW-MERGE-001", []string{"bug"}))

	require.NoError(t, svc.Tag.Merge("bug", "defect"))

	_, err = svc.Tag.Get("bug")
	assert.Error(t, err)

	linked, err := store.ListTaskTags("CW-MERGE-001")
	require.NoError(t, err)
	require.Len(t, linked, 1)
	assert.Equal(t, "defect", linked[0].Slug)
}

func TestTagServiceMergeRejectsSame(t *testing.T) {
	svc, _ := setupTagServiceTest(t)
	_, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
	require.NoError(t, err)

	err = svc.Tag.Merge("bug", "bug")
	assert.Error(t, err)
}

func TestResolveNamesAutoCreates(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	slugs, err := svc.Tag.ResolveNames([]string{"Bug", "UI"})
	require.NoError(t, err)
	require.Equal(t, []string{"bug", "ui"}, slugs)

	// Confirm tags were created with the raw input as the display name
	bug, err := svc.Tag.Get("bug")
	require.NoError(t, err)
	assert.Equal(t, "Bug", bug.Name)
	assert.Equal(t, "zinc", bug.Color)

	ui, err := svc.Tag.Get("ui")
	require.NoError(t, err)
	assert.Equal(t, "UI", ui.Name)
}

func TestResolveNamesPreservesExistingDisplayName(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	// Pre-create "bug" with a specific display name
	_, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug Report", Slug: "bug", Color: "red"})
	require.NoError(t, err)

	// Resolve with a different casing / display name for the same slug
	slugs, err := svc.Tag.ResolveNames([]string{"bug", "BUG"})
	require.NoError(t, err)
	require.Equal(t, []string{"bug"}, slugs) // dedup

	// Existing display name and color are NOT overwritten
	tag, err := svc.Tag.Get("bug")
	require.NoError(t, err)
	assert.Equal(t, "Bug Report", tag.Name)
	assert.Equal(t, "red", tag.Color)
}

func TestResolveNamesSkipsEmptyInputs(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	slugs, err := svc.Tag.ResolveNames([]string{"Bug", "   ", "!!!", "UI"})
	require.NoError(t, err)
	require.Equal(t, []string{"bug", "ui"}, slugs)
}

func TestResolveNamesAllEmpty(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	slugs, err := svc.Tag.ResolveNames([]string{"   ", "!!!"})
	require.NoError(t, err)
	assert.Empty(t, slugs)
}

func TestResolveNamesPreservesOrder(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	slugs, err := svc.Tag.ResolveNames([]string{"zulu", "alpha", "mike"})
	require.NoError(t, err)
	require.Equal(t, []string{"zulu", "alpha", "mike"}, slugs)
}

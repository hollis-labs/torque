package sqlstore_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

func sampleTemplate(id string, version int) *sqlstore.TemplateRecord {
	return &sqlstore.TemplateRecord{
		ID:          id,
		Version:     version,
		Name:        "Template " + id,
		Description: "x",
		Kind:        "agent",
		AutoExecute: true,
		Executor:    sql.NullString{String: "cli", Valid: true},
	}
}

func TestTemplate_CreateAndGet(t *testing.T) {
	store := setupTestStore(t)

	tpl := sampleTemplate("backend-fix", 1)
	tpl.Deliverables = sql.NullString{String: `[{"type":"diff","required":true}]`, Valid: true}
	require.NoError(t, store.CreateTemplate(tpl))

	got, err := store.GetTemplate("backend-fix", 1)
	require.NoError(t, err)
	assert.Equal(t, "Template backend-fix", got.Name)
	assert.Equal(t, "agent", got.Kind)
	assert.True(t, got.AutoExecute)
	assert.Equal(t, "cli", got.Executor.String)
	// Defaults applied by store.
	assert.Equal(t, "review", got.OnDone)
	assert.Equal(t, "retry", got.OnFail)
	assert.Equal(t, 0, got.MaxRetries)
}

func TestTemplate_CreateAndGet_PreservesExplicitZeroRetries(t *testing.T) {
	store := setupTestStore(t)

	tpl := sampleTemplate("zero-retries", 1)
	tpl.MaxRetries = 0
	require.NoError(t, store.CreateTemplate(tpl))

	got, err := store.GetTemplate("zero-retries", 1)
	require.NoError(t, err)
	assert.Equal(t, 0, got.MaxRetries)
}

func TestTemplate_GetNotFound(t *testing.T) {
	store := setupTestStore(t)
	_, err := store.GetTemplate("missing", 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrTemplateNotFound))
}

func TestTemplate_NextVersionEmpty(t *testing.T) {
	store := setupTestStore(t)
	v, err := store.NextTemplateVersion("never-created")
	require.NoError(t, err)
	assert.Equal(t, 1, v)
}

func TestTemplate_NextVersionIncrements(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTemplate(sampleTemplate("t", 1)))
	v, err := store.NextTemplateVersion("t")
	require.NoError(t, err)
	assert.Equal(t, 2, v)
	require.NoError(t, store.CreateTemplate(sampleTemplate("t", 2)))
	v, err = store.NextTemplateVersion("t")
	require.NoError(t, err)
	assert.Equal(t, 3, v)
}

func TestTemplate_GetLatestSkipsArchived(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTemplate(sampleTemplate("t", 1)))
	require.NoError(t, store.CreateTemplate(sampleTemplate("t", 2)))
	require.NoError(t, store.ArchiveTemplate("t", 2))

	got, err := store.GetLatestTemplate("t")
	require.NoError(t, err)
	assert.Equal(t, 1, got.Version, "latest non-archived should be v1 when v2 is archived")
}

func TestTemplate_Archive(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTemplate(sampleTemplate("t", 1)))
	require.NoError(t, store.ArchiveTemplate("t", 1))
	got, err := store.GetTemplate("t", 1)
	require.NoError(t, err)
	assert.True(t, got.IsArchived)
}

func TestTemplate_Archive_UnknownNotFound(t *testing.T) {
	store := setupTestStore(t)
	err := store.ArchiveTemplate("missing", 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrTemplateNotFound))
}

func TestTemplate_Delete_NoReferences(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTemplate(sampleTemplate("t", 1)))
	require.NoError(t, store.CreateTemplate(sampleTemplate("t", 2)))
	require.NoError(t, store.DeleteTemplate("t"))

	_, err := store.GetTemplate("t", 1)
	assert.True(t, errors.Is(err, sqlstore.ErrTemplateNotFound))
}

func TestTemplate_Delete_RejectsIfReferenced(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTemplate(sampleTemplate("t", 1)))
	// Create a task that references this template.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-T-REF",
		Title:    "referencing task",
		Status:   "todo",
		Metadata: sql.NullString{String: `{"template_ref":{"id":"t","version":1}}`, Valid: true},
	}))

	err := store.DeleteTemplate("t")
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrTemplateReferenced))

	// Template still retrievable.
	_, err = store.GetTemplate("t", 1)
	require.NoError(t, err)
}

func TestTemplate_Delete_UnknownNotFound(t *testing.T) {
	store := setupTestStore(t)
	err := store.DeleteTemplate("missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrTemplateNotFound))
}

func TestTemplate_List(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateTemplate(sampleTemplate("a", 1)))
	require.NoError(t, store.CreateTemplate(sampleTemplate("a", 2)))
	require.NoError(t, store.CreateTemplate(sampleTemplate("b", 1)))
	require.NoError(t, store.ArchiveTemplate("a", 1))

	// Default: exclude archived.
	list, err := store.ListTemplates(false, "")
	require.NoError(t, err)
	assert.Len(t, list, 2)
	for _, tpl := range list {
		assert.False(t, tpl.IsArchived)
	}

	// Include archived.
	all, err := store.ListTemplates(true, "")
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestTemplate_List_KindFilter(t *testing.T) {
	store := setupTestStore(t)
	a := sampleTemplate("a", 1)
	a.Kind = "agent"
	require.NoError(t, store.CreateTemplate(a))
	w := sampleTemplate("w", 1)
	w.Kind = "wait"
	require.NoError(t, store.CreateTemplate(w))

	list, err := store.ListTemplates(false, "wait")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "w", list[0].ID)
}

// Migration 010 added working_dir as a typed column on task_templates.
// Round-trip: set on create, read back unchanged.
func TestTemplate_WorkingDir_RoundTrip(t *testing.T) {
	store := setupTestStore(t)

	tpl := sampleTemplate("wd", 1)
	tpl.WorkingDir = sql.NullString{String: "/tmp/repo", Valid: true}
	require.NoError(t, store.CreateTemplate(tpl))

	got, err := store.GetTemplate("wd", 1)
	require.NoError(t, err)
	assert.True(t, got.WorkingDir.Valid)
	assert.Equal(t, "/tmp/repo", got.WorkingDir.String)
}

// Templates that don't set working_dir get a null column, which scanTemplate
// should surface as !Valid rather than the empty string.
func TestTemplate_WorkingDir_NullWhenUnset(t *testing.T) {
	store := setupTestStore(t)

	tpl := sampleTemplate("wd-null", 1)
	require.NoError(t, store.CreateTemplate(tpl))

	got, err := store.GetTemplate("wd-null", 1)
	require.NoError(t, err)
	assert.False(t, got.WorkingDir.Valid,
		"unset working_dir should scan as NullString{Valid:false}")
}

func TestTemplate_CompositeKey_SameIDDifferentVersions(t *testing.T) {
	store := setupTestStore(t)
	v1 := sampleTemplate("t", 1)
	v1.Name = "Version One"
	v2 := sampleTemplate("t", 2)
	v2.Name = "Version Two"
	require.NoError(t, store.CreateTemplate(v1))
	require.NoError(t, store.CreateTemplate(v2))

	got1, _ := store.GetTemplate("t", 1)
	got2, _ := store.GetTemplate("t", 2)
	assert.Equal(t, "Version One", got1.Name)
	assert.Equal(t, "Version Two", got2.Name)
}

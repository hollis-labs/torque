package service_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

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

func TestTagServiceCreateWithName(t *testing.T) {
	svc, _ := setupTagServiceTest(t)

	tag, err := svc.Tag.Create(service.TagCreateInput{Name: "Frontend Bug"})
	require.NoError(t, err)
	assert.Equal(t, "frontend-bug", tag.Slug)
	assert.Equal(t, "Frontend Bug", tag.Name)
	assert.Equal(t, "zinc", tag.Color)
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

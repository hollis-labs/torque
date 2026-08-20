package service_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEpicCreateRequiresFeature(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Epic.Create(service.EpicCreateInput{Name: "Epic 1"})
	assert.Error(t, err)
	assert.IsType(t, &service.FeatureDisabledError{}, err)
}

func TestEpicCreate(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	epic, err := svc.Epic.Create(service.EpicCreateInput{
		Name:        "Auth Overhaul",
		Description: "Replace entire auth stack",
	})
	require.NoError(t, err)
	assert.Contains(t, epic.ID, "EP-")
	assert.Equal(t, "Auth Overhaul", epic.Name)
	assert.Equal(t, "active", epic.Status)
}

func TestEpicCreateValidation(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	_, err := svc.Epic.Create(service.EpicCreateInput{})
	assert.Error(t, err)
	assert.IsType(t, &service.ValidationError{}, err)
}

func TestEpicUpdate(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	epic, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Old Name"})

	name := "New Name"
	err := svc.Epic.Update(epic.ID, service.EpicUpdateInput{Name: &name})
	require.NoError(t, err)

	got, _ := svc.Epic.Get(epic.ID)
	assert.Equal(t, "New Name", got.Name)
}

func TestEpicUpdateStatus(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	epic, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic"})

	inactive := "inactive"
	err := svc.Epic.Update(epic.ID, service.EpicUpdateInput{Status: &inactive})
	require.NoError(t, err)

	got, _ := svc.Epic.Get(epic.ID)
	assert.Equal(t, "inactive", got.Status)
}

func TestEpicUpdateInvalidStatus(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	epic, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic"})

	invalid := "invalid"
	err := svc.Epic.Update(epic.ID, service.EpicUpdateInput{Status: &invalid})
	assert.Error(t, err)
	assert.IsType(t, &service.ValidationError{}, err)
}

func TestEpicList(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	svc.Epic.Create(service.EpicCreateInput{Name: "Epic A"})
	svc.Epic.Create(service.EpicCreateInput{Name: "Epic B"})

	epics, err := svc.Epic.List("", "", false)
	require.NoError(t, err)
	assert.Len(t, epics, 2)
}

func TestEpicListFilterStatus(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	svc.Epic.Create(service.EpicCreateInput{Name: "Active Epic"})
	e2, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Inactive Epic"})
	inactive := "inactive"
	svc.Epic.Update(e2.ID, service.EpicUpdateInput{Status: &inactive})

	epics, err := svc.Epic.List("active", "", false)
	require.NoError(t, err)
	assert.Len(t, epics, 1)
	assert.Equal(t, "Active Epic", epics[0].Name)
}

// TestEpicArchiveDoesNotChangeStatus covers PRIM-004 AC: archiving is
// independent of status — archiving an epic isn't the same fact as the
// epic being "done".
func TestEpicArchiveDoesNotChangeStatus(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	epic, err := svc.Epic.Create(service.EpicCreateInput{Name: "Epic 1"})
	require.NoError(t, err)
	require.Equal(t, "active", epic.Status)

	require.NoError(t, svc.Epic.Archive(epic.ID))

	got, err := svc.Epic.Get(epic.ID)
	require.NoError(t, err)
	assert.True(t, got.ArchivedAt.Valid)
	assert.Equal(t, "active", got.Status)

	require.NoError(t, svc.Epic.Unarchive(epic.ID))

	got, err = svc.Epic.Get(epic.ID)
	require.NoError(t, err)
	assert.False(t, got.ArchivedAt.Valid)
	assert.Equal(t, "active", got.Status)
}

// TestEpicListExcludesArchivedByDefault covers PRIM-004 AC: list filters
// default to excluding archived rows unless include_archived is passed.
func TestEpicListExcludesArchivedByDefault(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	e1, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic A"})
	e2, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic B"})
	require.NoError(t, svc.Epic.Archive(e2.ID))

	epics, err := svc.Epic.List("", "", false)
	require.NoError(t, err)
	require.Len(t, epics, 1)
	assert.Equal(t, e1.ID, epics[0].ID)

	all, err := svc.Epic.List("", "", true)
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestEpicDelete(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	epic, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic 1"})

	err := svc.Epic.Delete(epic.ID)
	require.NoError(t, err)

	_, err = svc.Epic.Get(epic.ID)
	assert.Error(t, err)
}

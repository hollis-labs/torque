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

// TestEpicUpdateNameCannotBeCleared verifies EpicService.Update rejects an
// explicit empty name with a clean ValidationError (SWEEP-001, mirroring
// TaskService.Update's title guard added for FIX-001) rather than silently
// persisting an empty name.
func TestEpicUpdateNameCannotBeCleared(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	epic, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic"})

	empty := ""
	err := svc.Epic.Update(epic.ID, service.EpicUpdateInput{Name: &empty})
	require.Error(t, err)
	ve, ok := err.(*service.ValidationError)
	require.True(t, ok, "expected *service.ValidationError, got %T", err)
	assert.Equal(t, "name", ve.Field)

	got, _ := svc.Epic.Get(epic.ID)
	assert.Equal(t, "Epic", got.Name, "name must be unchanged after rejected clear")
}

// TestEpicUpdateDescriptionClear verifies an explicit empty description
// actually clears the field (SWEEP-001: buildEpicUpdateInput used to treat
// any empty string as "not provided", silently dropping an explicit clear —
// same bug class FIX-001 fixed for Task).
func TestEpicUpdateDescriptionClear(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	epic, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic", Description: "Some description"})

	empty := ""
	require.NoError(t, svc.Epic.Update(epic.ID, service.EpicUpdateInput{Description: &empty}))

	got, _ := svc.Epic.Get(epic.ID)
	assert.Equal(t, "", got.Description)
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

// TestEpicListPaginatedSearch covers ENT-EPIC's merged-search acceptance
// criterion at the service layer.
func TestEpicListPaginatedSearch(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	svc.Epic.Create(service.EpicCreateInput{Name: "Auth Overhaul", Description: "Replace entire auth stack"})
	svc.Epic.Create(service.EpicCreateInput{Name: "Billing Rewrite", Description: "New invoicing pipeline"})

	epics, err := svc.Epic.ListPaginated(service.EpicListInput{Search: "auth"})
	require.NoError(t, err)
	require.Len(t, epics, 1)
	assert.Equal(t, "Auth Overhaul", epics[0].Name)
}

// TestEpicListPaginatedSortByName covers PRIM-002's sort_by acceptance
// criterion at the service layer.
func TestEpicListPaginatedSortByName(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	svc.Epic.Create(service.EpicCreateInput{Name: "Zebra"})
	svc.Epic.Create(service.EpicCreateInput{Name: "Alpha"})

	epics, err := svc.Epic.ListPaginated(service.EpicListInput{SortBy: "name", SortDir: "asc"})
	require.NoError(t, err)
	require.Len(t, epics, 2)
	assert.Equal(t, "Alpha", epics[0].Name)
	assert.Equal(t, "Zebra", epics[1].Name)
}

// TestEpicListPaginatedIncludeArchived covers PRIM-004/PRIM-001 interplay:
// ListPaginated defaults to excluding archived rows, matching List's
// existing includeArchived semantics.
func TestEpicListPaginatedIncludeArchived(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	e1, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic A"})
	e2, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic B"})
	require.NoError(t, svc.Epic.Archive(e2.ID))

	epics, err := svc.Epic.ListPaginated(service.EpicListInput{})
	require.NoError(t, err)
	require.Len(t, epics, 1)
	assert.Equal(t, e1.ID, epics[0].ID)

	all, err := svc.Epic.ListPaginated(service.EpicListInput{IncludeArchived: true})
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// TestEpicCreateAndUpdatePriority covers ENT-EPIC's "priority settable"
// acceptance criterion at the service layer: settable on create, changeable
// on update.
func TestEpicCreateAndUpdatePriority(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	prio := int64(1)
	epic, err := svc.Epic.Create(service.EpicCreateInput{Name: "Epic", Priority: &prio})
	require.NoError(t, err)
	require.True(t, epic.Priority.Valid)
	assert.Equal(t, int64(1), epic.Priority.Int64)

	newPrio := int64(3)
	err = svc.Epic.Update(epic.ID, service.EpicUpdateInput{Priority: &newPrio})
	require.NoError(t, err)

	got, err := svc.Epic.Get(epic.ID)
	require.NoError(t, err)
	require.True(t, got.Priority.Valid)
	assert.Equal(t, int64(3), got.Priority.Int64)
}

// TestEpicBulkUpdate is PRIM-003's acceptance criterion applied to Epic:
// partial success — one bad id fails without blocking the rest.
func TestEpicBulkUpdate(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	e1, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic A"})
	e2, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic B"})

	inactive := "inactive"
	succeeded, failed := svc.Epic.BulkUpdate([]string{e1.ID, e2.ID, "EP-nonexistent"}, service.EpicUpdateInput{Status: &inactive})

	assert.ElementsMatch(t, []string{e1.ID, e2.ID}, succeeded)
	require.Len(t, failed, 1)
	assert.Equal(t, "EP-nonexistent", failed[0].ID)

	got1, _ := svc.Epic.Get(e1.ID)
	got2, _ := svc.Epic.Get(e2.ID)
	assert.Equal(t, "inactive", got1.Status)
	assert.Equal(t, "inactive", got2.Status)
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

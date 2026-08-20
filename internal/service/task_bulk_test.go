package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// TestTaskBulkUpdate_PartialSuccess mirrors BulkTransition's contract: one
// bad id in the batch fails that id only, the rest still apply, and the
// failure is the same *typed* not-found error single-item Update would
// have produced (ErrTaskNotFound), not a bulk-specific error shape.
func TestTaskBulkUpdate_PartialSuccess(t *testing.T) {
	svc := setupService(t)

	t1, err := svc.Task.Create(service.TaskCreateInput{Title: "one", Priority: 2})
	require.NoError(t, err)
	t2, err := svc.Task.Create(service.TaskCreateInput{Title: "two", Priority: 2})
	require.NoError(t, err)

	priority := 5
	input := service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Priority: &priority}}

	succeeded, failed := svc.Task.BulkUpdate([]string{t1.ID, "CW-nonexistent-0001", t2.ID}, input)

	require.ElementsMatch(t, []string{t1.ID, t2.ID}, succeeded)
	require.Len(t, failed, 1)
	require.Equal(t, "CW-nonexistent-0001", failed[0].ID)
	require.ErrorIs(t, failed[0].Err, sqlstore.ErrTaskNotFound)

	got1, err := svc.Task.Get(t1.ID)
	require.NoError(t, err)
	require.Equal(t, 5, got1.Priority)
	got2, err := svc.Task.Get(t2.ID)
	require.NoError(t, err)
	require.Equal(t, 5, got2.Priority)
}

// TestTaskBulkUpdate_NilFieldsUntouched proves BulkUpdate applies the exact
// same partial-patch semantics as single Update: a nil pointer in the
// shared TaskUpdateInput leaves that field alone on every id, it doesn't
// zero it out.
func TestTaskBulkUpdate_NilFieldsUntouched(t *testing.T) {
	svc := setupService(t)

	t1, err := svc.Task.Create(service.TaskCreateInput{Title: "keep-me", Description: "orig desc", Priority: 3})
	require.NoError(t, err)

	priority := 1
	input := service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Priority: &priority}}
	succeeded, failed := svc.Task.BulkUpdate([]string{t1.ID}, input)
	require.Equal(t, []string{t1.ID}, succeeded)
	require.Empty(t, failed)

	got, err := svc.Task.Get(t1.ID)
	require.NoError(t, err)
	require.Equal(t, 1, got.Priority)
	require.Equal(t, "keep-me", got.Title)
	require.Equal(t, "orig desc", got.Description)
}

// TestTaskBulkDelete_PartialSuccess exercises hard-delete across a batch
// with one missing id.
func TestTaskBulkDelete_PartialSuccess(t *testing.T) {
	svc := setupService(t)

	t1, err := svc.Task.Create(service.TaskCreateInput{Title: "del-me"})
	require.NoError(t, err)

	succeeded, failed := svc.Task.BulkDelete([]string{t1.ID, "CW-nonexistent-0002"})

	require.Equal(t, []string{t1.ID}, succeeded)
	require.Len(t, failed, 1)
	require.Equal(t, "CW-nonexistent-0002", failed[0].ID)
	require.Error(t, failed[0].Err)

	_, err = svc.Task.Get(t1.ID)
	require.ErrorIs(t, err, sqlstore.ErrTaskNotFound, "deleted task must actually be gone")
}

// TestTaskBulkTag_AddRemove covers the add/remove-slug shape: existing tags
// not named in either list survive untouched, removed slugs drop out, and
// added slugs (including auto-created ones) show up — across two tasks in
// one call.
func TestTaskBulkTag_AddRemove(t *testing.T) {
	svc := setupService(t)

	t1, err := svc.Task.Create(service.TaskCreateInput{Title: "tagged", Tags: []string{"wip", "keep"}})
	require.NoError(t, err)
	t2, err := svc.Task.Create(service.TaskCreateInput{Title: "untagged"})
	require.NoError(t, err)

	succeeded, failed := svc.Task.BulkTag([]string{t1.ID, t2.ID}, []string{"p0"}, []string{"wip"})
	require.ElementsMatch(t, []string{t1.ID, t2.ID}, succeeded)
	require.Empty(t, failed)

	tags1, err := svc.Task.ListTags(t1.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"keep", "p0"}, slugsOf(tags1))

	tags2, err := svc.Task.ListTags(t2.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"p0"}, slugsOf(tags2))
}

// TestTaskBulkTag_RemoveUnknownSlugIsNoOp proves remove doesn't auto-create
// a tag just to fail to find it — an unlinked/unknown slug is silently a
// no-op, matching the doc comment on BulkTag.
func TestTaskBulkTag_RemoveUnknownSlugIsNoOp(t *testing.T) {
	svc := setupService(t)

	t1, err := svc.Task.Create(service.TaskCreateInput{Title: "solo", Tags: []string{"keep"}})
	require.NoError(t, err)

	succeeded, failed := svc.Task.BulkTag([]string{t1.ID}, nil, []string{"never-existed"})
	require.Equal(t, []string{t1.ID}, succeeded)
	require.Empty(t, failed)

	all, err := svc.Tag.List()
	require.NoError(t, err)
	for _, tg := range all {
		require.NotEqual(t, "never-existed", tg.Slug, "remove must not auto-create the tag it's trying to remove")
	}

	linked, err := svc.Task.ListTags(t1.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"keep"}, slugsOf(linked))
}

// TestTaskBulkTag_NotFound proves a missing id fails that id only, with the
// same not_found-classifiable error single-item operations use.
func TestTaskBulkTag_NotFound(t *testing.T) {
	svc := setupService(t)

	t1, err := svc.Task.Create(service.TaskCreateInput{Title: "real"})
	require.NoError(t, err)

	succeeded, failed := svc.Task.BulkTag([]string{t1.ID, "CW-nonexistent-0003"}, []string{"p0"}, nil)
	require.Equal(t, []string{t1.ID}, succeeded)
	require.Len(t, failed, 1)
	require.Equal(t, "CW-nonexistent-0003", failed[0].ID)
	require.ErrorIs(t, failed[0].Err, sqlstore.ErrTaskNotFound)
}

func slugsOf(tags []sqlstore.TagRecord) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.Slug)
	}
	return out
}

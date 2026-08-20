package service

import (
	"strings"

	"github.com/hollis-labs/go-strutil"
)

// PRIM-003: shared bulk-operation pattern applied to Task. BulkUpdate,
// BulkDelete, and BulkTag all funnel through RunBulk (bulk.go) so they
// return the identical {succeeded, failed} partial-success shape that
// BulkTransition (task.go) already established — bulk_transition itself is
// out of scope here and is left untouched.

// BulkUpdate applies the same partial update to many tasks. Per-item
// semantics are identical to Update: input is a single TaskUpdateInput
// built once by the caller (the mcpadapter layer, mirroring
// handleTaskUpdate's presence-in-payload rule — only keys the caller
// actually sent are non-nil) and applied unchanged to every id. A failure
// on one id (not found, validation, etc.) does not stop the rest.
func (s *TaskService) BulkUpdate(ids []string, input TaskUpdateInput) ([]string, []BulkItemError) {
	return RunBulk(ids, func(id string) error {
		return s.Update(id, input)
	})
}

// BulkDelete hard-deletes many tasks by ID. A failure on one id (not
// found, etc.) does not stop the rest.
func (s *TaskService) BulkDelete(ids []string) ([]string, []BulkItemError) {
	return RunBulk(ids, func(id string) error {
		return s.Delete(id)
	})
}

// BulkTag adds and/or removes tag slugs across many task IDs in one call,
// reusing TagService the same way single-task tag assignment does.
//
//   - add is resolved through TagService.ResolveNames, which auto-creates
//     any tag that doesn't exist yet — identical to what Update does when
//     a caller sets Tags. add is resolved once, up front: if the shared
//     add list itself is bad (e.g. ResolveNames fails), every id in ids
//     fails uniformly with that error rather than the call returning a
//     bare error and breaking the {succeeded, failed} contract.
//   - remove is normalized with strutil.Slugify directly (no ResolveNames)
//     so removing a tag that was never linked — or never existed at all —
//     stays a no-op instead of creating the tag just to unlink it.
//   - add and remove may both be non-empty in the same call; remove is
//     applied first, then add, so a slug present in both ends up applied
//     (added), not removed.
//
// Per-id, the task's current tag set is fetched, add/remove are applied
// preserving existing sort order (removed slugs drop out in place, added
// slugs append), and the result is written back with SetTaskTags — the
// same replace-all primitive Update(Tags: ...) uses.
func (s *TaskService) BulkTag(ids []string, add, remove []string) ([]string, []BulkItemError) {
	addSlugs, err := s.tags.ResolveNames(add)
	if err != nil {
		failed := make([]BulkItemError, len(ids))
		for i, id := range ids {
			failed[i] = BulkItemError{ID: id, Err: err}
		}
		return nil, failed
	}

	removeSlugs := make(map[string]bool, len(remove))
	for _, raw := range remove {
		if slug := strutil.Slugify(strings.TrimSpace(raw)); slug != "" {
			removeSlugs[slug] = true
		}
	}

	return RunBulk(ids, func(id string) error {
		// Existence check up front: ListTaskTags/SetTaskTags don't fail on an
		// unknown task_id by themselves (ListTaskTags just returns zero rows;
		// SetTaskTags would hit the task_tags.task_id FK and surface as a raw
		// internal error). GetTask gives a clean, taxonomy-correct not_found
		// instead, matching Update's existing behavior.
		if _, err := s.store.GetTask(id); err != nil {
			return err
		}

		current, err := s.store.ListTaskTags(id)
		if err != nil {
			return err
		}

		kept := make([]string, 0, len(current)+len(addSlugs))
		have := make(map[string]bool, len(current)+len(addSlugs))
		for _, t := range current {
			if removeSlugs[t.Slug] || have[t.Slug] {
				continue
			}
			have[t.Slug] = true
			kept = append(kept, t.Slug)
		}
		for _, slug := range addSlugs {
			if have[slug] {
				continue
			}
			have[slug] = true
			kept = append(kept, slug)
		}

		return s.store.SetTaskTags(id, kept)
	})
}

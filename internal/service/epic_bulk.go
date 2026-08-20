package service

// PRIM-003: shared bulk-operation pattern applied to Epic, mirroring
// TaskService.BulkUpdate (task_bulk.go) — the reference entity for this
// primitive. BulkUpdate funnels through RunBulk (bulk.go) so it returns the
// identical {succeeded, failed} partial-success shape every other bulk_*
// verb across the codebase uses.

// BulkUpdate applies the same partial update to many epics. Per-item
// semantics are identical to Update: input is a single EpicUpdateInput
// built once by the caller (the mcpadapter layer) and applied unchanged to
// every id. A failure on one id (not found, validation, etc.) does not stop
// the rest.
func (s *EpicService) BulkUpdate(ids []string, input EpicUpdateInput) ([]string, []BulkItemError) {
	return RunBulk(ids, func(id string) error {
		return s.Update(id, input)
	})
}

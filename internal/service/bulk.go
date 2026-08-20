package service

// BulkItemError pairs a failed item's ID with the error the operation
// produced for it. The error is kept in its original typed form
// (*ValidationError, *service.NotFoundError, *TransitionError,
// *ConflictError, *FeatureDisabledError, or a plain error) rather than
// pre-stringified, so a caller — normally the MCP adapter's
// mapServiceError — can classify each failure through the exact same
// error-taxonomy path single-item operations already use
// (arg_invalid/not_found/conflict/domain/permission/internal).
type BulkItemError struct {
	ID  string
	Err error
}

// RunBulk applies op to each id in order and returns the succeeded IDs
// (input order, failures dropped) plus the per-item failures. This is the
// one partial-success shape shared by every bulk_* verb — bulk_transition,
// bulk_update, bulk_delete, bulk_tag, and any future entity's bulk
// operations. Partial success is not itself an error: op is invoked once
// per id regardless of earlier failures, and the caller always gets both
// slices back to decide how to react.
//
// Entity services should expose a thin BulkX(ids, ...) method that closes
// over their existing single-item X(id, ...) and calls RunBulk, rather than
// hand-rolling the loop per verb per entity — see TaskService.BulkUpdate /
// BulkDelete / BulkTag in task_bulk.go for the reference shape that Phase 4
// entity tasks (ENT-EPIC, ENT-SPRINT, ENT-ISSUE) should mirror.
func RunBulk(ids []string, op func(id string) error) (succeeded []string, failed []BulkItemError) {
	succeeded = make([]string, 0, len(ids))
	for _, id := range ids {
		if err := op(id); err != nil {
			failed = append(failed, BulkItemError{ID: id, Err: err})
			continue
		}
		succeeded = append(succeeded, id)
	}
	return succeeded, failed
}

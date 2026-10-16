package scheduler

import "github.com/hollis-labs/torque/internal/persistence/sqlstore"

// The scheduler's state changes are attributed to the scheduler in the event
// stream (CW-20261016-0133). The queued state writer batches ops into one
// transaction, so the trigger travels with each call and not in a context.
var schedulerTrigger = sqlstore.Trigger{Kind: sqlstore.TriggerScheduler, Ref: "scheduler"}

// schedPlain is a scheduler transition that leaves blocked_reason alone.
func schedPlain() sqlstore.TransitionOpts {
	return sqlstore.TransitionOpts{Trigger: schedulerTrigger}
}

// schedReason is a scheduler transition that also writes blocked_reason.
func schedReason(reason string) sqlstore.TransitionOpts {
	return sqlstore.TransitionOpts{Trigger: schedulerTrigger, Reason: &reason}
}

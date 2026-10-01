package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
)

// defaultIdleNudgeWindow is how long a long-lived worker may sit idle after
// a completed turn, with its task still doing, before runLongLived reminds
// it to signal, and again after the reminder before Torque routes the task
// itself (CW-20261001-0117). A var so tests can shrink it.
var defaultIdleNudgeWindow = 90 * time.Second

// idleNudgeMaxSeconds caps metadata.idle_nudge_seconds. Past an hour the
// inactivity threshold (30m by default) reaps the worker first anyway.
const idleNudgeMaxSeconds = 3600

// idleNudgeSendTimeout bounds the reminder's SendTurn, which runs on the
// wait loop: a stuck session pipe must not stall the status poll.
const idleNudgeSendTimeout = 30 * time.Second

// idleNudgeText is the one reminder turn a worker gets.
const idleNudgeText = "You ended your turn without signalling. Call torque_task_review if the work is complete, or torque_task_blocked with the reason. This is the only reminder: if you stay idle, Torque routes the task itself, to review if commits or task output landed and to blocked otherwise."

// autoRouteCommentAuthor marks the comment that tells the task's readers it
// was routed by Torque, not by its worker.
const autoRouteCommentAuthor = "[system/auto-route]"

// resolveIdleNudgeWindow picks the window from metadata.idle_nudge_seconds
// (0 to 3600; 0 turns the nudge and the auto-route off for the task), else
// defaultIdleNudgeWindow.
func resolveIdleNudgeWindow(opts Options) time.Duration {
	if secs, ok := clampedSecondsFromMetadata(opts.Metadata, "idle_nudge_seconds", 0, idleNudgeMaxSeconds); ok {
		return time.Duration(secs) * time.Second
	}
	return defaultIdleNudgeWindow
}

// idleNudger handles a long-lived worker that ends a turn without moving its
// task out of doing (CW-20261001-0117). A worker that finished but never
// called torque_task_review otherwise holds its project's slot until the
// inactivity threshold. Once no turn has been in flight for window since the
// last one ended, it sends the worker one reminder turn. If the worker is
// still idle a window after that, the reminder or the worker's reply to it,
// step reports that the run should be routed.
//
// runLongLived's status poll drives it, so it needs no loop of its own:
// step runs only on polls that find the task still doing. It never acts
// while a turn is in flight or before any turn has ended, nor while the task
// has a pending checkpoint, since the worker is then waiting by design.
// The reminder is sent at most once per run; a failed send is retried on the
// next poll.
type idleNudger struct {
	window  time.Duration
	turn    *turnTracker
	nudge   func() error
	waiting func() bool
	sentAt  time.Time
}

// step reports whether the worker has stayed idle a full window after its
// reminder. It is nil-safe, and a zero window disables it.
func (n *idleNudger) step(now time.Time) bool {
	if n == nil || n.window <= 0 || n.turn == nil {
		return false
	}
	ended, idle := n.turn.idleSince()
	if !idle {
		return false
	}
	if !n.sentAt.IsZero() {
		since := ended
		if n.sentAt.After(since) {
			since = n.sentAt
		}
		return now.Sub(since) >= n.window && !n.isWaiting()
	}
	if now.Sub(ended) < n.window || n.isWaiting() {
		return false
	}
	if err := n.nudge(); err != nil {
		log.Printf("agent: idle reminder failed (will retry on the next status poll): %v", err)
		return false
	}
	n.sentAt = now
	return false
}

func (n *idleNudger) isWaiting() bool {
	return n.waiting != nil && n.waiting()
}

// taskHasPendingCheckpoint reports whether the task has a checkpoint still
// awaiting a response. A store error counts as waiting, so an unreadable
// table never routes a task.
func taskHasPendingCheckpoint(store *sqlstore.Store, taskID string) bool {
	if store == nil || taskID == "" {
		return false
	}
	cps, err := store.ListCheckpointsForTask(taskID)
	if err != nil {
		log.Printf("agent: list checkpoints for %s failed: %v; not routing an idle worker", taskID, err)
		return true
	}
	for _, cp := range cps {
		if cp.Status == "pending" {
			return true
		}
	}
	return false
}

// sendIdleNudge sends the reminder turn, bounded by idleNudgeSendTimeout.
func sendIdleNudge(ctx context.Context, sender turnSender, sess *Session) error {
	sendCtx, cancel := context.WithTimeout(ctx, idleNudgeSendTimeout)
	defer cancel()
	return sender.SendTurn(sendCtx, sess, idleNudgeText)
}

// routeUnsignalled decides where a worker that stayed idle past its reminder
// goes: review when the run left commits on its branch or comments and
// artifacts on the task, blocked otherwise. Uncommitted edits block with the
// verifier's reason, which names the worktree they are preserved in. Either
// way the reason says Torque routed it.
func routeUnsignalled(v scheduler.WorkerVerdict, taskOutput int) (status, reason string) {
	const lead = "auto-routed: the worker ended its turn without signalling and stayed idle after one reminder"
	if v.Kind == scheduler.VerdictFailedNoCommitsWithEdits {
		return "blocked", lead + "; " + v.Reason
	}
	if v.CommitCount > 0 || taskOutput > 0 {
		return "review", fmt.Sprintf("%s; it left %d commit(s) on the run branch and %d comment(s) or artifact(s) on the task", lead, v.CommitCount, taskOutput)
	}
	return "blocked", lead + ", and left no commits on the run branch and no comments or artifacts on the task"
}

// commentAutoRoute records the route on the task. A review result's reason
// reaches no reader otherwise: the lifecycle moves the task to review
// without one.
func commentAutoRoute(store *sqlstore.Store, taskID, status, reason string) {
	if store == nil || taskID == "" {
		return
	}
	if err := store.AddComment(&sqlstore.CommentRecord{
		EntityType: "task",
		EntityID:   taskID,
		Author:     autoRouteCommentAuthor,
		Content:    fmt.Sprintf("Moved to %s by Torque: %s.", status, reason),
	}); err != nil {
		log.Printf("agent: auto-route comment on %s failed: %v", taskID, err)
	}
}

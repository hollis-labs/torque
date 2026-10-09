package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	gomsg "github.com/hollis-labs/substrate/mesh/messaging"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
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
const idleNudgeText = "You ended your turn without signalling. Call torque_task_review if the work is complete, or torque_task_blocked with the reason. Ending your turn again without signalling routes the task. If you are waiting on something, keep waiting inside this turn (poll it) or raise a checkpoint."

// autoRouteCommentAuthor marks the comment that tells the task's readers it
// was routed by Torque, not by its worker.
const autoRouteCommentAuthor = "[system/auto-route]"

// resolveIdleNudgeWindow picks the window from metadata.idle_nudge_seconds
// (0 to 3600; 0 turns the nudge and the auto-route off for the task), else
// defaultIdleNudgeWindow. It is 0 for anything but a kind=agent worker task:
// a plan, parent or internal task's session waits between turns by design.
func resolveIdleNudgeWindow(opts Options) time.Duration {
	if opts.TaskKind != "agent" {
		return 0
	}
	if secs, ok := clampedSecondsFromMetadata(opts.Metadata, "idle_nudge_seconds", 0, idleNudgeMaxSeconds); ok {
		return time.Duration(secs) * time.Second
	}
	return defaultIdleNudgeWindow
}

// idleNudger handles a long-lived worker that ends a turn without moving its
// task out of doing (CW-20261001-0117). A worker that finished but never
// called torque_task_review otherwise holds its project's slot until the
// inactivity threshold. Once no turn has been in flight for window since the
// last one ended, it sends the worker one reminder turn. The reminder's turn
// is in flight from the send until a done or error closes it (beginSend), so
// a reply that thinks, backs off or retries for a while before its first
// event is never read as idle. Only once a turn has ended after the reminder
// and the worker has been idle a window since does step report that the run
// should be routed. A worker that never answers is left to the inactivity
// threshold.
//
// runLongLived's status poll drives it, so it needs no loop of its own:
// step runs only on polls that find the task still doing. It never acts
// while a turn is in flight or before any turn has ended, nor while the
// worker waits by design (workerWaitsByDesign). It runs only for kind=agent
// worker tasks (resolveIdleNudgeWindow).
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
		if !ended.After(n.sentAt) {
			return false
		}
		return now.Sub(ended) >= n.window && !n.isWaiting()
	}
	if now.Sub(ended) < n.window || n.isWaiting() {
		return false
	}
	abort := n.turn.beginSend()
	if err := n.nudge(); err != nil {
		abort()
		log.Printf("agent: idle reminder failed (will retry on the next status poll): %v", err)
		return false
	}
	n.sentAt = now
	return false
}

func (n *idleNudger) isWaiting() bool {
	return n.waiting != nil && n.waiting()
}

// workerWaitsByDesign reports whether an idle worker is waiting on purpose,
// so it is neither reminded nor routed. That is when its task has:
//   - a checkpoint awaiting a response;
//   - a child task still open, which an orchestrating worker waits on;
//   - steering envelopes injected into the session that it has not
//     dismissed;
//   - or its session or task address polling its inbox.
//
// A store error counts as waiting, so an unreadable table never routes a
// task.
func workerWaitsByDesign(deps *Dependencies, taskID, sessID string) bool {
	if deps == nil || taskID == "" {
		return false
	}
	if deps.Polls.IsPollingAddress(gomsg.KindSession, sessID) || deps.Polls.IsPollingAddress(gomsg.KindAgent, taskID) {
		return true
	}
	for _, pe := range deps.Reminder.Snapshot(taskID) {
		if !pe.Dismissed {
			return true
		}
	}
	if deps.Store == nil {
		return false
	}
	cps, err := deps.Store.ListCheckpointsForTask(taskID)
	if err != nil {
		log.Printf("agent: list checkpoints for %s failed: %v; not routing an idle worker", taskID, err)
		return true
	}
	for _, cp := range cps {
		if cp.Status == "pending" {
			return true
		}
	}
	children, err := deps.Store.ListTasks(sqlstore.TaskFilter{ParentID: taskID})
	if err != nil {
		log.Printf("agent: list child tasks of %s failed: %v; not routing an idle worker", taskID, err)
		return true
	}
	for _, c := range children {
		if !closedTaskStatus(c.Status) {
			return true
		}
	}
	return false
}

// closedTaskStatus reports whether a child task is finished as far as a
// waiting parent is concerned. Anything else, review and blocked included,
// may still be what the parent waits on.
func closedTaskStatus(status string) bool {
	switch status {
	case "done", "archived", "abandoned", "cancelled", "canceled", "failed":
		return true
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

// autoRouteComment records the route on the task. A review result's reason
// reaches no reader otherwise: the lifecycle moves the task to review
// without one. The lifecycle posts it once it has moved the task.
func autoRouteComment(status, reason string) *executor.TaskComment {
	return &executor.TaskComment{
		Author:  autoRouteCommentAuthor,
		Content: fmt.Sprintf("Moved to %s by Torque: %s.", status, reason),
	}
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/hollis-labs/torque/internal/landing"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// SetSourceControl installs the adapter the landing gate reads PR state
// through. nil (the default) makes the gate use gh in the project's repo.
func (s *TaskService) SetSourceControl(sc landing.SourceControl) { s.sourceControl = sc }

// LandingCheck evaluates the landing gate for a task about to become done. It
// returns "" when the task may proceed, otherwise the unmet requirements. On a
// failure it also comments the remedy for the escalation target. It does not
// change task status; callers that own the transition do that.
func (s *TaskService) LandingCheck(ctx context.Context, task *sqlstore.TaskRecord) string {
	if ev := s.LandingEvaluate(ctx, task); ev.Blocking {
		return ev.Reason
	}
	return ""
}

// LandingEvaluate is LandingCheck with a typed result. A zero Kind means the
// gate does not apply (no merge policy or quality gates). Otherwise the result
// is pass; skipped when the policy was waived (never reported as a pass); fail
// when a requirement is unmet; or blocked when nothing is known to be wrong
// but the gate could not decide (PR or checks unreadable, checks unfinished).
// Fail and blocked both stop acceptance. It has LandingCheck's side effect of
// commenting the remedy on a failure, and emits no event: the caller that
// owns the transition records the evaluation with it.
func (s *TaskService) LandingEvaluate(ctx context.Context, task *sqlstore.TaskRecord) sqlstore.GateEvaluation {
	policy := landingPolicy(task)
	if !policy.Required() {
		return sqlstore.GateEvaluation{}
	}
	sc := s.sourceControl
	if sc == nil {
		gh := landing.GH{}
		if task.ProjectID.Valid {
			if p, err := s.store.GetProject(task.ProjectID.String); err == nil {
				gh.Dir = p.RepoPath
			}
		}
		sc = gh
	}
	v := landing.Evaluate(ctx, sc, policy, landing.ParseEvidence(task.Metadata.String))
	if v.OK && !v.Waived {
		// Merged work can still leave edits behind in the checkout; they would
		// be lost with it.
		if dirty, undecided := landing.CheckCleanState(ctx, task.WorkingDir); len(dirty) > 0 {
			v = landing.Verdict{Missing: dirty, Undecided: undecided}
		}
	}
	ev := sqlstore.GateEvaluation{Kind: sqlstore.GateKindLanding}
	switch {
	case v.Waived:
		ev.Result, ev.Reason = sqlstore.GateSkipped, "landing evidence waived"
	case v.OK:
		ev.Result = sqlstore.GatePass
	default:
		ev.Result, ev.Blocking = sqlstore.GateFail, true
		if v.Undecided {
			ev.Result = sqlstore.GateBlocked
		}
		ev.Reason, ev.Missing = v.Reason(), v.Missing
		s.escalateLanding(task.ID, ev.Reason)
	}
	return ev
}

// landingPolicy reads a task's merge policy and quality gates.
func landingPolicy(task *sqlstore.TaskRecord) landing.Policy {
	policy := landing.Policy{OnDoneMerge: task.OnDoneMerge}
	if task.QualityGates.Valid && task.QualityGates.String != "" {
		_ = json.Unmarshal([]byte(task.QualityGates.String), &policy.QualityGates)
	}
	return policy
}

// forcedGateSkips reports the gates a forced transition steps over: a forced
// done on a task that has a landing policy skips the landing gate. It is
// recorded as skipped with the reason, never as a pass. Nothing is evaluated.
func forcedGateSkips(task *sqlstore.TaskRecord, newStatus string) []sqlstore.GateEvaluation {
	if newStatus != "done" || task.Status == "done" || !landingPolicy(task).Required() {
		return nil
	}
	return []sqlstore.GateEvaluation{{
		Kind: sqlstore.GateKindLanding, Result: sqlstore.GateSkipped,
		Reason: "force transition bypassed the landing gate",
	}}
}

// EnforceLanding applies the landing gate to a done transition. When evidence
// is missing it moves the task to blocked with the unmet requirements as the
// reason and returns a TransitionError the caller surfaces; the evaluation is
// recorded in the same transaction as that block. When the gate passes or was
// waived it changes nothing and returns the evaluation for the caller to
// append in the transaction of the done it allows (nil when the task has no
// landing policy). ForceTransition does not call it: force is the explicit
// override.
func (s *TaskService) EnforceLanding(ctx context.Context, task *sqlstore.TaskRecord) ([]sqlstore.GateEvaluation, error) {
	ev := s.LandingEvaluate(ctx, task)
	if !ev.Blocking {
		if ev.Kind == "" {
			return nil, nil
		}
		return []sqlstore.GateEvaluation{ev}, nil
	}
	trigger := sqlstore.Trigger{Kind: sqlstore.TriggerHook, Ref: "landing-gate"}
	reason := ev.Reason
	w, err := s.store.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Rollback()
	// The gate was evaluated against the status read earlier. If another
	// request moved the task meanwhile, do not write events about a decision
	// that no longer applies (two concurrent dones would otherwise each record
	// a failure for one block).
	if cur, err := w.TaskStatusLocked(task.ID); err != nil {
		return nil, err
	} else if cur != task.Status {
		return nil, &TransitionError{From: task.Status, To: "done", Message: fmt.Sprintf("task changed from %s to %s while the landing gate ran; retry", task.Status, cur)}
	}
	deciding, err := w.AppendGateEvaluations(task.ID, 0, []sqlstore.GateEvaluation{ev}, trigger)
	if err != nil {
		return nil, err
	}
	if err := w.TransitionTaskOpts(task.ID, "blocked", sqlstore.TransitionOpts{Reason: &reason, Trigger: trigger, CausationID: deciding}); err != nil {
		return nil, err
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	s.notifyTransition(ctx, task.ID, task.Status, "blocked")
	return nil, &TransitionError{
		From:    task.Status,
		To:      "done",
		Message: fmt.Sprintf("%s. Task moved to blocked and escalated; satisfy the evidence and retry, or use force=true to override", reason),
	}
}

// transitionWithGates moves a task and records the gate evaluations that
// allowed it in the same transaction, ahead of the task.state_changed, which
// names the deciding evaluation as its cause. It first confirms the task is
// still in the status the decision was made against.
func (s *TaskService) transitionWithGates(ctx context.Context, id, from, to string, evals []sqlstore.GateEvaluation) error {
	w, err := s.store.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()
	if cur, err := w.TaskStatusLocked(id); err != nil {
		return err
	} else if cur != from {
		return &TransitionError{From: from, To: to, Message: fmt.Sprintf("task changed from %s to %s during the transition; retry", from, cur)}
	}
	deciding, err := w.AppendGateEvaluations(id, 0, evals, sqlstore.ResolveTrigger(ctx))
	if err != nil {
		return err
	}
	if err := w.TransitionTaskOpts(id, to, sqlstore.TransitionOpts{CausationID: deciding}); err != nil {
		return err
	}
	return w.Commit()
}

// escalateLanding records the block for the chat proxy agent. Until the proxy
// URN is configured (TORQUE_ESCALATION_PROXY) it is a task comment only.
func (s *TaskService) escalateLanding(taskID, reason string) {
	target := os.Getenv("TORQUE_ESCALATION_PROXY")
	if target == "" {
		target = "chat proxy agent (URN not configured)"
	}
	err := s.store.AddComment(&sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   taskID,
		Author:     "torque-landing",
		Content:    fmt.Sprintf("Landing gate blocked this task. Escalated to %s.\n\n%s", target, reason),
	})
	if err != nil {
		log.Printf("[landing] escalation comment for %s failed: %v", taskID, err)
	}
}

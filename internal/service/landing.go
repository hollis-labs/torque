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
	policy := landing.Policy{OnDoneMerge: task.OnDoneMerge}
	if task.QualityGates.Valid && task.QualityGates.String != "" {
		_ = json.Unmarshal([]byte(task.QualityGates.String), &policy.QualityGates)
	}
	if !policy.Required() {
		return ""
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
		if dirty := landing.CheckClean(ctx, task.WorkingDir); len(dirty) > 0 {
			v = landing.Verdict{Missing: dirty}
		}
	}
	if v.OK {
		return ""
	}
	reason := v.Reason()
	s.escalateLanding(task.ID, reason)
	return reason
}

// EnforceLanding applies the landing gate to a done transition. When evidence
// is missing it moves the task to blocked with the unmet requirements as the
// reason and returns a TransitionError the caller surfaces. ForceTransition
// does not call it: force is the explicit override.
func (s *TaskService) EnforceLanding(ctx context.Context, task *sqlstore.TaskRecord) error {
	reason := s.LandingCheck(ctx, task)
	if reason == "" {
		return nil
	}
	if err := s.store.TransitionTaskOpts(ctx, task.ID, "blocked", sqlstore.TransitionOpts{Reason: &reason, Trigger: sqlstore.Trigger{Kind: sqlstore.TriggerHook, Ref: "landing-gate"}}); err != nil {
		return err
	}
	s.notifyTransition(ctx, task.ID, task.Status, "blocked")
	return &TransitionError{
		From:    task.Status,
		To:      "done",
		Message: fmt.Sprintf("%s. Task moved to blocked and escalated; satisfy the evidence and retry, or use force=true to override", reason),
	}
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

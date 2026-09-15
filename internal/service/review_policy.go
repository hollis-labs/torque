package service

import (
	"database/sql"
	"fmt"
)

const (
	ReviewModeEndAgent = "end_agent"
	ReviewModeParent   = "parent"
)

// ReviewPolicy is the effective review handling for a task that reaches
// review. Absence of metadata.review preserves the existing end-agent path.
type ReviewPolicy struct {
	Mode                    string `json:"mode"`
	EnqueueInternalReviewer bool   `json:"enqueue_internal_reviewer"`
}

func defaultReviewPolicy() ReviewPolicy {
	return ReviewPolicy{Mode: ReviewModeEndAgent, EnqueueInternalReviewer: true}
}

// EffectiveReviewPolicy returns the review policy encoded in task metadata.
// The Torque-owned contract is:
//
//	{"review":{"mode":"parent"}}
//
// mode=end_agent is accepted as an explicit spelling of the default.
func EffectiveReviewPolicy(metadata map[string]any) (ReviewPolicy, error) {
	policy := defaultReviewPolicy()
	if metadata == nil {
		return policy, nil
	}

	raw, ok := metadata["review"]
	if !ok {
		return policy, nil
	}
	review, ok := raw.(map[string]any)
	if !ok {
		return policy, fmt.Errorf("metadata.review must be an object with mode %q or %q", ReviewModeParent, ReviewModeEndAgent)
	}
	rawMode, ok := review["mode"]
	if !ok {
		return policy, fmt.Errorf("metadata.review.mode is required when metadata.review is present; expected %q or %q", ReviewModeParent, ReviewModeEndAgent)
	}
	mode, ok := rawMode.(string)
	if !ok {
		return policy, fmt.Errorf("metadata.review.mode must be a string; expected %q or %q", ReviewModeParent, ReviewModeEndAgent)
	}
	switch mode {
	case ReviewModeParent:
		return ReviewPolicy{Mode: ReviewModeParent, EnqueueInternalReviewer: false}, nil
	case ReviewModeEndAgent:
		return policy, nil
	default:
		return policy, fmt.Errorf("invalid metadata.review.mode %q; expected %q or %q", mode, ReviewModeParent, ReviewModeEndAgent)
	}
}

func EffectiveReviewPolicyForKind(kind string, metadata map[string]any) (ReviewPolicy, error) {
	policy, err := EffectiveReviewPolicy(metadata)
	if err != nil {
		return policy, err
	}
	if kind != "agent" {
		policy.EnqueueInternalReviewer = false
	}
	return policy, nil
}

func EffectiveReviewPolicyFromJSON(metadata sql.NullString) (ReviewPolicy, error) {
	if !metadata.Valid || metadata.String == "" {
		return defaultReviewPolicy(), nil
	}
	var md map[string]any
	if err := unmarshalJSON([]byte(metadata.String), &md); err != nil {
		return defaultReviewPolicy(), fmt.Errorf("invalid metadata JSON: %w", err)
	}
	return EffectiveReviewPolicy(md)
}

func EffectiveReviewPolicyForKindFromJSON(kind string, metadata sql.NullString) (ReviewPolicy, error) {
	if !metadata.Valid || metadata.String == "" {
		policy := defaultReviewPolicy()
		if kind != "agent" {
			policy.EnqueueInternalReviewer = false
		}
		return policy, nil
	}
	var md map[string]any
	if err := unmarshalJSON([]byte(metadata.String), &md); err != nil {
		return defaultReviewPolicy(), fmt.Errorf("invalid metadata JSON: %w", err)
	}
	return EffectiveReviewPolicyForKind(kind, md)
}

package scheduler_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
)

func TestDeliverableCheckerAllPresent(t *testing.T) {
	checker := scheduler.NewDeliverableChecker()

	required := []executor.Deliverable{
		{Type: "diff", Required: true},
		{Type: "test-results", Required: true},
	}

	artifacts := []executor.Artifact{
		{Type: "diff", Content: "--- a/file\n+++ b/file"},
		{Type: "test-results", Content: "PASS"},
	}

	missing := checker.Check(required, artifacts)
	assert.Len(t, missing, 0)
}

func TestDeliverableCheckerMissingRequired(t *testing.T) {
	checker := scheduler.NewDeliverableChecker()

	required := []executor.Deliverable{
		{Type: "diff", Required: true},
		{Type: "test-results", Required: true},
		{Type: "screenshot", Required: true, Description: "Before and after"},
	}

	artifacts := []executor.Artifact{
		{Type: "diff", Content: "some diff"},
	}

	missing := checker.Check(required, artifacts)
	assert.Len(t, missing, 2)
	assert.Equal(t, "test-results", missing[0].Type)
	assert.Equal(t, "screenshot", missing[1].Type)
}

func TestDeliverableCheckerOptionalNotRequired(t *testing.T) {
	checker := scheduler.NewDeliverableChecker()

	required := []executor.Deliverable{
		{Type: "diff", Required: true},
		{Type: "pr-link", Required: false},
	}

	artifacts := []executor.Artifact{
		{Type: "diff", Content: "some diff"},
	}

	missing := checker.Check(required, artifacts)
	assert.Len(t, missing, 0, "optional deliverables should not appear in missing")
}

func TestDeliverableCheckerNoRequirements(t *testing.T) {
	checker := scheduler.NewDeliverableChecker()

	missing := checker.Check(nil, nil)
	assert.Len(t, missing, 0)
}

func TestDeliverableCheckerEmptyArtifacts(t *testing.T) {
	checker := scheduler.NewDeliverableChecker()

	required := []executor.Deliverable{
		{Type: "diff", Required: true},
	}

	missing := checker.Check(required, nil)
	assert.Len(t, missing, 1)
	assert.Equal(t, "diff", missing[0].Type)
}

func TestDeliverableCheckerHasAllRequired(t *testing.T) {
	checker := scheduler.NewDeliverableChecker()

	required := []executor.Deliverable{
		{Type: "diff", Required: true},
		{Type: "test-results", Required: true},
	}

	artifacts := []executor.Artifact{
		{Type: "diff", Content: "content"},
		{Type: "test-results", Content: "PASS"},
		{Type: "note", Content: "extra"},
	}

	assert.True(t, checker.HasAllRequired(required, artifacts))
}

func TestDeliverableCheckerHasAllRequiredFalse(t *testing.T) {
	checker := scheduler.NewDeliverableChecker()

	required := []executor.Deliverable{
		{Type: "diff", Required: true},
		{Type: "test-results", Required: true},
	}

	artifacts := []executor.Artifact{
		{Type: "diff", Content: "content"},
	}

	assert.False(t, checker.HasAllRequired(required, artifacts))
}

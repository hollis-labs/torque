package hitl_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/hitl"
)

func TestLookupCanonicalWorkflowSchemas(t *testing.T) {
	def := hitl.Lookup(hitl.TypePRReview)

	require.True(t, def.Known)
	assert.Equal(t, hitl.TypePRReview, def.Type)
	assert.Equal(t, hitl.EnforcementAdvisory, def.TaskMetadata.EnforcementMode)
	assert.True(t, def.TaskMetadata.Requirements.ResponseRequired)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(def.PayloadSchema, &payload))
	assert.Equal(t, "object", payload["type"])
	assert.Contains(t, payload["required"], "pr_url")

	var response map[string]any
	require.NoError(t, json.Unmarshal(def.ResponseSchema, &response))
	assert.Contains(t, response["required"], "decision")
}

func TestLookupUnknownWorkflowDegradesToOpenObject(t *testing.T) {
	def := hitl.Lookup("vendor.custom")

	require.False(t, def.Known)
	assert.Equal(t, "vendor.custom", def.Type)
	assert.Equal(t, hitl.EnforcementNone, def.TaskMetadata.EnforcementMode)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(def.PayloadSchema, &payload))
	assert.Equal(t, "object", payload["type"])
	assert.Equal(t, true, payload["additionalProperties"])
}

func TestListCanonicalWorkflows(t *testing.T) {
	defs := hitl.ListCanonical()
	require.Len(t, defs, 3)
	assert.Equal(t, []string{hitl.TypePRReview, hitl.TypeApproval, hitl.TypeMessage}, []string{
		defs[0].Type,
		defs[1].Type,
		defs[2].Type,
	})
}

func TestTypedPRReviewRoundTrip(t *testing.T) {
	payload := hitl.PRReviewPayload{
		PRURL:     "https://github.com/acme/app/pull/42",
		Title:     "Fix checkout",
		Checklist: []string{"tests pass"},
	}
	payloadJSON, err := json.Marshal(payload)
	require.NoError(t, err)

	var decodedPayload hitl.PRReviewPayload
	require.NoError(t, json.Unmarshal(payloadJSON, &decodedPayload))
	assert.Equal(t, payload.PRURL, decodedPayload.PRURL)

	response := hitl.PRReviewResponse{
		Decision: "request_changes",
		Summary:  "Needs one fix.",
		RequiredChanges: []string{
			"Add regression coverage.",
		},
	}
	responseJSON, err := json.Marshal(response)
	require.NoError(t, err)

	var decodedResponse hitl.PRReviewResponse
	require.NoError(t, json.Unmarshal(responseJSON, &decodedResponse))
	assert.Equal(t, response.Decision, decodedResponse.Decision)
	assert.Equal(t, response.RequiredChanges, decodedResponse.RequiredChanges)
}

func TestParseRequiredWorkflowPolicyNestedAndLegacy(t *testing.T) {
	nested, ok, err := hitl.ParseRequiredWorkflowFromMetadata(map[string]any{
		"hitl": map[string]any{
			"required_workflow": map[string]any{
				"workflow_type":    hitl.TypeApproval,
				"enforcement_mode": hitl.EnforcementRequired,
				"requirements":     map[string]any{"response_required": true},
				"reason":           "permission before restart",
			},
		},
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, hitl.TypeApproval, nested.WorkflowType)
	assert.Equal(t, hitl.EnforcementRequired, nested.EnforcementMode)
	assert.Equal(t, 1, nested.Requirements.MinResponders, "response_required defaults min_responders to one")

	legacy, ok, err := hitl.ParseRequiredWorkflowFromMetadata(map[string]any{
		"hitl": map[string]any{
			"workflow_type":    hitl.TypePRReview,
			"enforcement_mode": hitl.EnforcementAdvisory,
			"requirements":     map[string]any{"response_required": true, "min_responders": 1},
		},
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, hitl.TypePRReview, legacy.WorkflowType)
}

func TestParseRequiredWorkflowPolicyRejectsRequiredMultiResponder(t *testing.T) {
	_, ok, err := hitl.ParseRequiredWorkflowFromMetadata(map[string]any{
		"hitl": map[string]any{
			"required_workflow": map[string]any{
				"workflow_type":    hitl.TypeApproval,
				"enforcement_mode": hitl.EnforcementRequired,
				"requirements":     map[string]any{"response_required": true, "min_responders": 2},
			},
		},
	})
	require.Error(t, err)
	assert.True(t, ok)
	assert.Contains(t, err.Error(), "process-level")
}

func TestEvaluateRequiredWorkflowSatisfaction(t *testing.T) {
	policy := hitl.RequiredWorkflowPolicy{
		WorkflowType:    hitl.TypeApproval,
		EnforcementMode: hitl.EnforcementRequired,
		Requirements: hitl.WorkflowRequirements{
			ResponseRequired:            true,
			AllowedResponderSourceTypes: []string{"user"},
			MinResponders:               1,
		},
	}

	pending := hitl.CheckpointSatisfiesRequiredWorkflow(policy, hitl.CheckpointState{
		Type:   hitl.TypeApproval,
		Status: "pending",
	})
	assert.False(t, pending.Satisfied)
	assert.True(t, pending.Pending)

	disallowed := hitl.CheckpointSatisfiesRequiredWorkflow(policy, hitl.CheckpointState{
		Type:                hitl.TypeApproval,
		Status:              "responded",
		ResponderSourceType: "agent",
	})
	assert.False(t, disallowed.Satisfied)
	assert.Contains(t, disallowed.Reasons, "responder_source_type is not allowed for required workflow")

	allowed := hitl.CheckpointSatisfiesRequiredWorkflow(policy, hitl.CheckpointState{
		Type:                hitl.TypeApproval,
		Status:              "responded",
		ResponderSourceType: "user",
	})
	assert.True(t, allowed.Satisfied)
}

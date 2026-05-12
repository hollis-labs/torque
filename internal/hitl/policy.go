package hitl

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MetadataKey         = "hitl"
	RequiredWorkflowKey = "required_workflow"
)

// RequiredWorkflowPolicy is an optional task-level contract stored under
// metadata.hitl.required_workflow. The older metadata.hitl direct shape
// {workflow_type, requirements, enforcement_mode} is still accepted so the
// CW-20260510-0118 registry examples remain compatible.
type RequiredWorkflowPolicy struct {
	WorkflowType    string               `json:"workflow_type"`
	Requirements    WorkflowRequirements `json:"requirements"`
	EnforcementMode EnforcementMode      `json:"enforcement_mode"`
	Reason          string               `json:"reason,omitempty"`
}

// CheckpointState is the small, storage-agnostic checkpoint projection needed
// to evaluate whether a checkpoint can satisfy a RequiredWorkflowPolicy.
type CheckpointState struct {
	Type                string
	Status              string
	ResponderSourceType string
}

type WorkflowSatisfaction struct {
	Required        bool            `json:"required"`
	Satisfied       bool            `json:"satisfied"`
	Pending         bool            `json:"pending"`
	WorkflowType    string          `json:"workflow_type,omitempty"`
	EnforcementMode EnforcementMode `json:"enforcement_mode,omitempty"`
	Reasons         []string        `json:"reasons,omitempty"`
}

var validPolicyResponderSourceTypes = map[string]bool{
	"agent":   true,
	"user":    true,
	"api":     true,
	"system":  true,
	"webhook": true,
	"import":  true,
}

func ParseRequiredWorkflowFromMetadata(metadata map[string]any) (RequiredWorkflowPolicy, bool, error) {
	raw, ok := metadata[MetadataKey]
	if !ok || raw == nil {
		return RequiredWorkflowPolicy{}, false, nil
	}
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return RequiredWorkflowPolicy{}, false, fmt.Errorf("metadata.%s must be a JSON object", MetadataKey)
	}
	var hitlMeta map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &hitlMeta); err != nil {
		return RequiredWorkflowPolicy{}, false, fmt.Errorf("metadata.%s must be a JSON object", MetadataKey)
	}

	var policy RequiredWorkflowPolicy
	if rawPolicy, ok := hitlMeta[RequiredWorkflowKey]; ok {
		if err := json.Unmarshal(rawPolicy, &policy); err != nil {
			return RequiredWorkflowPolicy{}, false, fmt.Errorf("metadata.%s.%s is invalid: %w", MetadataKey, RequiredWorkflowKey, err)
		}
		return NormalizeRequiredWorkflowPolicy(policy)
	}

	if _, ok := hitlMeta["workflow_type"]; !ok {
		return RequiredWorkflowPolicy{}, false, nil
	}
	if err := json.Unmarshal(rawJSON, &policy); err != nil {
		return RequiredWorkflowPolicy{}, false, fmt.Errorf("metadata.%s required workflow is invalid: %w", MetadataKey, err)
	}
	return NormalizeRequiredWorkflowPolicy(policy)
}

func NormalizeRequiredWorkflowPolicy(policy RequiredWorkflowPolicy) (RequiredWorkflowPolicy, bool, error) {
	policy.WorkflowType = normalizeType(policy.WorkflowType)
	if policy.WorkflowType == "" {
		return RequiredWorkflowPolicy{}, true, fmt.Errorf("workflow_type is required")
	}
	if policy.EnforcementMode == "" {
		policy.EnforcementMode = EnforcementAdvisory
	}
	if !validEnforcementMode(policy.EnforcementMode) {
		return RequiredWorkflowPolicy{}, true, fmt.Errorf("invalid enforcement_mode %q", policy.EnforcementMode)
	}
	if policy.Requirements.MinResponders < 0 {
		return RequiredWorkflowPolicy{}, true, fmt.Errorf("requirements.min_responders must be >= 0")
	}
	if policy.Requirements.ResponseRequired && policy.Requirements.MinResponders == 0 {
		policy.Requirements.MinResponders = 1
	}
	for _, sourceType := range policy.Requirements.AllowedResponderSourceTypes {
		sourceType = strings.TrimSpace(sourceType)
		if !validPolicyResponderSourceTypes[sourceType] {
			return RequiredWorkflowPolicy{}, true, fmt.Errorf("invalid allowed responder source type %q", sourceType)
		}
	}
	if policy.EnforcementMode == EnforcementRequired && policy.Requirements.MinResponders > 1 {
		return RequiredWorkflowPolicy{}, true, fmt.Errorf("required multi-responder workflows are process-level only in the current checkpoint substrate")
	}
	return policy, true, nil
}

func EvaluateRequiredWorkflow(policy RequiredWorkflowPolicy, checkpoints []CheckpointState) WorkflowSatisfaction {
	policy, present, err := NormalizeRequiredWorkflowPolicy(policy)
	if !present {
		return WorkflowSatisfaction{}
	}
	out := WorkflowSatisfaction{
		Required:        policy.EnforcementMode == EnforcementRequired,
		WorkflowType:    policy.WorkflowType,
		EnforcementMode: policy.EnforcementMode,
	}
	if err != nil {
		out.Reasons = append(out.Reasons, err.Error())
		return out
	}

	requiredResponses := policy.Requirements.MinResponders
	if policy.Requirements.ResponseRequired && requiredResponses == 0 {
		requiredResponses = 1
	}
	matchingResponses := 0
	matchingCheckpoints := 0
	for _, cp := range checkpoints {
		if normalizeType(cp.Type) != policy.WorkflowType {
			continue
		}
		matchingCheckpoints++
		switch cp.Status {
		case "pending":
			out.Pending = true
		case "responded":
			if responderAllowed(policy.Requirements.AllowedResponderSourceTypes, cp.ResponderSourceType) {
				matchingResponses++
			} else {
				out.Reasons = append(out.Reasons, "responder_source_type is not allowed for required workflow")
			}
		}
	}

	if !policy.Requirements.ResponseRequired {
		out.Satisfied = matchingCheckpoints > 0
		if !out.Satisfied {
			out.Reasons = append(out.Reasons, "no checkpoint with required workflow type")
		}
		return out
	}
	if matchingResponses >= requiredResponses {
		out.Satisfied = true
		return out
	}
	if matchingCheckpoints == 0 {
		out.Reasons = append(out.Reasons, "no checkpoint with required workflow type")
	} else if out.Pending {
		out.Reasons = append(out.Reasons, "required workflow response is still pending")
	} else {
		out.Reasons = append(out.Reasons, "required workflow response is missing")
	}
	return out
}

func CheckpointSatisfiesRequiredWorkflow(policy RequiredWorkflowPolicy, checkpoint CheckpointState) WorkflowSatisfaction {
	return EvaluateRequiredWorkflow(policy, []CheckpointState{checkpoint})
}

func validEnforcementMode(mode EnforcementMode) bool {
	switch mode {
	case EnforcementNone, EnforcementAdvisory, EnforcementRequired:
		return true
	default:
		return false
	}
}

func responderAllowed(allowed []string, sourceType string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == sourceType {
			return true
		}
	}
	return false
}

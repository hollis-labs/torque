// Package hitl defines the shared human-in-the-loop workflow vocabulary used
// by checkpoint-backed flows. It is a contract/registry layer only: payload
// and response validation/enforcement remains with callers.
package hitl

import (
	"encoding/json"
	"strings"
)

const (
	TypePRReview = "pr_review"
	TypeApproval = "approval"
	TypeMessage  = "message"
)

type EnforcementMode string

const (
	EnforcementNone     EnforcementMode = "none"
	EnforcementAdvisory EnforcementMode = "advisory"
	EnforcementRequired EnforcementMode = "required"
)

type WorkflowRequirements struct {
	ResponseRequired            bool     `json:"response_required"`
	AllowedResponderSourceTypes []string `json:"allowed_responder_source_types,omitempty"`
	MinResponders               int      `json:"min_responders,omitempty"`
}

type TaskMetadataContract struct {
	Key                      string               `json:"key"`
	WorkflowTypeKey          string               `json:"workflow_type_key"`
	RequiredWorkflowKey      string               `json:"required_workflow_key"`
	RequirementsKey          string               `json:"requirements_key"`
	EnforcementKey           string               `json:"enforcement_key"`
	Requirements             WorkflowRequirements `json:"requirements"`
	EnforcementMode          EnforcementMode      `json:"enforcement_mode"`
	DeterministicEnforcement []string             `json:"deterministic_enforcement"`
	ProcessLevelEnforcement  []string             `json:"process_level_enforcement"`
	BehaviorReserved         bool                 `json:"behavior_reserved"`
}

type WorkflowDefinition struct {
	Type           string               `json:"type"`
	Known          bool                 `json:"known"`
	Title          string               `json:"title"`
	Description    string               `json:"description"`
	PayloadSchema  json.RawMessage      `json:"payload_schema"`
	ResponseSchema json.RawMessage      `json:"response_schema"`
	TaskMetadata   TaskMetadataContract `json:"task_metadata"`
}

type PRReviewPayload struct {
	PRURL     string   `json:"pr_url"`
	Title     string   `json:"title,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Checklist []string `json:"checklist,omitempty"`
}

type PRReviewResponse struct {
	Decision        string   `json:"decision"`
	Summary         string   `json:"summary,omitempty"`
	Comments        []string `json:"comments,omitempty"`
	RequiredChanges []string `json:"required_changes,omitempty"`
}

type ApprovalPayload struct {
	Title   string         `json:"title"`
	Prompt  string         `json:"prompt"`
	Context map[string]any `json:"context,omitempty"`
	Options []string       `json:"options,omitempty"`
}

type ApprovalResponse struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment,omitempty"`
}

type MessagePayload struct {
	Subject  string         `json:"subject,omitempty"`
	Message  string         `json:"message"`
	Severity string         `json:"severity,omitempty"`
	Context  map[string]any `json:"context,omitempty"`
}

type MessageResponse struct {
	Acknowledged bool   `json:"acknowledged"`
	Reply        string `json:"reply,omitempty"`
}

var canonicalOrder = []string{TypePRReview, TypeApproval, TypeMessage}

var canonicalDefinitions = map[string]WorkflowDefinition{
	TypePRReview: {
		Type:           TypePRReview,
		Known:          true,
		Title:          "Pull request review",
		Description:    "Checkpoint flow for reviewing a pull request and returning an explicit review decision.",
		PayloadSchema:  rawSchema(prReviewPayloadSchema),
		ResponseSchema: rawSchema(prReviewResponseSchema),
		TaskMetadata:   taskMetadataContract(EnforcementAdvisory, WorkflowRequirements{ResponseRequired: true, AllowedResponderSourceTypes: []string{"user", "agent", "api"}, MinResponders: 1}),
	},
	TypeApproval: {
		Type:           TypeApproval,
		Known:          true,
		Title:          "Approval",
		Description:    "Checkpoint flow for a yes/no style approval gate outside pull request review.",
		PayloadSchema:  rawSchema(approvalPayloadSchema),
		ResponseSchema: rawSchema(approvalResponseSchema),
		TaskMetadata:   taskMetadataContract(EnforcementAdvisory, WorkflowRequirements{ResponseRequired: true, AllowedResponderSourceTypes: []string{"user", "agent", "api"}, MinResponders: 1}),
	},
	TypeMessage: {
		Type:           TypeMessage,
		Known:          true,
		Title:          "Message",
		Description:    "Checkpoint flow for sending a message that may optionally be acknowledged or replied to.",
		PayloadSchema:  rawSchema(messagePayloadSchema),
		ResponseSchema: rawSchema(messageResponseSchema),
		TaskMetadata:   taskMetadataContract(EnforcementNone, WorkflowRequirements{ResponseRequired: false, AllowedResponderSourceTypes: []string{"user", "agent", "api"}}),
	},
}

func Lookup(typ string) WorkflowDefinition {
	key := normalizeType(typ)
	if def, ok := canonicalDefinitions[key]; ok {
		return cloneDefinition(def)
	}
	return Unknown(key)
}

func PayloadSchema(typ string) json.RawMessage {
	return cloneRaw(Lookup(typ).PayloadSchema)
}

func ResponseSchema(typ string) json.RawMessage {
	return cloneRaw(Lookup(typ).ResponseSchema)
}

func ListCanonical() []WorkflowDefinition {
	out := make([]WorkflowDefinition, 0, len(canonicalOrder))
	for _, typ := range canonicalOrder {
		out = append(out, Lookup(typ))
	}
	return out
}

func Unknown(typ string) WorkflowDefinition {
	key := normalizeType(typ)
	if key == "" {
		key = "unknown"
	}
	return WorkflowDefinition{
		Type:           key,
		Known:          false,
		Title:          "Unknown workflow",
		Description:    "Unregistered checkpoint workflow. Treat payload and response bodies as opaque JSON objects.",
		PayloadSchema:  rawSchema(openObjectSchema),
		ResponseSchema: rawSchema(openObjectSchema),
		TaskMetadata:   taskMetadataContract(EnforcementNone, WorkflowRequirements{}),
	}
}

func normalizeType(typ string) string {
	return strings.TrimSpace(typ)
}

func taskMetadataContract(mode EnforcementMode, req WorkflowRequirements) TaskMetadataContract {
	return TaskMetadataContract{
		Key:                 MetadataKey,
		WorkflowTypeKey:     "workflow_type",
		RequiredWorkflowKey: RequiredWorkflowKey,
		RequirementsKey:     "requirements",
		EnforcementKey:      "enforcement_mode",
		Requirements:        req,
		EnforcementMode:     mode,
		DeterministicEnforcement: []string{
			"metadata policy parsing",
			"single-responder required workflow satisfaction",
			"allowed responder source type on required workflow response",
		},
		ProcessLevelEnforcement: []string{
			"emitting the checkpoint before a sensitive step",
			"choosing when a surprise requires human guidance",
			"multi-responder approval quorum",
		},
		BehaviorReserved: true,
	}
}

func rawSchema(s string) json.RawMessage {
	return json.RawMessage(s)
}

func cloneDefinition(def WorkflowDefinition) WorkflowDefinition {
	def.PayloadSchema = cloneRaw(def.PayloadSchema)
	def.ResponseSchema = cloneRaw(def.ResponseSchema)
	def.TaskMetadata.Requirements.AllowedResponderSourceTypes = append(
		[]string(nil),
		def.TaskMetadata.Requirements.AllowedResponderSourceTypes...,
	)
	return def
}

func cloneRaw(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}
	out := make(json.RawMessage, len(in))
	copy(out, in)
	return out
}

const openObjectSchema = `{
  "type": "object",
  "additionalProperties": true
}`

const prReviewPayloadSchema = `{
  "type": "object",
  "additionalProperties": true,
  "required": ["pr_url"],
  "properties": {
    "pr_url": { "type": "string", "description": "Pull request URL." },
    "title": { "type": "string" },
    "summary": { "type": "string" },
    "branch": { "type": "string" },
    "checklist": { "type": "array", "items": { "type": "string" } }
  }
}`

const prReviewResponseSchema = `{
  "type": "object",
  "additionalProperties": true,
  "required": ["decision"],
  "properties": {
    "decision": { "type": "string", "enum": ["approve", "request_changes", "comment"] },
    "summary": { "type": "string" },
    "comments": { "type": "array", "items": { "type": "string" } },
    "required_changes": { "type": "array", "items": { "type": "string" } }
  }
}`

const approvalPayloadSchema = `{
  "type": "object",
  "additionalProperties": true,
  "required": ["title", "prompt"],
  "properties": {
    "title": { "type": "string" },
    "prompt": { "type": "string" },
    "context": { "type": "object", "additionalProperties": true },
    "options": { "type": "array", "items": { "type": "string" } }
  }
}`

const approvalResponseSchema = `{
  "type": "object",
  "additionalProperties": true,
  "required": ["decision"],
  "properties": {
    "decision": { "type": "string", "enum": ["approved", "rejected", "needs_info"] },
    "comment": { "type": "string" }
  }
}`

const messagePayloadSchema = `{
  "type": "object",
  "additionalProperties": true,
  "required": ["message"],
  "properties": {
    "subject": { "type": "string" },
    "message": { "type": "string" },
    "severity": { "type": "string", "enum": ["info", "warning", "urgent"] },
    "context": { "type": "object", "additionalProperties": true }
  }
}`

const messageResponseSchema = `{
  "type": "object",
  "additionalProperties": true,
  "required": ["acknowledged"],
  "properties": {
    "acknowledged": { "type": "boolean" },
    "reply": { "type": "string" }
  }
}`

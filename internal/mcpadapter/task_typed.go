package mcpadapter

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hollis-labs/torque/internal/hitl"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

type typedDecodeError struct {
	Field string `json:"field"`
	Error string `json:"error"`
}

type typedTaskRecord map[string]any

func typedTagJSON(t sqlstore.TagRecord) map[string]any {
	return map[string]any{
		"slug":        t.Slug,
		"name":        t.Name,
		"description": t.Description,
		"color":       t.Color,
		"created_at":  t.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":  t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func typedTagsJSON(tags []sqlstore.TagRecord) []map[string]any {
	if tags == nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(tags))
	for _, tag := range tags {
		out = append(out, typedTagJSON(tag))
	}
	return out
}

func typedNullString(ns sql.NullString) any {
	if ns.Valid {
		return ns.String
	}
	return nil
}

func typedNullFloat(nf sql.NullFloat64) any {
	if nf.Valid {
		return nf.Float64
	}
	return nil
}

func typedNullInt(ni sql.NullInt64) any {
	if ni.Valid {
		return ni.Int64
	}
	return nil
}

func typedNullTime(nt sql.NullTime) any {
	if nt.Valid {
		return nt.Time
	}
	return nil
}

func typedDecodeJSON(raw sql.NullString, field string, fallback any, errs *[]typedDecodeError, validate func(any) (any, error)) any {
	if !raw.Valid || raw.String == "" {
		return fallback
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw.String)))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		*errs = append(*errs, typedDecodeError{Field: field, Error: err.Error()})
		return fallback
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		*errs = append(*errs, typedDecodeError{Field: field, Error: "multiple JSON values"})
		return fallback
	}
	if out == nil {
		return fallback
	}
	if validate != nil {
		checked, err := validate(out)
		if err != nil {
			*errs = append(*errs, typedDecodeError{Field: field, Error: err.Error()})
			return fallback
		}
		return checked
	}
	return out
}

func typedStringArray(raw sql.NullString, field string, errs *[]typedDecodeError) any {
	return typedDecodeJSON(raw, field, []any{}, errs, validateStringArray)
}

func typedObject(raw sql.NullString, field string, errs *[]typedDecodeError) any {
	return typedDecodeJSON(raw, field, map[string]any{}, errs, validateObject)
}

func typedStringMap(raw sql.NullString, field string, errs *[]typedDecodeError) any {
	return typedDecodeJSON(raw, field, map[string]string{}, errs, validateStringMap)
}

func typedDeliverables(raw sql.NullString, field string, errs *[]typedDecodeError) any {
	return typedDecodeJSON(raw, field, []service.Deliverable{}, errs, validateDeliverables)
}

func validateStringArray(v any) (any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array of strings")
	}
	out := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("expected string at index %d", i)
		}
		out = append(out, s)
	}
	return out, nil
}

func validateObject(v any) (any, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object")
	}
	return obj, nil
}

func validateStringMap(v any) (any, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object with string values")
	}
	out := make(map[string]string, len(obj))
	for k, item := range obj {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("expected string value at key %q", k)
		}
		out[k] = s
	}
	return out, nil
}

func validateDeliverables(v any) (any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array of deliverable objects")
	}
	out := make([]service.Deliverable, 0, len(arr))
	for i, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected object at index %d", i)
		}
		typ, ok := obj["type"].(string)
		if !ok {
			return nil, fmt.Errorf("expected string type at index %d", i)
		}
		required := false
		if rawRequired, ok := obj["required"]; ok {
			v, ok := rawRequired.(bool)
			if !ok {
				return nil, fmt.Errorf("expected boolean required at index %d", i)
			}
			required = v
		}
		d := service.Deliverable{Type: typ, Required: required}
		if desc, ok := obj["description"]; ok {
			s, ok := desc.(string)
			if !ok {
				return nil, fmt.Errorf("expected string description at index %d", i)
			}
			d.Description = s
		}
		out = append(out, d)
	}
	return out, nil
}

func typedStats(agg *sqlstore.TaskRunAggregate) map[string]any {
	stats := map[string]any{
		"run_count":         0,
		"prompt_tokens":     0,
		"completion_tokens": 0,
		"cost":              0.0,
		"cost_source":       "",
	}
	if agg != nil {
		stats["run_count"] = agg.Count
		stats["prompt_tokens"] = agg.PromptTokens
		stats["completion_tokens"] = agg.CompletionTokens
		stats["cost"] = agg.Cost
		stats["cost_source"] = agg.CostSource
	}
	return stats
}

func typedRequiredWorkflowPolicy(metadata any) (hitl.RequiredWorkflowPolicy, bool) {
	md, ok := metadata.(map[string]any)
	if !ok {
		return hitl.RequiredWorkflowPolicy{}, false
	}
	policy, ok, err := hitl.ParseRequiredWorkflowFromMetadata(md)
	if err != nil {
		return hitl.RequiredWorkflowPolicy{}, false
	}
	return policy, ok
}

func typedTaskFull(t *sqlstore.TaskRecord, tags []sqlstore.TagRecord, deps []string, agg *sqlstore.TaskRunAggregate, subtodos []sqlstore.Subtodo, collectionName string) typedTaskRecord {
	if deps == nil {
		deps = []string{}
	}
	if subtodos == nil {
		subtodos = []sqlstore.Subtodo{}
	}
	var decodeErrs []typedDecodeError
	metadata := typedObject(t.Metadata, "metadata", &decodeErrs)
	body := typedTaskRecord{
		"id":                     t.ID,
		"title":                  t.Title,
		"description":            t.Description,
		"status":                 t.Status,
		"priority":               t.Priority,
		"tags":                   typedTagsJSON(tags),
		"manual":                 t.Manual,
		"executor":               t.Executor,
		"launch_profile":         t.LaunchProfile,
		"agent_profile":          t.AgentProfile,
		"working_dir":            t.WorkingDir,
		"tools":                  typedStringArray(t.Tools, "tools", &decodeErrs),
		"permissions":            typedObject(t.Permissions, "permissions", &decodeErrs),
		"environment":            typedStringMap(t.Environment, "environment", &decodeErrs),
		"system_prompt":          t.SystemPrompt,
		"agent_file":             t.AgentFile,
		"files":                  typedStringArray(t.Files, "files", &decodeErrs),
		"cost_budget":            typedNullFloat(t.CostBudget),
		"max_retries":            t.MaxRetries,
		"max_duration_ms":        typedNullInt(t.MaxDurationMs),
		"token_budget":           typedNullInt(t.TokenBudget),
		"on_done":                t.OnDone,
		"on_fail":                t.OnFail,
		"on_review":              t.OnReview,
		"on_done_merge":          t.OnDoneMerge,
		"escalation_chain":       typedStringArray(t.EscalationChain, "escalation_chain", &decodeErrs),
		"quality_gates":          typedStringArray(t.QualityGates, "quality_gates", &decodeErrs),
		"deliverables":           typedDeliverables(t.Deliverables, "deliverables", &decodeErrs),
		"deliverable_preset":     t.DeliverablePreset,
		"depends_on":             deps,
		"blocked_reason":         t.BlockedReason,
		"metadata":               metadata,
		"effective_review":       typedEffectiveReview(t.Kind, t.Metadata),
		"sprint_id":              typedNullString(t.SprintID),
		"project_id":             typedNullString(t.ProjectID),
		"epic_id":                typedNullString(t.EpicID),
		"created_at":             t.CreatedAt,
		"updated_at":             t.UpdatedAt,
		"kind":                   t.Kind,
		"source_type":            t.SourceType,
		"source_ref":             typedNullString(t.SourceRef),
		"trust":                  t.Trust,
		"checkpoint_mode":        t.CheckpointMode,
		"on_checkpoint_response": t.OnCheckpointResponse,
		"parent_id":              typedNullString(t.ParentID),
		"collection_id":          typedNullString(t.CollectionID),
		"collection_name": func() any {
			if collectionName == "" {
				return nil
			}
			return collectionName
		}(),
		"collection_position":     typedNullInt(t.CollectionPosition),
		"added_to_collections_at": typedNullTime(t.AddedToCollectionsAt),
		"stats":                   typedStats(agg),
		"subtodos":                subtodos,
	}
	if policy, ok := typedRequiredWorkflowPolicy(metadata); ok {
		body["required_workflow_policy"] = policy
	}
	if len(decodeErrs) > 0 {
		body["decode_errors"] = decodeErrs
	}
	return body
}

func typedTaskBrief(t sqlstore.TaskRecord, tagSlugs []string, deps []string) typedTaskRecord {
	if tagSlugs == nil {
		tagSlugs = []string{}
	}
	if deps == nil {
		deps = []string{}
	}
	return typedTaskRecord{
		"id":               t.ID,
		"title":            t.Title,
		"status":           t.Status,
		"priority":         t.Priority,
		"manual":           t.Manual,
		"executor":         t.Executor,
		"launch_profile":   t.LaunchProfile,
		"agent_profile":    t.AgentProfile,
		"kind":             t.Kind,
		"source_type":      t.SourceType,
		"source_ref":       typedNullString(t.SourceRef),
		"trust":            t.Trust,
		"checkpoint_mode":  t.CheckpointMode,
		"parent_id":        typedNullString(t.ParentID),
		"project_id":       typedNullString(t.ProjectID),
		"sprint_id":        typedNullString(t.SprintID),
		"epic_id":          typedNullString(t.EpicID),
		"collection_id":    typedNullString(t.CollectionID),
		"effective_review": typedEffectiveReview(t.Kind, t.Metadata),
		"tags":             tagSlugs,
		"depends_on":       deps,
		"created_at":       t.CreatedAt,
		"updated_at":       t.UpdatedAt,
	}
}

func typedEffectiveReview(kind string, metadata sql.NullString) map[string]any {
	policy, err := service.EffectiveReviewPolicyForKindFromJSON(kind, metadata)
	if err != nil {
		return map[string]any{
			"mode":                      service.ReviewModeEndAgent,
			"enqueue_internal_reviewer": false,
			"error":                     err.Error(),
		}
	}
	return map[string]any{
		"mode":                      policy.Mode,
		"enqueue_internal_reviewer": policy.EnqueueInternalReviewer,
	}
}

func (a *Adapter) typedTaskFullResult(task *sqlstore.TaskRecord) (typedTaskRecord, *mcp.CallToolResult) {
	tags, err := a.svc.Task.ListTags(task.ID)
	if err != nil {
		res, _ := errFromService(err)
		return nil, res
	}
	deps, err := a.svc.Task.ListDependencyIDs(task.ID)
	if err != nil {
		res, _ := errFromService(err)
		return nil, res
	}
	agg, err := a.svc.Run.Aggregate(task.ID)
	if err != nil {
		res, _ := errFromService(err)
		return nil, res
	}
	subs, err := a.svc.Task.ListSubtodos(task.ID)
	if err != nil {
		res, _ := errFromService(err)
		return nil, res
	}
	collectionName := ""
	if task.CollectionID.Valid && task.CollectionID.String != "" {
		names, err := a.svc.Collection.LookupNames([]string{task.CollectionID.String})
		if err != nil {
			res, _ := errFromService(err)
			return nil, res
		}
		collectionName = names[task.CollectionID.String]
	}
	return typedTaskFull(task, tags, deps, agg, subs, collectionName), nil
}

func (a *Adapter) typedTaskListItem(t sqlstore.TaskRecord, full bool) (any, *mcp.CallToolResult) {
	if full {
		rec := t
		return a.typedTaskFullResult(&rec)
	}
	deps, err := a.svc.Task.ListDependencyIDs(t.ID)
	if err != nil {
		res, _ := errFromService(err)
		return nil, res
	}
	return typedTaskBrief(t, briefTagSlugs(a.svc, t.ID), deps), nil
}

func reqTaskFormat(req mcp.CallToolRequest) (string, error) {
	raw, ok := req.GetArguments()["format"]
	if !ok || raw == nil {
		return "legacy", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("format must be a string")
	}
	return validateTaskFormat(s)
}

func validateTaskFormat(raw string) (string, error) {
	switch raw {
	case "", "legacy":
		return "legacy", nil
	case "typed":
		return "typed", nil
	default:
		return "", fmt.Errorf("format must be legacy or typed")
	}
}

func isTypedFormat(reqFormat string) bool {
	return reqFormat == "typed"
}

package service

// UnitOfWorkTemplate is the template for a unit-of-work task (id "unit-of-work").
// The task is created manual=true (the usual rule); approving it is the single
// step of setting manual=false, which lets the scheduler dispatch it, or
// torque_unit_dispatch can start it on demand. Required vars: workstream_id,
// goal, done_when. Everything else in the plan is defaulted here and can be
// edited on the task (metadata.unit_plan). See docs/unit-of-work.md.
func UnitOfWorkTemplate() TemplateCreateInput {
	max := 1
	return TemplateCreateInput{
		ID:          "unit-of-work",
		Name:        "Unit of Work",
		Description: "{{goal}}\n\nDone when:\n{{done_when}}\n\nLaunched by Torque as a Tether team (orchestrator + workers + reviewer). See the execution plan in metadata.unit_plan.",
		Kind:        "agent",
		AutoExecute: false,
		Executor:    "cli",
		OnDone:      "review",
		OnFail:      "retry",
		MaxRetries:  &max,
		MetadataTemplate: map[string]any{
			"unit_plan": map[string]any{
				"workstream_id":  "{{workstream_id}}",
				"goal":           "{{goal}}",
				"done_when":      "{{done_when}}",
				"workers":        1,
				"worker_runtime": "sol",
				"escalate_to":    "@chief",
				"merge_policy":   "reviewer_merges_on_green",
				"risk_tier":      "normal",
			},
		},
		RequiredVars: []string{"workstream_id", "goal", "done_when"},
		Tags:         []string{"unit-of-work"},
	}
}

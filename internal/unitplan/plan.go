// Package unitplan is the execution-plan contract for a unit of work: the
// small structured plan a Torque task carries (metadata key "unit_plan") so
// Torque can launch a Tether team for it with no agent hand-assigning it.
//
// The plan is deliberately minimal. It holds what the team needs to start
// (goal, done-when, scope, escalation target) and the few knobs the dispatcher
// turns into a team shape (worker count and runtime, merge policy, risk tier).
// Everything else a team needs lives on the task itself.
package unitplan

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MetadataKey is the task metadata key that holds the plan.
const MetadataKey = "unit_plan"

// Worker runtimes a plan may prefer. They map to Tether launches pinned in
// teams-policy.yaml (team-worker-sol, team-worker-agy-w1).
const (
	RuntimeSol = "sol"
	RuntimeAgy = "agy"
)

// Merge policies.
const (
	MergeOnGreen = "reviewer_merges_on_green" // default: reviewer merges when CI is green
	MergePROnly  = "pr_only"                  // open a PR, do not merge
	MergeNone    = "none"                     // no landing; evidence only
)

// Risk tiers.
const (
	RiskLow    = "low"
	RiskNormal = "normal"
	RiskHigh   = "high"
)

// Limits. A team is one orchestrator, 1-3 workers and one reviewer: 3-5 agents.
const (
	MinWorkers     = 1
	MaxWorkers     = 3
	DefaultEscalee = "@chief"
	MaxTimeout     = 12 * time.Hour
)

// List accepts a JSON array of strings or a single string. A single string is
// split on newlines, so a template variable can carry a short bullet list.
type List []string

func (l *List) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*l = splitLines(s)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err != nil {
		return fmt.Errorf("expected a string or an array of strings")
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	*l = out
	return nil
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*"))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Int accepts a JSON number or a numeric string (template variables are strings).
type Int int

func (i *Int) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*i = Int(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("expected an integer")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		*i = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("expected an integer, got %q", s)
	}
	*i = Int(n)
	return nil
}

// Bool accepts a JSON bool or "true"/"false".
type Bool bool

func (v *Bool) UnmarshalJSON(b []byte) error {
	var x bool
	if err := json.Unmarshal(b, &x); err == nil {
		*v = Bool(x)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("expected a boolean")
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "false", "no", "0":
		*v = false
	case "true", "yes", "1":
		*v = true
	default:
		return fmt.Errorf("expected a boolean, got %q", s)
	}
	return nil
}

// Scope bounds what the team may touch.
type Scope struct {
	In  List `json:"in,omitempty"`
	Out List `json:"out,omitempty"`
}

// Plan is the execution plan for one unit of work.
type Plan struct {
	WorkstreamID string `json:"workstream_id"`
	Goal         string `json:"goal"`
	DoneWhen     List   `json:"done_when"`
	Scope        Scope  `json:"scope,omitempty"`
	Slices       List   `json:"slices,omitempty"`

	Workers       Int    `json:"workers,omitempty"`        // 1-3, default 1
	WorkerRuntime string `json:"worker_runtime,omitempty"` // sol | agy, default sol

	EscalateTo  string `json:"escalate_to,omitempty"` // default @chief
	Evidence    List   `json:"evidence,omitempty"`    // what the result must show
	MergePolicy string `json:"merge_policy,omitempty"`
	Release     Bool   `json:"release,omitempty"`
	Deploy      Bool   `json:"deploy,omitempty"`
	RiskTier    string `json:"risk_tier,omitempty"`
	// Timeout caps the run (Go duration, e.g. "2h"); empty uses the dispatcher default.
	Timeout string `json:"timeout,omitempty"`
}

// Present reports whether metadata carries a plan.
func Present(meta map[string]any) bool {
	if meta == nil {
		return false
	}
	_, ok := meta[MetadataKey]
	return ok
}

// FromMetadata decodes and validates the plan in task metadata, applying
// defaults. The error names the offending field.
func FromMetadata(meta map[string]any) (Plan, error) {
	raw, ok := meta[MetadataKey]
	if !ok || raw == nil {
		return Plan{}, fmt.Errorf("unit_plan: task has no execution plan (metadata.unit_plan)")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return Plan{}, fmt.Errorf("unit_plan: %v", err)
	}
	return Parse(b)
}

// Parse decodes a plan from JSON, applies defaults and validates it.
func Parse(b []byte) (Plan, error) {
	var p Plan
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Plan{}, fmt.Errorf("unit_plan: %v", err)
	}
	p.applyDefaults()
	if err := p.Validate(); err != nil {
		return Plan{}, err
	}
	return p, nil
}

func (p *Plan) applyDefaults() {
	p.WorkstreamID = strings.TrimSpace(p.WorkstreamID)
	p.Goal = strings.TrimSpace(p.Goal)
	if p.Workers == 0 {
		p.Workers = MinWorkers
	}
	p.WorkerRuntime = strings.ToLower(strings.TrimSpace(p.WorkerRuntime))
	if p.WorkerRuntime == "" {
		p.WorkerRuntime = RuntimeSol
	}
	if strings.TrimSpace(p.EscalateTo) == "" {
		p.EscalateTo = DefaultEscalee
	}
	p.EscalateTo = strings.TrimSpace(p.EscalateTo)
	p.MergePolicy = strings.ToLower(strings.TrimSpace(p.MergePolicy))
	if p.MergePolicy == "" {
		p.MergePolicy = MergeOnGreen
	}
	p.RiskTier = strings.ToLower(strings.TrimSpace(p.RiskTier))
	if p.RiskTier == "" {
		p.RiskTier = RiskNormal
	}
}

// Validate checks a plan that already has defaults applied.
func (p Plan) Validate() error {
	if p.WorkstreamID == "" {
		return fmt.Errorf("unit_plan.workstream_id: required (the Tether workstream id the unit runs under)")
	}
	if p.Goal == "" {
		return fmt.Errorf("unit_plan.goal: required")
	}
	if len(p.DoneWhen) == 0 {
		return fmt.Errorf("unit_plan.done_when: at least one done-when criterion is required")
	}
	if p.Workers < MinWorkers || p.Workers > MaxWorkers {
		return fmt.Errorf("unit_plan.workers: %d out of range; a team is 1 orchestrator + %d-%d workers + 1 reviewer (3-5 agents)", p.Workers, MinWorkers, MaxWorkers)
	}
	switch p.WorkerRuntime {
	case RuntimeSol, RuntimeAgy:
	default:
		return fmt.Errorf("unit_plan.worker_runtime: %q not one of sol, agy", p.WorkerRuntime)
	}
	switch p.MergePolicy {
	case MergeOnGreen, MergePROnly, MergeNone:
	default:
		return fmt.Errorf("unit_plan.merge_policy: %q not one of %s, %s, %s", p.MergePolicy, MergeOnGreen, MergePROnly, MergeNone)
	}
	switch p.RiskTier {
	case RiskLow, RiskNormal, RiskHigh:
	default:
		return fmt.Errorf("unit_plan.risk_tier: %q not one of low, normal, high", p.RiskTier)
	}
	if !strings.HasPrefix(p.EscalateTo, "@") || len(p.EscalateTo) < 2 {
		return fmt.Errorf("unit_plan.escalate_to: %q must be an @handle", p.EscalateTo)
	}
	if p.Timeout != "" {
		d, err := time.ParseDuration(p.Timeout)
		if err != nil || d < time.Minute {
			return fmt.Errorf("unit_plan.timeout: %q must be a duration of at least 1m (e.g. 2h)", p.Timeout)
		}
		if d > MaxTimeout {
			return fmt.Errorf("unit_plan.timeout: %s exceeds the 12h Tether ceiling", p.Timeout)
		}
	}
	return nil
}

// TimeoutDuration returns the plan's timeout, or def when unset.
func (p Plan) TimeoutDuration(def time.Duration) time.Duration {
	if d, err := time.ParseDuration(p.Timeout); err == nil && d > 0 {
		return d
	}
	return def
}

// Brief renders the orchestrator's brief for a task.
func (p Plan) Brief(taskID, title, runID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "UNIT OF WORK %s: %s\n", taskID, title)
	fmt.Fprintf(&b, "Torque task: %s\nWorkstream: %s\n\n", taskID, p.WorkstreamID)
	fmt.Fprintf(&b, "GOAL\n%s\n\nDONE WHEN\n", p.Goal)
	writeList(&b, p.DoneWhen)
	if len(p.Scope.In) > 0 {
		b.WriteString("\nSCOPE (in)\n")
		writeList(&b, p.Scope.In)
	}
	if len(p.Scope.Out) > 0 {
		b.WriteString("\nSCOPE (out, do not touch)\n")
		writeList(&b, p.Scope.Out)
	}
	if len(p.Slices) > 0 {
		b.WriteString("\nSLICES (suggested order)\n")
		writeList(&b, p.Slices)
	}
	if len(p.Evidence) > 0 {
		b.WriteString("\nEVIDENCE REQUIRED IN THE RESULT\n")
		writeList(&b, p.Evidence)
	}
	fmt.Fprintf(&b, "\nMERGE POLICY: %s\nRELEASE: %t  DEPLOY: %t  RISK: %s\n", p.MergePolicy, bool(p.Release), bool(p.Deploy), p.RiskTier)
	fmt.Fprintf(&b, "ESCALATION: only if blocked, to %s.\n", p.EscalateTo)
	fmt.Fprintf(&b, `
HOW TO RUN
- You are the orchestrator. Workers and the reviewer talk only to you; you own status and upward communication.
- Do not message anyone else unless blocked. No acknowledgement or status chatter.
- Record progress and the final result on Torque task %s (torque_comment_add).
- When done, FIRST add a Torque comment on %s whose text starts exactly with "RESULT %s:" (your team run id is part of the marker; Torque ignores any other RESULT comment) and states outcome, PR links and evidence, THEN call tether_team_report_result. Reporting ends the run and stops every member.
- The reviewer merges (on green CI) before you report; merging is part of the protocol, not something Torque checks.
`, taskID, taskID, runID)
	return b.String()
}

func writeList(b *strings.Builder, l List) {
	for _, v := range l {
		fmt.Fprintf(b, "- %s\n", v)
	}
}

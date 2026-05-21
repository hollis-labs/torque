// Package aar defines the After-Action Report shape and rendering for Torque
// agent runs. Every Torque-orchestrated run (worker, orchestrator, planner)
// is expected to produce one AAR before signaling completion, capturing
// reflection feedback about the run that's not present in error logs or
// commits.
//
// The AAR is persisted as a typed artifact (Type="aar") attached to the
// task + run; the body is markdown with a YAML-ish frontmatter holding the
// run-identity fields the substrate auto-fills, and the agent fills in the
// reflection sections. This package owns:
//
//   - The Reflection struct (the agent's answers).
//   - The Identity struct (the substrate's run-identity fields).
//   - Render(), which composes a markdown document with frontmatter.
//   - Parse(), which round-trips a rendered AAR back into Identity +
//     Reflection (used by the CLI aggregator and tests).
//
// The schema version is bumped in this file when the shape changes; existing
// AAR artifacts retain their original schema field so older entries stay
// readable.
package aar

import (
	"bufio"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ArtifactType is the canonical type string for AAR artifacts in the
// artifacts table. CLI / aggregation tools filter on this exact value.
const ArtifactType = "aar"

// Schema is the version stamped into the AAR frontmatter. Bump when the
// reflection-section set or frontmatter keys change in a way that's not
// purely additive.
const Schema = "aar/v1"

// Outcome categorizes the run's terminal state from the AGENT's perspective.
// Distinct from the run record's status (which captures harness-side
// truth) — the agent self-reports whether the work was successful, partial,
// or otherwise.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomePartial Outcome = "partial"
	OutcomeBlocked Outcome = "blocked"
	OutcomeFailed  Outcome = "failed"
)

// ValidOutcomes is the canonical set; ValidateOutcome rejects everything else.
var ValidOutcomes = []Outcome{OutcomeSuccess, OutcomePartial, OutcomeBlocked, OutcomeFailed}

// ValidateOutcome returns nil iff o is in ValidOutcomes. Empty is treated as
// "unset" and accepted; callers default to OutcomeSuccess at write time.
func ValidateOutcome(o Outcome) error {
	if o == "" {
		return nil
	}
	if slices.Contains(ValidOutcomes, o) {
		return nil
	}
	return fmt.Errorf("invalid outcome %q (must be one of %v)", o, ValidOutcomes)
}

// Identity is the substrate-supplied run identity. The agent does NOT fill
// these — the AAR submission handler stamps them from the task record + run
// record + boot context. Empty / zero values are tolerated for fields the
// substrate can't resolve (e.g. SessionID when the run pre-dates the unified
// agent.Boot session registry).
type Identity struct {
	TaskID       string    `json:"task_id"`
	RunID        int64     `json:"run_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	AgentProfile string    `json:"agent_profile,omitempty"`
	Provider     string    `json:"provider,omitempty"`
	Mode         string    `json:"mode,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	SprintID     string    `json:"sprint_id,omitempty"`
	EpicID       string    `json:"epic_id,omitempty"`
	Title        string    `json:"title,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	EndedAt      time.Time `json:"ended_at,omitempty"`
}

// Reflection is the agent-authored body of the AAR — the five canonical
// questions plus an optional free-form summary. Empty strings are permitted
// (a vacuous AAR is still a queryable signal that the agent surfaced no
// friction), but every key MUST be present so a renderer / parser can rely
// on a stable section set.
type Reflection struct {
	// Summary is a one-paragraph prose summary of what was attempted +
	// accomplished. Optional; the canonical end-of-turn summary is the
	// torque_task_summary tool's output. This is for AAR-local context.
	Summary string `json:"summary"`

	// Outcome is the agent's self-classification. See Outcome constants.
	Outcome Outcome `json:"outcome"`

	// Clunky: "What was clunky, confusing, or surprising?"
	Clunky string `json:"clunky"`

	// Automatable: "What could be automated or converted to a deterministic
	// harness step?"
	Automatable string `json:"automatable"`

	// ManualShouldBeAuto: "What manual step should the system have done for
	// me?"
	ManualShouldBeAuto string `json:"manual_should_be_auto"`

	// SharpEdges: "Sharp edges hit, and how they were worked around?"
	SharpEdges string `json:"sharp_edges"`

	// Suggestions: "Concrete suggestions to make the next run smoother
	// (system, DX, process)."
	Suggestions string `json:"suggestions"`

	// Errors is a free-form list of error/issue blurbs encountered during
	// the run. Distinct from the reflection sections — captures concrete
	// failures, retries, or unexpected exits the agent hit. One bullet per
	// entry when rendered.
	Errors []string `json:"errors,omitempty"`
}

// reflectionSections is the canonical ordered list of section keys + their
// rendered headings. Order matters for both render() and parse(); changing it
// is a schema bump.
var reflectionSections = []struct {
	Key     string
	Heading string
}{
	{"summary", "Summary"},
	{"clunky", "What was clunky, confusing, or surprising?"},
	{"automatable", "What could be automated or converted to a harness step?"},
	{"manual_should_be_auto", "What manual step should the system have done?"},
	{"sharp_edges", "Sharp edges hit and workarounds"},
	{"suggestions", "Suggestions for the next run"},
	{"errors", "Errors / issues encountered"},
}

// Render composes the identity + reflection into a markdown document with
// YAML-ish frontmatter. The output is deterministic — given the same inputs
// it produces byte-identical output, so tests + diff tooling stay sane.
//
// Layout:
//
//	---
//	schema: aar/v1
//	task_id: ...
//	run_id: ...
//	(other identity fields, omitted when empty)
//	---
//
//	# After-Action Report
//
//	## Summary
//	<body>
//
//	## What was clunky, ...
//	<body>
//
//	... etc, one section per reflectionSections entry.
//
// Empty bodies render as "_(none)_" so the section heading is preserved (a
// reader can see at a glance which questions the agent didn't have anything
// to say about).
func Render(id Identity, r Reflection) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("schema: " + Schema + "\n")
	if r.Outcome != "" {
		b.WriteString("outcome: " + string(r.Outcome) + "\n")
	}
	writeKV := func(k, v string) {
		if v == "" {
			return
		}
		b.WriteString(k + ": " + yamlString(v) + "\n")
	}
	writeKV("task_id", id.TaskID)
	if id.RunID != 0 {
		fmt.Fprintf(&b, "run_id: %d\n", id.RunID)
	}
	writeKV("session_id", id.SessionID)
	writeKV("agent_profile", id.AgentProfile)
	writeKV("provider", id.Provider)
	writeKV("mode", id.Mode)
	writeKV("project_id", id.ProjectID)
	writeKV("sprint_id", id.SprintID)
	writeKV("epic_id", id.EpicID)
	writeKV("title", id.Title)
	if !id.StartedAt.IsZero() {
		b.WriteString("started_at: " + id.StartedAt.UTC().Format(time.RFC3339) + "\n")
	}
	if !id.EndedAt.IsZero() {
		b.WriteString("ended_at: " + id.EndedAt.UTC().Format(time.RFC3339) + "\n")
	}
	b.WriteString("---\n\n")

	b.WriteString("# After-Action Report\n\n")

	for _, sec := range reflectionSections {
		b.WriteString("## " + sec.Heading + "\n\n")
		body := sectionBody(r, sec.Key)
		if body == "" {
			b.WriteString("_(none)_\n\n")
			continue
		}
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// sectionBody returns the rendered body for a given reflection section. The
// `errors` section renders as a bulleted list; everything else is the raw
// string. Trailing whitespace is left to the renderer.
func sectionBody(r Reflection, key string) string {
	switch key {
	case "summary":
		return r.Summary
	case "clunky":
		return r.Clunky
	case "automatable":
		return r.Automatable
	case "manual_should_be_auto":
		return r.ManualShouldBeAuto
	case "sharp_edges":
		return r.SharpEdges
	case "suggestions":
		return r.Suggestions
	case "errors":
		if len(r.Errors) == 0 {
			return ""
		}
		var b strings.Builder
		for _, e := range r.Errors {
			if strings.TrimSpace(e) == "" {
				continue
			}
			b.WriteString("- " + e + "\n")
		}
		return b.String()
	}
	return ""
}

// yamlString returns a YAML-safe rendering of v. Strings without special
// characters round-trip bare; anything else gets double-quoted with embedded
// quotes escaped. This is intentionally minimal — the frontmatter is consumed
// by Parse() in this package, not a full YAML parser, so we only need to
// stay safe against newlines + colons.
func yamlString(v string) string {
	if v == "" {
		return ""
	}
	if strings.ContainsAny(v, ":#\n\"") || strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ") {
		// Escape internal quotes; wrap in double quotes.
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return v
}

// Parse extracts an Identity + Reflection from a rendered AAR markdown body.
// The intent is round-trip fidelity for documents that Render() produced; it
// is NOT a general YAML/markdown parser. Documents authored by hand may
// produce surprising results — agents should always go through Render via
// the loopback handler, never compose AAR markdown directly.
//
// Returns an error when the frontmatter delimiter `---` cannot be found or
// the document is empty. Missing reflection sections become empty strings;
// unknown sections are ignored. The frontmatter `schema` field is returned
// as the third value so callers can decide whether they understand the
// document; an empty schema means the frontmatter omitted the field.
func Parse(body string) (Identity, Reflection, string, error) {
	if body == "" {
		return Identity{}, Reflection{}, "", fmt.Errorf("aar: empty document")
	}
	if !strings.HasPrefix(body, "---\n") {
		return Identity{}, Reflection{}, "", fmt.Errorf("aar: missing leading frontmatter")
	}
	rest := body[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return Identity{}, Reflection{}, "", fmt.Errorf("aar: missing closing frontmatter delimiter")
	}
	frontmatter := rest[:end]
	mdBody := strings.TrimLeft(rest[end+len("\n---\n"):], "\n")

	id, outcome, schema, err := parseFrontmatter(frontmatter)
	if err != nil {
		return Identity{}, Reflection{}, "", err
	}

	r := parseReflectionBody(mdBody)
	r.Outcome = outcome
	return id, r, schema, nil
}

// parseFrontmatter pulls Identity fields out of the YAML-ish key:value block
// between the `---` delimiters. Unknown keys are dropped silently — the
// schema may add fields in additive bumps and older parsers should tolerate
// them.
func parseFrontmatter(fm string) (Identity, Outcome, string, error) {
	var id Identity
	var outcome Outcome
	var schema string

	scanner := bufio.NewScanner(strings.NewReader(fm))
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		val = unquoteYAML(val)
		switch key {
		case "schema":
			schema = val
		case "outcome":
			outcome = Outcome(val)
		case "task_id":
			id.TaskID = val
		case "run_id":
			var n int64
			fmt.Sscanf(val, "%d", &n)
			id.RunID = n
		case "session_id":
			id.SessionID = val
		case "agent_profile":
			id.AgentProfile = val
		case "provider":
			id.Provider = val
		case "mode":
			id.Mode = val
		case "project_id":
			id.ProjectID = val
		case "sprint_id":
			id.SprintID = val
		case "epic_id":
			id.EpicID = val
		case "title":
			id.Title = val
		case "started_at":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				id.StartedAt = t
			}
		case "ended_at":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				id.EndedAt = t
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return id, outcome, schema, fmt.Errorf("aar: scan frontmatter: %w", err)
	}
	return id, outcome, schema, nil
}

// unquoteYAML strips a single layer of double quotes off a YAML scalar
// emitted by yamlString. No-op for bare values.
func unquoteYAML(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		inner := v[1 : len(v)-1]
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		inner = strings.ReplaceAll(inner, `\\`, `\`)
		return inner
	}
	return v
}

// parseReflectionBody walks the markdown body, picks out `## <heading>`
// blocks, and routes each block's content into the matching Reflection
// field. "_(none)_" placeholders are restored to empty strings. Unknown
// headings are dropped silently.
func parseReflectionBody(body string) Reflection {
	headingToKey := make(map[string]string, len(reflectionSections))
	for _, sec := range reflectionSections {
		headingToKey[sec.Heading] = sec.Key
	}

	// Tokenize the body by `## ` headings. The first chunk (before any
	// heading) is the document-level `# After-Action Report` block; we
	// ignore it. Each subsequent chunk starts with the heading text on
	// line 1 and the body on the remaining lines.
	rest := body
	if idx := strings.Index(rest, "## "); idx >= 0 {
		rest = rest[idx:]
	} else {
		return Reflection{}
	}

	var r Reflection
	for {
		if !strings.HasPrefix(rest, "## ") {
			break
		}
		rest = rest[len("## "):]
		newline := strings.IndexByte(rest, '\n')
		if newline < 0 {
			break
		}
		heading := strings.TrimSpace(rest[:newline])
		rest = rest[newline+1:]

		next := strings.Index(rest, "\n## ")
		var section string
		if next < 0 {
			section = rest
			rest = ""
		} else {
			section = rest[:next]
			rest = rest[next+1:]
		}
		key, ok := headingToKey[heading]
		if !ok {
			continue
		}
		text := strings.TrimSpace(section)
		if text == "_(none)_" {
			text = ""
		}
		assignReflectionField(&r, key, text)
	}
	return r
}

func assignReflectionField(r *Reflection, key, text string) {
	switch key {
	case "summary":
		r.Summary = text
	case "clunky":
		r.Clunky = text
	case "automatable":
		r.Automatable = text
	case "manual_should_be_auto":
		r.ManualShouldBeAuto = text
	case "sharp_edges":
		r.SharpEdges = text
	case "suggestions":
		r.Suggestions = text
	case "errors":
		r.Errors = parseErrorList(text)
	}
}

// parseErrorList converts a bulleted list back into a []string. Lines that
// don't start with "- " are concatenated into the previous entry (for
// multi-line error bodies) — matching what render produces.
func parseErrorList(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "- ") {
			out = append(out, strings.TrimPrefix(line, "- "))
		} else if len(out) > 0 && strings.TrimSpace(line) != "" {
			out[len(out)-1] = out[len(out)-1] + "\n" + line
		}
	}
	return out
}

// ReflectionKeys returns the canonical reflection section keys in render
// order. Useful for callers that need to iterate every reflection field
// without re-hardcoding the list (e.g. CLI summary tables).
func ReflectionKeys() []string {
	keys := make([]string, 0, len(reflectionSections))
	for _, sec := range reflectionSections {
		keys = append(keys, sec.Key)
	}
	return keys
}

// SortableOutcomes returns a stable ascending order over Outcome values; used
// by CLI aggregation when grouping AARs by outcome so the output is
// reproducible regardless of map iteration order.
func SortableOutcomes(in []Outcome) []Outcome {
	out := make([]Outcome, len(in))
	copy(out, in)
	slices.Sort(out)
	return out
}

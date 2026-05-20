package steering

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
)

// ReminderRegistry tracks steering envelopes injected into a live agent's
// turn loop and whether the agent has acted on them, so the long-lived
// runtime can re-surface unaddressed messages at the next turn boundary
// (CW-20260519-0065).
//
// Motivation. Inject-at-turn delivery (steering.Bridge.Deliver) hands the
// envelope to the agent as the next turn's text — but a busy agent can
// effectively pass over it: read the text, run unrelated tools, then idle.
// The notification is then silently lost. The registry records every
// successful injection, then at the next end-of-turn the runtime asks
// PendingForTurnBoundary which envelopes still need a nudge. The nudge is a
// soft reminder, not a hard gate — the agent dismisses each via the
// torque_steering_dismiss MCP tool, and dismissed envelopes are not
// re-reminded.
//
// Keyed by task id (NOT session id) so a dismissal survives a re-dispatch:
// the same long-lived worker task may be re-launched across daemon restarts
// and we do not want the operator's "ack, ignoring" to silently reset.
//
// All methods are safe for concurrent use and safe to call on a nil
// *ReminderRegistry (an unwired bridge/runtime degrades to "no reminders",
// preserving the prior fire-and-forget delivery behavior).
type ReminderRegistry struct {
	now func() time.Time

	mu     sync.Mutex
	tasks  map[string]*taskReminderState // taskID -> per-task tracker
}

// taskReminderState holds the per-task reminder bookkeeping. injected
// records every successful steering delivery (keyed by envelope id, not
// session id, since the same task can be re-dispatched against different
// sessions). turnHadInjection flips true on each RecordDelivery and clears
// on PendingForTurnBoundary, so the reminder fires only on turn boundaries
// that ACTUALLY saw an injection — matching the spec ("if … a message was
// injected during this turn").
type taskReminderState struct {
	injected         map[string]*PendingEnvelope
	turnHadInjection bool
}

// PendingEnvelope is the per-envelope record the registry stores. Fields
// other than Envelope/From/Kind/Subject are bookkeeping the registry owns.
type PendingEnvelope struct {
	// EnvelopeID is the injected envelope's stable ID — the key the
	// dismiss tool passes back.
	EnvelopeID string

	// From is the rendered URN of env.From, surfaced verbatim in the
	// reminder text so the agent can tell who sent it.
	From string

	// Kind is the envelope's MsgKind (notice/response/…), surfaced in the
	// reminder text so the agent can decide how to respond.
	Kind string

	// Subject is a short, human-readable preview extracted from the
	// envelope payload — first ~80 chars of the rendered turn body with
	// the header line stripped. Best-effort; an empty subject still gets
	// a reminder, just without a one-liner preview.
	Subject string

	// InjectedAt timestamps when RecordDelivery saw this envelope.
	// Surfaced in the reminder text as an "age" hint so the agent can
	// prioritize fresh vs. stale messages.
	InjectedAt time.Time

	// Reminded is set true after PendingForTurnBoundary returns this
	// envelope. The registry never re-reminds a previously reminded
	// envelope — the spec's "stop nagging" rule. The agent dismisses
	// (or acts) on its own thereafter; without an explicit dismiss the
	// envelope stays in the registry but produces no further reminders.
	Reminded bool

	// Dismissed flips true via Dismiss. A dismissed envelope is never
	// returned from PendingForTurnBoundary even if Reminded is false
	// (an early dismiss skips the one-shot reminder entirely).
	Dismissed bool
}

// subjectMaxLen caps the preview slice the reminder includes per envelope.
// Long enough to be useful, short enough that a reminder for 4-5 envelopes
// still fits comfortably inside a turn.
const subjectMaxLen = 120

// NewReminderRegistry returns an empty registry using time.Now as its
// clock. Tests override the clock via WithNow.
func NewReminderRegistry() *ReminderRegistry {
	return &ReminderRegistry{
		now:   time.Now,
		tasks: make(map[string]*taskReminderState),
	}
}

// WithNow swaps the registry's clock — for deterministic tests that
// assert on InjectedAt. Production never calls this.
func (r *ReminderRegistry) WithNow(now func() time.Time) *ReminderRegistry {
	if r == nil || now == nil {
		return r
	}
	r.now = now
	return r
}

// RecordDelivery is called by the steering Bridge after a successful turn
// injection (OutcomeDelivered). taskID identifies which task's reminder
// budget this envelope counts against; the envelope is recorded as
// pending+unreminded+undismissed. A nil registry or empty taskID is a
// no-op so test paths and addresses with no resolvable task degrade
// cleanly.
//
// Duplicate envelope ids (same envelope re-delivered to the same task —
// not currently possible because the Bridge marks consumed, but defensive)
// refresh the InjectedAt and clear Reminded so the agent gets a fresh
// surface chance. Dismissed envelopes stay dismissed — the agent's prior
// "ignore this" intent wins.
func (r *ReminderRegistry) RecordDelivery(taskID string, env gomsg.Envelope) {
	if r == nil || taskID == "" || env.ID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.tasks[taskID]
	if st == nil {
		st = &taskReminderState{injected: make(map[string]*PendingEnvelope)}
		r.tasks[taskID] = st
	}
	st.turnHadInjection = true

	pe, ok := st.injected[env.ID]
	if !ok {
		pe = &PendingEnvelope{EnvelopeID: env.ID}
		st.injected[env.ID] = pe
	}
	pe.From = env.From.URN()
	pe.Kind = string(env.Kind)
	pe.Subject = previewSubject(env)
	pe.InjectedAt = r.now()
	// Preserve Dismissed: if the operator re-sends after dismissal the
	// agent already said "ignore" — do not re-surface unless they
	// re-record by dismissing first. Reminded resets so a fresh send
	// gets its one-shot nudge.
	if !pe.Dismissed {
		pe.Reminded = false
	}
}

// Dismiss marks the envelopes ack'd by the agent's torque_steering_dismiss
// tool. Returns the IDs that were actually recognized (i.e., still in the
// registry for this task) so the tool can report misses back to the agent.
// Unknown envelope ids are silently ignored — an agent that dismisses a
// stale id (e.g., from a prior process) does not deserve an error.
//
// A nil registry or empty taskID returns nil.
func (r *ReminderRegistry) Dismiss(taskID string, envelopeIDs ...string) []string {
	if r == nil || taskID == "" || len(envelopeIDs) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.tasks[taskID]
	if st == nil {
		return nil
	}
	out := make([]string, 0, len(envelopeIDs))
	for _, id := range envelopeIDs {
		pe, ok := st.injected[id]
		if !ok {
			continue
		}
		pe.Dismissed = true
		out = append(out, id)
	}
	return out
}

// PendingForTurnBoundary returns the envelopes that should be re-surfaced
// to the agent at this turn boundary AND clears the turn-injection flag
// for this task — so a turn that saw NO injection produces no reminder
// even if older undismissed envelopes are still in the registry. This is
// the spec's "if the agent has unread / unaddressed messages AND a message
// was injected during this turn" gate.
//
// Returned envelopes are sorted by InjectedAt ascending (oldest first) so
// the reminder text is deterministic and easy to scan. The caller MUST
// call MarkReminded(taskID, ids) for the returned envelopes after the
// reminder text is successfully injected — otherwise the next turn
// boundary with an injection would re-remind them (the registry does not
// auto-mark on read so a delivery failure between PendingForTurnBoundary
// and SendTurn can be retried).
//
// A nil registry or empty taskID returns (nil, false).
func (r *ReminderRegistry) PendingForTurnBoundary(taskID string) (pending []*PendingEnvelope, hadInjection bool) {
	if r == nil || taskID == "" {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.tasks[taskID]
	if st == nil {
		return nil, false
	}
	hadInjection = st.turnHadInjection
	st.turnHadInjection = false
	if !hadInjection {
		return nil, false
	}
	for _, pe := range st.injected {
		if pe.Dismissed || pe.Reminded {
			continue
		}
		pending = append(pending, copyPending(pe))
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].InjectedAt.Before(pending[j].InjectedAt)
	})
	return pending, true
}

// MarkReminded flags the listed envelopes as reminded so the next call to
// PendingForTurnBoundary will not re-include them. Call this only after
// the reminder text has been successfully delivered. A nil registry or
// empty taskID is a no-op.
func (r *ReminderRegistry) MarkReminded(taskID string, envelopeIDs ...string) {
	if r == nil || taskID == "" || len(envelopeIDs) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.tasks[taskID]
	if st == nil {
		return
	}
	for _, id := range envelopeIDs {
		if pe, ok := st.injected[id]; ok {
			pe.Reminded = true
		}
	}
}

// Forget drops all per-task state — called by the runtime when a long-
// lived session reaches terminal state, so the map does not grow without
// bound across the daemon's lifetime. A nil registry or empty taskID is
// a no-op; forgetting an unknown task is also a no-op.
func (r *ReminderRegistry) Forget(taskID string) {
	if r == nil || taskID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, taskID)
}

// Snapshot returns a deep copy of the per-task pending envelopes for
// observability/tests. The returned slice is sorted by InjectedAt. A nil
// registry or unknown task returns nil.
func (r *ReminderRegistry) Snapshot(taskID string) []*PendingEnvelope {
	if r == nil || taskID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.tasks[taskID]
	if st == nil {
		return nil
	}
	out := make([]*PendingEnvelope, 0, len(st.injected))
	for _, pe := range st.injected {
		out = append(out, copyPending(pe))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].InjectedAt.Before(out[j].InjectedAt)
	})
	return out
}

// RenderReminder formats the unaddressed envelopes as a single steering
// turn text. Header line names the count and points the agent at the
// dismiss tool; each entry shows envelope id, kind, sender, age, and a
// one-line subject preview. Caller passes the slice straight from
// PendingForTurnBoundary; an empty slice produces an empty string so the
// caller can short-circuit without an extra check.
//
// `now` is parameterized so tests get deterministic age strings; pass
// time.Now in production.
func RenderReminder(pending []*PendingEnvelope, now time.Time) string {
	if len(pending) == 0 {
		return ""
	}
	var sb strings.Builder
	if len(pending) == 1 {
		sb.WriteString("[steering reminder · 1 unaddressed message]\n\n")
	} else {
		fmt.Fprintf(&sb, "[steering reminder · %d unaddressed messages]\n\n", len(pending))
	}
	sb.WriteString("Earlier in this run an operator/peer sent these steering messages, " +
		"and the substrate didn't observe an explicit action on them. " +
		"If they need a response, handle them now. " +
		"Otherwise call torque_steering_dismiss(envelope_ids=[...]) " +
		"to acknowledge — dismissed messages will not be re-surfaced.\n\n")
	for i, pe := range pending {
		age := now.Sub(pe.InjectedAt)
		if age < 0 {
			age = 0
		}
		fmt.Fprintf(&sb, "  %d. envelope=%s  kind=%s  from=%s  age=%s\n",
			i+1, pe.EnvelopeID, defaultIfEmpty(pe.Kind, "?"),
			defaultIfEmpty(pe.From, "(unknown)"), age.Truncate(time.Second))
		if pe.Subject != "" {
			fmt.Fprintf(&sb, "     subject: %s\n", pe.Subject)
		}
	}
	return sb.String()
}

// previewSubject extracts a short, human-readable subject for the
// reminder body. Reuses RenderTurn's body extraction so the preview
// matches what the agent originally saw in the injected turn, minus the
// "[steering message …]" header. Trimmed to subjectMaxLen runes and
// collapsed to a single line so the reminder stays compact.
func previewSubject(env gomsg.Envelope) string {
	body := renderBody(env)
	body = strings.TrimSpace(body)
	if body == "(no payload)" || body == "" {
		return ""
	}
	// Collapse to a single line: replace any newline run with a single
	// space so a multi-line payload still fits on one preview line.
	body = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, body)
	body = collapseSpaces(body)
	// Ellipsis is 3 bytes in UTF-8 — account for it so the byte budget
	// holds even when the original payload is ASCII.
	const ellipsis = "…"
	if len(body) > subjectMaxLen {
		body = body[:subjectMaxLen-len(ellipsis)] + ellipsis
	}
	return body
}

// collapseSpaces folds runs of ASCII spaces into a single space.
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
			b.WriteRune(r)
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func copyPending(pe *PendingEnvelope) *PendingEnvelope {
	cp := *pe
	return &cp
}

package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
	"github.com/hollis-labs/libs/security/auth"
	"go.opentelemetry.io/otel/trace"
)

// This file is the one writer of state events (CW-20261016-0133). Every task
// status change, however it arrives (store, WriteTx, checkpoint park), reaches
// appendStateEvent inside the same transaction as the UPDATE, so the event row
// commits or rolls back with the change: a transactional outbox. Readers
// consume event_outbox by seq (see migration 042); delivery is at-least-once
// and consumers dedupe on the event id.
//
// emitTaskTransition (task_hooks.go) is NOT the event source. It stays an
// in-process, non-durable hook for reactions such as cancelling a worker.

// Trigger kinds, per the event stream contract (selfreport.md "One choke point").
const (
	TriggerHuman     = "human"
	TriggerAgent     = "agent"
	TriggerScheduler = "scheduler"
	TriggerHook      = "hook"
	TriggerReaction  = "reaction"
	TriggerSystem    = "system"
)

// Trigger says what caused a state change. Kind is one of the Trigger*
// constants; Ref names the specific cause (a tool, a route, a component, a
// rule or checkpoint id).
type Trigger struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
}

// Guard records a guard that was evaluated for the change.
type Guard struct {
	Name   string `json:"name"`
	Result string `json:"result"`
}

// TransitionOpts carries what a store method cannot infer about a transition.
// The zero value is a plain system transition without a blocked_reason.
type TransitionOpts struct {
	// Reason, when non-nil, is written to blocked_reason in the same UPDATE
	// and carried on the event.
	Reason *string
	// Forced marks an operator re-assertion (ForceTransition). It is the only
	// way a from == to change produces an event.
	Forced bool
	// Trigger overrides the trigger found in the context. Zero means "use the
	// context, else system".
	Trigger Trigger
	Guard   *Guard
	// CausationID names the event that made this one happen (reactions).
	CausationID string
}

type triggerKey struct{}

// ContextWithTrigger records the trigger for state changes made with ctx.
// HTTP and MCP handlers set it; store-level callers without a context fall
// back to TransitionOpts.Trigger, then to system.
func ContextWithTrigger(ctx context.Context, t Trigger) context.Context {
	return context.WithValue(ctx, triggerKey{}, t)
}

func triggerFromContext(ctx context.Context) (Trigger, bool) {
	t, ok := ctx.Value(triggerKey{}).(Trigger)
	return t, ok && t.Kind != ""
}

// stateScope is the part of the dimension block Torque can verify from its own
// rows. Missing is allowed; wrong is not.
type stateScope struct {
	ProjectID, EpicID, SprintID, TaskID     string
	RunID                                   sql.NullInt64
	Attempt                                 uint32
	TetherSessionID, LaunchID, WorkstreamID string
}

// stateChange is one entity transition handed to appendStateEvent.
type stateChange struct {
	EntityType string // task | run | checkpoint
	EntityID   string
	From, To   string
	Reason     string
	Forced     bool
	Trigger    Trigger
	Guard      *Guard
	Causation  string
	Scope      stateScope
}

type stateEntity struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// statePayload is state_changed@1 (catalog schema urn:hollis-labs:events:state_changed@1).
type statePayload struct {
	Entity  stateEntity `json:"entity"`
	From    string      `json:"from,omitempty"`
	To      string      `json:"to"`
	Trigger Trigger     `json:"trigger"`
	Reason  string      `json:"reason,omitempty"`
	Guard   *Guard      `json:"guard,omitempty"`
	Forced  bool        `json:"forced,omitempty"`
}

// maxReasonBytes caps the free-text reason carried on an event. blocked_reason
// can hold error strings, which can hold anything.
const maxReasonBytes = 1024

// sanitizeReason is the sanitize stage for T1. It truncates and nothing more:
// the shared pattern-based sanitize library (contract D5) has no tagged
// release yet, and an exact-value scrub with no known secrets would only
// pretend. Events therefore carry class.sanitized=false until that stage
// replaces this function. Reports whether the text was cut.
func sanitizeReason(s string) (string, bool) {
	if len(s) <= maxReasonBytes {
		return s, false
	}
	cut := maxReasonBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

func stateKind(entityType string) (string, error) {
	switch entityType {
	case "task":
		return catalog.KindTaskStateChanged, nil
	case "run":
		return catalog.KindRunStateChanged, nil
	case "checkpoint":
		return catalog.KindCheckpointStateChanged, nil
	}
	return "", fmt.Errorf("state event: unsupported entity type %q", entityType)
}

// appendStateEvent stamps and writes one state event in w's transaction. It
// writes nothing when the state did not change, unless the change is forced
// (an operator re-assertion), per the state_changed@1 contract.
func (w *WriteTx) appendStateEvent(c stateChange) error {
	if c.From == c.To && !c.Forced {
		return nil
	}
	kind, err := stateKind(c.EntityType)
	if err != nil {
		return err
	}

	reason, truncated := sanitizeReason(c.Reason)
	payload, err := json.Marshal(statePayload{
		Entity:  stateEntity{Type: c.EntityType, ID: c.EntityID},
		From:    c.From,
		To:      c.To,
		Trigger: c.Trigger,
		Reason:  reason,
		Guard:   c.Guard,
		Forced:  c.Forced,
	})
	if err != nil {
		return fmt.Errorf("state event payload: %w", err)
	}
	_, err = w.appendEvent(eventSpec{
		Kind:       kind,
		Category:   "state",
		EntityType: c.EntityType,
		EntityID:   c.EntityID,
		Payload:    payload,
		Truncated:  truncated,
		Trigger:    c.Trigger,
		Causation:  c.Causation,
		Scope:      c.Scope,
	})
	return err
}

// eventSpec is everything appendEvent needs to stamp and store one event. The
// payload is already marshalled and has already been through the caller's
// allowlist and sanitize step; appendEvent does not look inside it, so a
// structured-data payload keeps its schema shape.
type eventSpec struct {
	Kind     string
	Category string // state | progress | data ...
	// EntityType and EntityID name the subject (msg://<type>/torque/<id>).
	EntityType, EntityID string
	Payload              json.RawMessage
	// PayloadSchema overrides the catalog's schema for Kind ("id@version" or
	// the urn form the catalog uses). Empty means "look the kind up".
	PayloadSchema string
	Truncated     bool
	Trigger       Trigger
	Causation     string
	Scope         stateScope
	// IdempotencyKey, when set, is stamped on corr.idempotency_key.
	IdempotencyKey string
}

// appendEvent is the one in-transaction writer of event_outbox: it stamps the
// v1.1 envelope (scope, corr, actor, class), inserts the row on w's
// transaction and arranges the reader wake-up for commit. It returns the
// canonical event id so a caller (a receipt, a data_ref) can record it in the
// same transaction. State events go through appendStateEvent; self-report
// events (progress.checkpoint, data.emitted) call this directly.
func (w *WriteTx) appendEvent(sp eventSpec) (string, error) {
	if sp.Kind == "" || sp.EntityType == "" || sp.EntityID == "" {
		return "", fmt.Errorf("append event: kind, entity type and entity id are required")
	}
	if sp.Category == "" {
		sp.Category = "state"
	}
	if sp.Trigger.Kind == "" {
		sp.Trigger = Trigger{Kind: TriggerSystem}
	}
	digest := sha256.Sum256(sp.Payload)

	now := time.Now().UTC()
	ev := runtimeevents.Event{
		SchemaVersion: runtimeevents.SchemaVersion,
		ID:            runtimeevents.NewEventID(),
		Kind:          runtimeevents.EventKind(sp.Kind),
		Time:          now,
		App:           "torque",
		SessionID:     sp.Scope.TetherSessionID,
		Subject:       "msg://" + sp.EntityType + "/torque/" + sp.EntityID,
		Source:        runtimeevents.Source{Channel: "torque", Confidence: runtimeevents.ConfidenceExact},
		Payload:       sp.Payload,
		Truncated:     sp.Truncated,
		Scope: &runtimeevents.Scope{
			ProjectID:    sp.Scope.ProjectID,
			EpicID:       sp.Scope.EpicID,
			SprintID:     sp.Scope.SprintID,
			TaskID:       sp.Scope.TaskID,
			Attempt:      sp.Scope.Attempt,
			LaunchID:     sp.Scope.LaunchID,
			WorkstreamID: sp.Scope.WorkstreamID,
		},
		Corr:  w.eventCorr(sp),
		Actor: stateActor(w.ctx),
		Class: &runtimeevents.Class{
			Category:   sp.Category,
			Visibility: "environment",
			// The shared sanitize stage is not wired yet (sanitizeReason); the
			// envelope says so rather than claiming a scrub that did not run.
			Sanitized: false,
		},
	}
	if sp.Scope.RunID.Valid {
		ev.Scope.RunID = fmt.Sprintf("%d", sp.Scope.RunID.Int64)
	}
	switch {
	case sp.PayloadSchema != "":
		ev.Class.PayloadSchema = sp.PayloadSchema
	default:
		if e, ok := catalog.Lookup(sp.Kind); ok {
			ev.Class.PayloadSchema = e.PayloadSchema
		}
	}
	envelope, err := json.Marshal(ev)
	if err != nil {
		return "", fmt.Errorf("event envelope: %w", err)
	}

	var actorURN sql.NullString
	if ev.Actor != nil {
		actorURN = sql.NullString{String: ev.Actor.URN, Valid: true}
	}
	_, err = w.tx.Exec(
		`INSERT INTO event_outbox (event_id, kind, category, occurred_at, entity_type, entity_id,
			project_id, epic_id, sprint_id, task_id, run_id, trigger_kind, actor_urn, envelope, payload_digest)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.ID, sp.Kind, sp.Category, now.Format(time.RFC3339Nano), sp.EntityType, sp.EntityID,
		nullIfEmpty(sp.Scope.ProjectID), nullIfEmpty(sp.Scope.EpicID), nullIfEmpty(sp.Scope.SprintID),
		nullIfEmpty(sp.Scope.TaskID), sp.Scope.RunID, sp.Trigger.Kind, actorURN,
		string(envelope), hex.EncodeToString(digest[:]),
	)
	if err != nil {
		return "", fmt.Errorf("append event: %w", err)
	}
	w.wakeOutbox = true
	return ev.ID, nil
}

func nullIfEmpty(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// eventCorr builds the correlation block: the trace of the calling span, the
// task as the correlation group, and the causing event when there is one.
// Known limit: ops submitted through the queued state writer run in the
// queue's batch transaction, so their events carry the queue's trace (if any),
// not the submitter's.
func (w *WriteTx) eventCorr(sp eventSpec) *runtimeevents.Corr {
	corr := &runtimeevents.Corr{CausationID: sp.Causation, IdempotencyKey: sp.IdempotencyKey}
	// The correlation group is the run when there is one (the task is already
	// in scope.task_id), else the task.
	switch {
	case sp.Scope.RunID.Valid:
		corr.CorrelationID = fmt.Sprintf("msg://run/torque/%d", sp.Scope.RunID.Int64)
	case sp.Scope.TaskID != "":
		corr.CorrelationID = sp.Scope.TaskID
	}
	if sc := trace.SpanContextFromContext(w.ctx); sc.IsValid() {
		corr.TraceID = sc.TraceID().String()
		corr.SpanID = sc.SpanID().String()
	}
	return corr
}

// stateActor reports the caller only when a verified principal carries a mesh
// URN as its id. Anything weaker is omitted: missing is allowed, wrong is not.
func stateActor(ctx context.Context) *runtimeevents.Actor {
	p, ok := auth.FromContext(ctx)
	if !ok || !p.Verified || !strings.HasPrefix(p.ID, "msg://") {
		return nil
	}
	kind := p.Kind
	switch kind {
	case auth.KindOperator:
		kind = "user"
	case "":
		kind = "service"
	}
	return &runtimeevents.Actor{URN: p.ID, Kind: kind, Verified: "token"}
}

// TriggerWithRef is the trigger kind ctx carries (system when none) with ref
// as the specific cause. Use it where the kind comes from who called but the
// ref names the thing acted on, such as a checkpoint id.
func TriggerWithRef(ctx context.Context, ref string) Trigger {
	t, ok := triggerFromContext(ctx)
	if !ok {
		t = Trigger{Kind: TriggerSystem}
	}
	t.Ref = ref
	return t
}

// resolveTrigger picks the trigger for a change: explicit option, then the
// context, then system.
func resolveTrigger(ctx context.Context, o TransitionOpts) Trigger {
	if o.Trigger.Kind != "" {
		return o.Trigger
	}
	if t, ok := triggerFromContext(ctx); ok {
		return t
	}
	return Trigger{Kind: TriggerSystem}
}

// OutboxWake returns a channel that receives a value (never blocking the
// writer) after a transaction that appended state events commits. It is a
// wake-up only: event_outbox is the source of truth, so a lost wake-up costs at
// most the reader's poll interval.
func (s *Store) OutboxWake() <-chan struct{} { return s.outboxWake }

func (s *Store) signalOutbox() {
	select {
	case s.outboxWake <- struct{}{}:
	default:
	}
}

// ResolveTrigger reports the trigger a state change made with ctx carries when
// the caller passes none: the one ContextWithTrigger stamped, else system.
func ResolveTrigger(ctx context.Context) Trigger { return resolveTrigger(ctx, TransitionOpts{}) }

// trigger resolves the trigger for a change in w: the explicit one, else the
// one the context carries, else system.
func (w *WriteTx) trigger(explicit Trigger) Trigger {
	return resolveTrigger(w.ctx, TransitionOpts{Trigger: explicit})
}

// scopeForTask reads the dimensions Torque can verify from the task row.
func (w *WriteTx) scopeForTask(taskID string) stateScope {
	var project, epic, sprint sql.NullString
	_ = w.tx.QueryRow(`SELECT project_id, epic_id, sprint_id FROM tasks WHERE id = ?`, taskID).Scan(&project, &epic, &sprint)
	return stateScope{ProjectID: project.String, EpicID: epic.String, SprintID: sprint.String, TaskID: taskID}
}

// RecordRunStateChange appends a run.state_changed event for a run whose
// status the caller just wrote in this transaction (from is "" for a new
// run). A change to the same status writes nothing. Callers that update the
// runs row themselves use this so the event commits with the change; the
// store's own run writes (CreateRun, CompleteRun, SetRunOperatorStatus) call
// it for you. An empty trigger means the context's, else system.
func (w *WriteTx) RecordRunStateChange(runID int64, taskID, from, to, reason string, trig Trigger) error {
	scope := w.scopeForTask(taskID)
	scope.RunID = sql.NullInt64{Int64: runID, Valid: true}
	return w.appendStateEvent(stateChange{
		EntityType: "run",
		EntityID:   fmt.Sprintf("%d", runID),
		From:       from,
		To:         to,
		Reason:     reason,
		Trigger:    w.trigger(trig),
		Scope:      scope,
	})
}

// recordCheckpointState appends a checkpoint.state_changed event. The entity id
// is the checkpoint's correlation id; the ref names it too.
func (w *WriteTx) recordCheckpointState(corr, taskID, from, to, reason string, trig Trigger) error {
	if trig.Kind == "" {
		trig = TriggerWithRef(w.ctx, "checkpoint:"+corr)
	}
	return w.appendStateEvent(stateChange{
		EntityType: "checkpoint",
		EntityID:   corr,
		From:       from,
		To:         to,
		Reason:     reason,
		Trigger:    trig,
		Scope:      w.scopeForTask(taskID),
	})
}

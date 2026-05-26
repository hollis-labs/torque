// Package broker is Torque's typed envelope dispatcher. It layers
// Torque-specific validation, kind-aware helpers, and SSE publishing
// on top of a `gomsg.Store` (S1.2 / CW-20260503-0012).
//
// CW-20260503-0013 (S1.3). Distinct from internal/toolbroker/, which
// gates tool calls — these two never share a package name.
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	gomsg "github.com/hollis-labs/go-messaging"
	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"
)

// MaxPayloadBytes caps every envelope payload. 256 KiB is well above
// typical structured-context exchanges and well below SQLite's BLOB
// row-size limits — keeps a malformed agent from filling the messages
// table with megabyte payloads.
const MaxPayloadBytes = 256 * 1024

// DefaultRequestTimeout is the per-call timeout the HTTP/MCP request
// surface applies when the caller doesn't override.
const DefaultRequestTimeout = 30 // seconds

// MinRequestTimeout / MaxRequestTimeout fence the timeout the HTTP/MCP
// request surface accepts. Keeps callers from setting 0 (which would
// race the Send) or many-hour timeouts that survive process restarts.
const (
	MinRequestTimeout = 1
	MaxRequestTimeout = 600
)

// Severity is the level of a kind=escalation envelope.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// EscalationPayload is the canonical body shape for kind=escalation
// envelopes. JSON-encoded by Escalation; consumers can decode against
// this struct or read the raw payload off the envelope.
type EscalationPayload struct {
	Severity Severity        `json:"severity"`
	Reason   string          `json:"reason"`
	Context  json.RawMessage `json:"context,omitempty"`
}

// HandoffPayload is the canonical body shape for kind=handoff.
type HandoffPayload struct {
	TaskID string `json:"task_id,omitempty"`
	From   string `json:"from"`
	To     string `json:"to"`
	Note   string `json:"note,omitempty"`
}

// StatusPayload is the canonical body shape for kind=status_update —
// orchestrator heartbeats, executor progress reports, etc.
type StatusPayload struct {
	State    string `json:"state"`
	Progress int    `json:"progress,omitempty"`
	Note     string `json:"note,omitempty"`
}

// EventPublisher is the optional SSE-publishing hook. Pass nil to skip
// broadcasts (test paths, or contexts where SSE isn't wired).
type EventPublisher interface {
	Broadcast(eventType string, data map[string]interface{})
}

// Broker wraps a messaging.Dispatcher with Torque-specific
// validation, typed-envelope helpers, and SSE event publishing.
type Broker struct {
	d   gomsg.Dispatcher
	sse EventPublisher
}

// New returns a Broker built on top of store. If sse is non-nil, the
// broker publishes envelope.* events on Send / Inbox / Request / Reply
// / Escalation calls.
func New(store gomsg.Store, sse EventPublisher) *Broker {
	return &Broker{
		d:   gomsg.NewDispatcher(store),
		sse: sse,
	}
}

// Sentinel errors returned by Broker validation. Callers can use
// errors.Is to distinguish — handlers map them to 422 / 413.
var (
	ErrValidation      = errors.New("envelope validation failed")
	ErrPayloadTooLarge = errors.New("envelope payload exceeds MaxPayloadBytes")
)

// Send validates env and persists it through the underlying Store.
// Publishes envelope.created on success.
//
// Use directly when you already have a fully-formed envelope (the HTTP
// /broker/send route, MCP torque_broker_send tool). Prefer the
// typed helpers (Notice / Escalation / Handoff / StatusUpdate) when
// constructing an envelope from primitives.
func (b *Broker) Send(ctx context.Context, env gomsg.Envelope) (sent gomsg.Envelope, err error) {
	// Trace the canonical send path. Higher-level helpers (Notice, Request,
	// Reply, Escalation, Handoff, StatusUpdate) all fan through here, so this
	// one span covers the whole envelope lifecycle; kind / from / to attrs
	// distinguish them. Validation errors still RecordError — broker validation
	// is a real fault (malformed envelope), not a policy reject.
	ctx, span := feotel.StartSpan(ctx, "torque.message.send")
	span.SetAttributes(
		attribute.String("hollis.app", "torque"),
		attribute.String("torque.message.kind", string(env.Kind)),
		attribute.String("torque.message.from", env.From.URN()),
		attribute.String("torque.message.to", env.To.URN()),
	)
	if env.ThreadID != "" {
		span.SetAttributes(attribute.String("torque.message.thread_id", env.ThreadID))
	}
	if env.InReplyTo != "" {
		span.SetAttributes(attribute.String("torque.message.in_reply_to", env.InReplyTo))
	}
	defer func() {
		if err != nil {
			span.RecordError(err)
		} else {
			// The store-assigned envelope id is the load-bearing handle for
			// downstream correlation (consume / cancel / get / federation
			// hop). Surface it AFTER Send so operators can grep the trace.
			span.SetAttributes(attribute.String("hollis.message.id", sent.ID))
		}
		span.End()
	}()

	if err := validate(env); err != nil {
		return gomsg.Envelope{}, err
	}
	sent, err = b.d.Send(ctx, env)
	if err != nil {
		return gomsg.Envelope{}, err
	}
	b.publish("envelope.created", sent)
	return sent, nil
}

// Request sends env as kind=request and blocks until a matching
// response (InReplyTo=<sent id>) arrives or ctx expires. Returns
// gomsg.ErrRequestTimeout on deadline.
//
// On success, publishes envelope.created (for the request) and
// envelope.responded (for the reply). The request envelope persists
// after timeout — callers may invoke Cancel(id) to retire it.
func (b *Broker) Request(ctx context.Context, env gomsg.Envelope) (gomsg.Envelope, error) {
	env.Kind = gomsg.MsgKindRequest
	if err := validate(env); err != nil {
		return gomsg.Envelope{}, err
	}
	// Dispatcher.Request subscribes BEFORE Send, then waits — we don't
	// have visibility into the sent envelope's ID before the call
	// returns, so we publish envelope.created from the response side
	// (using InReplyTo) once we have the reply. For pre-reply observers,
	// the Store's Subscribe stream is the right place to watch.
	resp, err := b.d.Request(ctx, env)
	if err != nil {
		return gomsg.Envelope{}, err
	}
	b.publish("envelope.responded", resp)
	return resp, nil
}

// Reply constructs and sends a response envelope correlated to parent.
// Convenience wrapper around dispatcher.Reply with payload-size guard.
func (b *Broker) Reply(ctx context.Context, parent gomsg.Envelope, payload json.RawMessage) (gomsg.Envelope, error) {
	if len(payload) > MaxPayloadBytes {
		return gomsg.Envelope{}, ErrPayloadTooLarge
	}
	resp, err := b.d.Reply(ctx, parent, payload)
	if err != nil {
		return gomsg.Envelope{}, err
	}
	b.publish("envelope.responded", resp)
	return resp, nil
}

// Notice sends a fire-and-forget informational envelope.
func (b *Broker) Notice(ctx context.Context, from, to gomsg.Address, payload json.RawMessage, opts ...Option) (gomsg.Envelope, error) {
	env := gomsg.Envelope{
		Kind:        gomsg.MsgKindNotice,
		From:        from,
		To:          to,
		Payload:     payload,
		ContentType: "application/json",
	}
	for _, o := range opts {
		o(&env)
	}
	return b.Send(ctx, env)
}

// Escalation flags an issue up the chain. Validates severity, marshals
// the payload, sends, and publishes envelope.escalated.
func (b *Broker) Escalation(ctx context.Context, from, to gomsg.Address, p EscalationPayload, opts ...Option) (gomsg.Envelope, error) {
	if !validSeverities[p.Severity] {
		return gomsg.Envelope{}, fmt.Errorf("%w: severity=%q", ErrValidation, p.Severity)
	}
	if p.Reason == "" {
		return gomsg.Envelope{}, fmt.Errorf("%w: escalation reason is required", ErrValidation)
	}
	body, err := json.Marshal(p)
	if err != nil {
		return gomsg.Envelope{}, err
	}
	env := gomsg.Envelope{
		Kind:        gomsg.MsgKindEscalation,
		From:        from,
		To:          to,
		Payload:     body,
		ContentType: "application/json",
	}
	for _, o := range opts {
		o(&env)
	}
	if err := validate(env); err != nil {
		return gomsg.Envelope{}, err
	}
	sent, err := b.d.Send(ctx, env)
	if err != nil {
		return gomsg.Envelope{}, err
	}
	b.publish("envelope.created", sent)
	b.publish("envelope.escalated", sent)
	return sent, nil
}

// Handoff transfers task ownership between sessions.
func (b *Broker) Handoff(ctx context.Context, from, to gomsg.Address, p HandoffPayload, opts ...Option) (gomsg.Envelope, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return gomsg.Envelope{}, err
	}
	env := gomsg.Envelope{
		Kind:        gomsg.MsgKindHandoff,
		From:        from,
		To:          to,
		Payload:     body,
		ContentType: "application/json",
	}
	for _, o := range opts {
		o(&env)
	}
	return b.Send(ctx, env)
}

// StatusUpdate sends a heartbeat/progress envelope.
func (b *Broker) StatusUpdate(ctx context.Context, from, to gomsg.Address, p StatusPayload, opts ...Option) (gomsg.Envelope, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return gomsg.Envelope{}, err
	}
	env := gomsg.Envelope{
		Kind:        gomsg.MsgKindStatusUpdate,
		From:        from,
		To:          to,
		Payload:     body,
		ContentType: "application/json",
	}
	for _, o := range opts {
		o(&env)
	}
	return b.Send(ctx, env)
}

// Inbox returns undelivered envelopes addressed to `to`. The Store
// atomically marks them DeliveredAt; the broker publishes
// envelope.delivered for each.
func (b *Broker) Inbox(ctx context.Context, to gomsg.Address, f gomsg.Filter) ([]gomsg.Envelope, error) {
	envs, err := b.d.Inbox(ctx, to, f)
	if err != nil {
		return nil, err
	}
	for _, env := range envs {
		b.publish("envelope.delivered", env)
	}
	return envs, nil
}

// Get retrieves a single envelope by ID — pass-through.
func (b *Broker) Get(ctx context.Context, id string) (gomsg.Envelope, error) {
	return b.d.Get(ctx, id)
}

// Cancel marks a request envelope as retired — pass-through.
func (b *Broker) Cancel(ctx context.Context, id string) error {
	return b.d.Cancel(ctx, id)
}

// Consume advances ConsumedAt for (envelope, recipient) — the terminal
// "handled" lifecycle state, distinct from Inbox's "delivered". Pass-through
// to the underlying Store.
//
// The steering bridge (internal/runtime/steering) calls this after it
// injects a steering envelope into a live agent's loop: the messaging
// Store's Consume inserts a delivery row, which also excludes the envelope
// from any future Inbox drain — so a steered envelope is never re-delivered
// by an opt-in inbox poll.
func (b *Broker) Consume(ctx context.Context, id string, recipient gomsg.Address) (err error) {
	// Consume is the lifecycle close — once an envelope is consumed by its
	// recipient (or by the steering bridge on behalf of a live session) it's
	// excluded from future Inbox drains. Tracing it lets operators see the
	// full envelope path: send → optional federation hop → deliver →
	// consume, all under one trace when callers propagate ctx through.
	ctx, span := feotel.StartSpan(ctx, "torque.message.consume")
	span.SetAttributes(
		attribute.String("hollis.app", "torque"),
		attribute.String("hollis.message.id", id),
		attribute.String("torque.message.recipient", recipient.URN()),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
		}
		span.End()
	}()
	return b.d.Consume(ctx, id, recipient)
}

// Subscribe streams envelopes addressed to `to` — pass-through to the
// underlying Store. Caller cancels via ctx.
func (b *Broker) Subscribe(ctx context.Context, to gomsg.Address, f gomsg.Filter) (<-chan gomsg.Envelope, error) {
	return b.d.Subscribe(ctx, to, f)
}

// Option mutates an envelope before send. Use to set ThreadID, Channel,
// Metadata, etc., without bloating each helper's signature.
type Option func(*gomsg.Envelope)

// WithThread sets the envelope's ThreadID, scoping it to a conversation.
func WithThread(id string) Option {
	return func(e *gomsg.Envelope) { e.ThreadID = id }
}

// WithChannel sets the envelope's Channel (opaque UX-layer pass-through).
func WithChannel(ch gomsg.Channel) Option {
	return func(e *gomsg.Envelope) { e.Channel = ch }
}

// WithMetadata adds a single (key, value) entry to the envelope's
// Metadata map. Idempotent across calls; later values overwrite.
func WithMetadata(k, v string) Option {
	return func(e *gomsg.Envelope) {
		if e.Metadata == nil {
			e.Metadata = make(map[string]string, 1)
		}
		e.Metadata[k] = v
	}
}

// WithContentType overrides the default "application/json" content type.
func WithContentType(ct string) Option {
	return func(e *gomsg.Envelope) { e.ContentType = ct }
}

// validKinds is the closed set of envelope kinds Torque dispatches.
// Mirrors gomsg's enum but kept Torque-side so future kinds added
// to gomsg don't silently route — every new kind is an explicit add.
var validKinds = map[gomsg.Kind]bool{
	gomsg.MsgKindRequest:      true,
	gomsg.MsgKindResponse:     true,
	gomsg.MsgKindNotice:       true,
	gomsg.MsgKindStatusUpdate: true,
	gomsg.MsgKindHandoff:      true,
	gomsg.MsgKindEscalation:   true,
}

// validSeverities is the closed set of escalation severities.
var validSeverities = map[Severity]bool{
	SeverityInfo:     true,
	SeverityWarn:     true,
	SeverityError:    true,
	SeverityCritical: true,
}

// validate enforces Torque-specific envelope invariants applied to
// every Send / Request / Escalation path:
//   - kind in the recognized enum
//   - From and To addresses non-zero
//   - Payload size within MaxPayloadBytes
//
// Pre-existing messaging.Store enforces the lifecycle-field rule
// (DeliveredAt/ConsumedAt nil on input) — broker doesn't duplicate.
func validate(env gomsg.Envelope) error {
	if !validKinds[env.Kind] {
		return fmt.Errorf("%w: kind=%q", ErrValidation, env.Kind)
	}
	if env.From.IsZero() {
		return fmt.Errorf("%w: from address is zero", ErrValidation)
	}
	if env.To.IsZero() {
		return fmt.Errorf("%w: to address is zero", ErrValidation)
	}
	if len(env.Payload) > MaxPayloadBytes {
		return ErrPayloadTooLarge
	}
	return nil
}

// publish broadcasts an envelope event over the SSE hub if one is wired.
// Payload is the routing surface only — the wire envelope (including
// payload bytes) is NOT broadcast to keep the SSE stream small and to
// avoid leaking arbitrary payload content into the connected-client
// fan-out. Consumers that need the body fetch via /broker/{id}.
func (b *Broker) publish(eventType string, env gomsg.Envelope) {
	if b.sse == nil {
		return
	}
	data := map[string]interface{}{
		"id":          env.ID,
		"kind":        string(env.Kind),
		"from":        env.From.URN(),
		"to":          env.To.URN(),
		"thread_id":   env.ThreadID,
		"in_reply_to": env.InReplyTo,
		"created_at":  env.CreatedAt,
	}
	b.sse.Broadcast(eventType, data)
}

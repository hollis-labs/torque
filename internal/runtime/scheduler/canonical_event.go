package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// canonicalSchemaVersion is the wire-format version for CanonicalEvent.
// Bump only on breaking shape changes; additive fields do not require a bump.
const canonicalSchemaVersion = 1

// CanonicalSource identifies the host emitting an event. Used as the first
// URN segment and the `source` field. ASCII-lowercase; the only value Torque
// emits is "torque" — other hosts (e.g. "nanite") populate the field when
// they emit envelopes that flow through the same federation surface.
const CanonicalSource = "torque"

// CanonicalEvent is the cross-host wire-format envelope wrapping a
// SchedulerEvent for export onto the mux envelope broker. The Layer-1
// in-process EventBus does NOT use this type — it continues to use the
// existing SchedulerEvent shape (Type, TaskID, RunID, Data, Timestamp).
// This type is built only at the export boundary; today the boundary is
// not yet wired, so this exists as a type contract that consumers can code
// against and tests can pin.
//
// Field discipline:
//   - SchemaVersion lets consumers tolerate additive fields without breaking;
//     a major-version bump signals an incompatible reshape.
//   - EventID is per-emission, not per-Type; consumers de-dupe on it.
//   - URN is opaque to consumers — string-equality / prefix-match only.
//   - Payload mirrors SchedulerEvent.Data when that field was a map; for
//     non-map Data values the translator wraps it under {"value": ...}.
//
// See docs/adr/0003-harness-emitted-coordination-events.md for the design.
type CanonicalEvent struct {
	SchemaVersion int                    `json:"schema_version"`
	EventID       string                 `json:"id"`
	Kind          string                 `json:"kind"`
	Source        string                 `json:"source"`
	URN           string                 `json:"urn"`
	TaskID        string                 `json:"task_id,omitempty"`
	RunID         int64                  `json:"run_id,omitempty"`
	OccurredAt    time.Time              `json:"occurred_at"`
	Actor         CanonicalActor         `json:"actor"`
	Payload       map[string]interface{} `json:"payload,omitempty"`
}

// CanonicalActor identifies who caused the event. Kind is one of
// "scheduler" | "worker" | "operator" | "agent" | "system". ID is a
// free-form identifier scoped to Kind (e.g. worker_id, agent name).
type CanonicalActor struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// ToCanonical wraps a SchedulerEvent in a CanonicalEvent suitable for
// cross-host export. The translator chooses an URN based on the event's
// TaskID + RunID + Kind; events without a meaningful entity (e.g.
// `scheduler.tick`) get a daemon-scoped URN.
//
// Actor defaults to {kind:"scheduler", id:""} — callers that emit on
// behalf of a specific actor should populate Data["actor_kind"] and
// Data["actor_id"], which the translator promotes into the Actor field
// and strips from the Payload to avoid duplication.
func ToCanonical(ev SchedulerEvent) CanonicalEvent {
	ce := CanonicalEvent{
		SchemaVersion: canonicalSchemaVersion,
		EventID:       newEventID(),
		Kind:          ev.Type,
		Source:        CanonicalSource,
		TaskID:        ev.TaskID,
		RunID:         ev.RunID,
		OccurredAt:    ev.Timestamp,
		Actor:         CanonicalActor{Kind: "scheduler"},
	}
	if ce.OccurredAt.IsZero() {
		ce.OccurredAt = time.Now()
	}
	ce.URN = urnFor(ev)
	ce.Payload, ce.Actor = projectPayloadAndActor(ev.Data, ce.Actor)
	return ce
}

// URNForTask returns the canonical URN for a Torque task. Exported so
// callers outside the scheduler package can construct URNs without
// re-implementing the format.
func URNForTask(taskID string) string {
	if taskID == "" {
		return ""
	}
	return "urn:" + CanonicalSource + ":task:" + taskID
}

// URNForRun returns the canonical URN for a Torque run. RunID is
// rendered as a base-10 integer (no padding).
func URNForRun(runID int64) string {
	if runID <= 0 {
		return ""
	}
	return "urn:" + CanonicalSource + ":run:" + strconv.FormatInt(runID, 10)
}

// URNForDaemon returns the canonical URN for the serve process. PID is
// 0 when unknown — callers should pass os.Getpid() at boot.
func URNForDaemon(pid int) string {
	if pid <= 0 {
		return "urn:" + CanonicalSource + ":daemon:unknown"
	}
	return "urn:" + CanonicalSource + ":daemon:" + strconv.Itoa(pid)
}

// urnFor chooses the most specific URN for the event. Run-scoped events
// prefer the task URN over the run URN when both are present — task IDs
// are stable identifiers a human can read, while run IDs are an internal
// integer detail.
func urnFor(ev SchedulerEvent) string {
	if ev.TaskID != "" {
		return URNForTask(ev.TaskID)
	}
	if ev.RunID > 0 {
		return URNForRun(ev.RunID)
	}
	// Daemon-scoped events (scheduler.tick, daemon.up) have no entity ID
	// in the SchedulerEvent itself. Use a generic daemon URN; the PID
	// version is supplied at emission time via Data["pid"] when relevant.
	return "urn:" + CanonicalSource + ":daemon:default"
}

// projectPayloadAndActor extracts the Payload map and promotes any
// `actor_kind` / `actor_id` keys into the Actor field. Returns the
// projected payload (which may be nil) and the resolved actor.
//
// SchedulerEvent.Data is `interface{}`; in practice every existing
// emission site passes either `nil` or `map[string]interface{}`. For
// non-map values we wrap under {"value": ...} so consumers always see
// a map shape.
func projectPayloadAndActor(data interface{}, defaultActor CanonicalActor) (map[string]interface{}, CanonicalActor) {
	if data == nil {
		return nil, defaultActor
	}
	m, ok := data.(map[string]interface{})
	if !ok {
		return map[string]interface{}{"value": data}, defaultActor
	}
	out := make(map[string]interface{}, len(m))
	actor := defaultActor
	for k, v := range m {
		switch k {
		case "actor_kind":
			if s, ok := v.(string); ok && s != "" {
				actor.Kind = s
			}
		case "actor_id":
			if s, ok := v.(string); ok {
				actor.ID = s
			}
		default:
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil, actor
	}
	return out, actor
}

// newEventID returns a short random hex string suitable for de-duplication.
// 16 random bytes (128 bits) → 32-char hex; we use 12 bytes for compactness
// (24-char hex) — collision-resistant within the lifetime of any single
// daemon and across federation hops.
func newEventID() string {
	var b [12]byte
	_, err := rand.Read(b[:])
	if err != nil {
		// rand.Read never errors on a healthy OS RNG; fall back to a
		// time-based id so the field is never empty.
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return "evt_" + hex.EncodeToString(b[:])
}

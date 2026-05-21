package scheduler

import (
	"strings"
	"testing"
	"time"
)

func TestToCanonicalPopulatesURNFromTaskID(t *testing.T) {
	ev := SchedulerEvent{
		Type:      "task.transitioned",
		TaskID:    "CW-20260519-0126",
		RunID:     42,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"from": "todo", "to": "doing"},
	}

	ce := ToCanonical(ev)

	if ce.SchemaVersion != canonicalSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", ce.SchemaVersion, canonicalSchemaVersion)
	}
	if ce.Kind != "task.transitioned" {
		t.Errorf("Kind = %q, want %q", ce.Kind, "task.transitioned")
	}
	if ce.Source != "torque" {
		t.Errorf("Source = %q, want %q", ce.Source, "torque")
	}
	if ce.URN != "urn:torque:task:CW-20260519-0126" {
		t.Errorf("URN = %q, want urn:torque:task:CW-20260519-0126", ce.URN)
	}
	if ce.TaskID != "CW-20260519-0126" {
		t.Errorf("TaskID = %q", ce.TaskID)
	}
	if ce.RunID != 42 {
		t.Errorf("RunID = %d", ce.RunID)
	}
	if got := ce.Payload["from"]; got != "todo" {
		t.Errorf("Payload[from] = %v, want todo", got)
	}
	if got := ce.Payload["to"]; got != "doing" {
		t.Errorf("Payload[to] = %v, want doing", got)
	}
}

func TestToCanonicalFallsBackToRunURNWhenNoTaskID(t *testing.T) {
	ev := SchedulerEvent{Type: "run.progress", RunID: 99, Timestamp: time.Now()}
	ce := ToCanonical(ev)
	if ce.URN != "urn:torque:run:99" {
		t.Errorf("URN = %q, want urn:torque:run:99", ce.URN)
	}
}

func TestToCanonicalUsesDaemonURNWhenNoEntity(t *testing.T) {
	ev := SchedulerEvent{Type: "scheduler.tick", Timestamp: time.Now()}
	ce := ToCanonical(ev)
	if !strings.HasPrefix(ce.URN, "urn:torque:daemon:") {
		t.Errorf("URN = %q, want urn:torque:daemon: prefix", ce.URN)
	}
}

func TestToCanonicalPromotesActorFromPayload(t *testing.T) {
	ev := SchedulerEvent{
		Type:   "scheduler.toggled",
		TaskID: "",
		Data: map[string]interface{}{
			"enabled":    true,
			"actor_kind": "operator",
			"actor_id":   "chrispian",
		},
		Timestamp: time.Now(),
	}

	ce := ToCanonical(ev)

	if ce.Actor.Kind != "operator" {
		t.Errorf("Actor.Kind = %q, want operator", ce.Actor.Kind)
	}
	if ce.Actor.ID != "chrispian" {
		t.Errorf("Actor.ID = %q, want chrispian", ce.Actor.ID)
	}
	if _, ok := ce.Payload["actor_kind"]; ok {
		t.Errorf("Payload still contains actor_kind; should have been stripped")
	}
	if _, ok := ce.Payload["actor_id"]; ok {
		t.Errorf("Payload still contains actor_id; should have been stripped")
	}
	if got := ce.Payload["enabled"]; got != true {
		t.Errorf("Payload[enabled] = %v, want true", got)
	}
}

func TestToCanonicalDefaultsToSchedulerActor(t *testing.T) {
	ev := SchedulerEvent{Type: "run.started", TaskID: "T-1", Timestamp: time.Now()}
	ce := ToCanonical(ev)
	if ce.Actor.Kind != "scheduler" {
		t.Errorf("Actor.Kind = %q, want scheduler", ce.Actor.Kind)
	}
}

func TestToCanonicalWrapsNonMapData(t *testing.T) {
	ev := SchedulerEvent{Type: "x.scalar", Data: "raw-string", Timestamp: time.Now()}
	ce := ToCanonical(ev)
	if got := ce.Payload["value"]; got != "raw-string" {
		t.Errorf("Payload[value] = %v, want raw-string", got)
	}
}

func TestToCanonicalNilDataYieldsNilPayload(t *testing.T) {
	ev := SchedulerEvent{Type: "x.empty", Timestamp: time.Now()}
	ce := ToCanonical(ev)
	if ce.Payload != nil {
		t.Errorf("Payload = %v, want nil", ce.Payload)
	}
}

func TestToCanonicalGeneratesUniqueEventIDs(t *testing.T) {
	ev := SchedulerEvent{Type: "x", Timestamp: time.Now()}
	a := ToCanonical(ev)
	b := ToCanonical(ev)
	if a.EventID == b.EventID {
		t.Errorf("EventIDs collide: %s", a.EventID)
	}
	if !strings.HasPrefix(a.EventID, "evt_") {
		t.Errorf("EventID = %q, want evt_ prefix", a.EventID)
	}
}

func TestURNHelpers(t *testing.T) {
	if got := URNForTask("T-1"); got != "urn:torque:task:T-1" {
		t.Errorf("URNForTask = %q", got)
	}
	if got := URNForTask(""); got != "" {
		t.Errorf("URNForTask empty = %q, want empty", got)
	}
	if got := URNForRun(7); got != "urn:torque:run:7" {
		t.Errorf("URNForRun = %q", got)
	}
	if got := URNForRun(0); got != "" {
		t.Errorf("URNForRun 0 = %q, want empty", got)
	}
	if got := URNForDaemon(1234); got != "urn:torque:daemon:1234" {
		t.Errorf("URNForDaemon = %q", got)
	}
	if got := URNForDaemon(0); got != "urn:torque:daemon:unknown" {
		t.Errorf("URNForDaemon 0 = %q", got)
	}
}

func TestToCanonicalOccurredAtFallsBackToNow(t *testing.T) {
	ev := SchedulerEvent{Type: "x"}
	before := time.Now()
	ce := ToCanonical(ev)
	after := time.Now()
	if ce.OccurredAt.Before(before) || ce.OccurredAt.After(after) {
		t.Errorf("OccurredAt = %v, want between %v and %v", ce.OccurredAt, before, after)
	}
}

package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventConstants(t *testing.T) {
	assert.Equal(t, "task.created", EventTaskCreated)
	assert.Equal(t, "task.updated", EventTaskUpdated)
	assert.Equal(t, "task.transitioning", EventTaskTransitioning)
	assert.Equal(t, "task.transitioned", EventTaskTransitioned)
	assert.Equal(t, "task.assigned", EventTaskAssigned)
	assert.Equal(t, "run.started", EventRunStarted)
	assert.Equal(t, "run.completed", EventRunCompleted)
	assert.Equal(t, "run.failed", EventRunFailed)
	assert.Equal(t, "artifact.created", EventArtifactCreated)
	assert.Equal(t, "scheduler.tick", EventSchedulerTick)
	assert.Equal(t, "deliverable.check", EventDeliverableCheck)
	assert.Equal(t, "plugin.installed", EventPluginInstalled)
	assert.Equal(t, "plugin.uninstalled", EventPluginUninstalled)
	assert.Equal(t, "config.changed", EventConfigChanged)
}

func TestNewEvent(t *testing.T) {
	data := EventData{
		TaskID:    "task-1",
		RunID:     "run-42",
		OldStatus: "pending",
		NewStatus: "running",
	}
	ev := NewEvent(EventTaskTransitioned, "test", data)
	assert.Equal(t, EventTaskTransitioned, ev.Type)
	assert.Equal(t, "test", ev.Source)
	assert.False(t, ev.Timestamp.IsZero())
	assert.Equal(t, "task-1", ev.Data["task_id"])
	assert.Equal(t, "run-42", ev.Data["run_id"])
	assert.Equal(t, "pending", ev.Data["old_status"])
	assert.Equal(t, "running", ev.Data["new_status"])
	// Fields not set should not appear in map
	_, hasArtifact := ev.Data["artifact_id"]
	assert.False(t, hasArtifact)
}

func TestSubscribeEvents(t *testing.T) {
	h := NewClockworkHost(nil, NewLogger("test"))

	ch := h.SubscribeEvents()
	require.NotNil(t, ch)

	// Emit an event and verify it arrives.
	h.EmitEvent(NewEvent(EventTaskCreated, "test", EventData{TaskID: "t1"}))

	select {
	case ev := <-ch:
		assert.Equal(t, EventTaskCreated, ev.Type)
	default:
		t.Fatal("expected event on channel")
	}

	// Unsubscribe and verify channel is closed.
	h.UnsubscribeEvents(ch)
	_, open := <-ch
	assert.False(t, open, "channel should be closed after unsubscribe")
}

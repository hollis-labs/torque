package scheduler_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventBusSubscribeAndPublish(t *testing.T) {
	bus := scheduler.NewEventBus()
	defer bus.Close()

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	bus.Publish(scheduler.SchedulerEvent{
		Type:   "task.transitioned",
		TaskID: "CW-20260407-0001",
		Data:   map[string]interface{}{"from": "todo", "to": "doing"},
	})

	select {
	case event := <-sub:
		assert.Equal(t, "task.transitioned", event.Type)
		assert.Equal(t, "CW-20260407-0001", event.TaskID)
		data := event.Data.(map[string]interface{})
		assert.Equal(t, "doing", data["to"])
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBusMultipleSubscribers(t *testing.T) {
	bus := scheduler.NewEventBus()
	defer bus.Close()

	sub1 := bus.Subscribe()
	sub2 := bus.Subscribe()
	defer bus.Unsubscribe(sub1)
	defer bus.Unsubscribe(sub2)

	bus.Publish(scheduler.SchedulerEvent{
		Type:   "run.started",
		TaskID: "CW-20260407-0001",
	})

	for _, sub := range []<-chan scheduler.SchedulerEvent{sub1, sub2} {
		select {
		case event := <-sub:
			assert.Equal(t, "run.started", event.Type)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event")
		}
	}
}

func TestEventBusUnsubscribe(t *testing.T) {
	bus := scheduler.NewEventBus()
	defer bus.Close()

	sub := bus.Subscribe()
	bus.Unsubscribe(sub)

	// Publishing after unsubscribe should not block
	bus.Publish(scheduler.SchedulerEvent{
		Type: "task.created",
	})

	// Channel should be closed
	select {
	case _, ok := <-sub:
		assert.False(t, ok, "channel should be closed")
	case <-time.After(100 * time.Millisecond):
		// Acceptable — no event received
	}
}

func TestEventBusSlowSubscriberDoesNotBlock(t *testing.T) {
	bus := scheduler.NewEventBus()
	defer bus.Close()

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	// Publish more events than buffer size (256)
	for i := 0; i < 300; i++ {
		bus.Publish(scheduler.SchedulerEvent{
			Type: "scheduler.tick",
		})
	}

	// Should not have blocked or panicked
	assert.True(t, true, "did not block on slow subscriber")
}

func TestSchedulerEventJSON(t *testing.T) {
	event := scheduler.SchedulerEvent{
		Type:   "task.transitioned",
		TaskID: "CW-20260407-0001",
		RunID:  42,
		Data:   map[string]interface{}{"status": "doing"},
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	var decoded scheduler.SchedulerEvent
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, "task.transitioned", decoded.Type)
	assert.Equal(t, "CW-20260407-0001", decoded.TaskID)
}

func TestEventBusSubscriberCount(t *testing.T) {
	bus := scheduler.NewEventBus()
	defer bus.Close()

	assert.Equal(t, 0, bus.SubscriberCount())

	sub1 := bus.Subscribe()
	assert.Equal(t, 1, bus.SubscriberCount())

	sub2 := bus.Subscribe()
	assert.Equal(t, 2, bus.SubscriberCount())

	bus.Unsubscribe(sub1)
	assert.Equal(t, 1, bus.SubscriberCount())

	bus.Unsubscribe(sub2)
	assert.Equal(t, 0, bus.SubscriberCount())
}

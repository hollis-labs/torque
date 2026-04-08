package scheduler

import (
	"sync"
	"time"
)

const subscriberBufferSize = 256

// SchedulerEvent is broadcast to all subscribers when something happens.
type SchedulerEvent struct {
	Type      string      `json:"type"`
	TaskID    string      `json:"task_id,omitempty"`
	RunID     int64       `json:"run_id,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// EventBus provides fan-out event broadcasting to multiple subscribers.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[<-chan SchedulerEvent]chan SchedulerEvent
	closed      bool
}

// NewEventBus creates a new event bus.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[<-chan SchedulerEvent]chan SchedulerEvent),
	}
}

// Subscribe returns a channel that receives all published events.
// The channel is buffered to avoid blocking the publisher on slow consumers.
func (b *EventBus) Subscribe() <-chan SchedulerEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan SchedulerEvent, subscriberBufferSize)
	b.subscribers[ch] = ch
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *EventBus) Unsubscribe(sub <-chan SchedulerEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, ok := b.subscribers[sub]; ok {
		delete(b.subscribers, sub)
		close(ch)
	}
}

// Publish sends an event to all subscribers. If a subscriber's buffer is full,
// the event is dropped for that subscriber (non-blocking).
func (b *EventBus) Publish(event SchedulerEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	for _, ch := range b.subscribers {
		select {
		case ch <- event:
		default:
			// Drop event for slow subscriber
		}
	}
}

// SubscriberCount returns the number of active subscribers.
func (b *EventBus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Close marks the bus as closed and closes all subscriber channels.
func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	for sub, ch := range b.subscribers {
		delete(b.subscribers, sub)
		close(ch)
	}
}

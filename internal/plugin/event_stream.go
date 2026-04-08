package plugin

import goplugin "github.com/hollis-labs/plugin"

// SubscribeEvents creates a buffered channel and registers it for event broadcast.
func (h *ClockworkHost) SubscribeEvents() chan goplugin.Event {
	ch := make(chan goplugin.Event, 64)
	h.mu.Lock()
	h.eventSubs = append(h.eventSubs, ch)
	h.mu.Unlock()
	return ch
}

// UnsubscribeEvents removes a channel from the event broadcast list and closes it.
func (h *ClockworkHost) UnsubscribeEvents(ch chan goplugin.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := make([]chan goplugin.Event, 0, len(h.eventSubs))
	for _, s := range h.eventSubs {
		if s != ch {
			subs = append(subs, s)
		}
	}
	h.eventSubs = subs
	close(ch)
}

// broadcastEvent sends an event to all subscribers without blocking.
func (h *ClockworkHost) broadcastEvent(event goplugin.Event) {
	h.mu.RLock()
	subs := make([]chan goplugin.Event, len(h.eventSubs))
	copy(subs, h.eventSubs)
	h.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			// drop if full
		}
	}
}

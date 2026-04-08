package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSEHub manages Server-Sent Events subscribers and broadcasts.
type SSEHub struct {
	mu   sync.RWMutex
	subs map[chan SSEEvent]struct{}
}

// SSEEvent is a typed, timestamped event payload.
type SSEEvent struct {
	Type      string                 `json:"type"`
	Data      map[string]interface{} `json:"data"`
	Timestamp string                 `json:"timestamp"`
}

// NewSSEHub returns an initialised hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{subs: make(map[chan SSEEvent]struct{})}
}

// Broadcast sends an event to all active subscribers.
func (h *SSEHub) Broadcast(eventType string, data map[string]interface{}) {
	event := SSEEvent{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- event:
		default:
			// Drop slow subscriber
		}
	}
}

// ServeHTTP streams events to an SSE client.
func (h *SSEHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan SSEEvent, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}()

	ctx := r.Context()

	// Send initial connection event
	connEvent := SSEEvent{
		Type:      "connected",
		Data:      map[string]interface{}{"message": "SSE connected"},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if data, err := json.Marshal(connEvent); err == nil {
		fmt.Fprintf(w, "data: %s\n\n", data) //nolint:errcheck
		flusher.Flush()
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-ch:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data) //nolint:errcheck
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n") //nolint:errcheck
			flusher.Flush()
		}
	}
}

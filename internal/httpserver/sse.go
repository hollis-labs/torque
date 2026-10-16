package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// SSEHub manages Server-Sent Events subscribers and broadcasts.
type SSEHub struct {
	mu   sync.RWMutex
	subs map[chan SSEEvent]struct{}

	// outbox, when set, adds durable state events (event_outbox) to every
	// stream: frames carry ids and a reconnecting client resumes from
	// Last-Event-ID. See sse_outbox.go.
	outbox *outboxFeed
}

// SSEEvent is a typed, timestamped event payload.
type SSEEvent struct {
	Type      string                 `json:"type"`
	Data      map[string]interface{} `json:"data"`
	Timestamp string                 `json:"timestamp"`
	// Envelope is the full v1.1 event when the frame comes from the outbox.
	// Type and Data keep the legacy shape clients already read.
	Envelope json.RawMessage `json:"envelope,omitempty"`
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

	// Durable frames: replay from Last-Event-ID (or ?since=), then follow.
	var (
		feed *outboxFeed
		cur  int64
	)
	if feed = h.outbox; feed != nil {
		lastID := r.Header.Get("Last-Event-ID")
		if lastID == "" {
			lastID = r.URL.Query().Get("since")
		}
		var gap *SSEEvent
		var err error
		cur, gap, err = feed.start(ctx, lastID)
		if err != nil {
			log.Printf("[sse] outbox start: %v", err)
			feed = nil
		} else if gap != nil {
			if data, err := json.Marshal(gap); err == nil {
				fmt.Fprintf(w, "data: %s\n\n", data) //nolint:errcheck
				flusher.Flush()
			}
		}
	}

	for {
		var outboxWake <-chan struct{}
		if feed != nil {
			// Take the wait channel before reading so a commit in between
			// still wakes us.
			outboxWake = feed.wait()
			if err := feed.drain(ctx, w, flusher, &cur); err != nil {
				if ctx.Err() == nil {
					log.Printf("[sse] outbox drain: %v", err)
				}
				return
			}
		}
		if !h.waitOnce(ctx, ch, outboxWake, ticker.C, w, flusher) {
			return
		}
	}
}

// waitOnce blocks until something is worth a write, handles it, and reports
// false when the connection should end. An outbox wake returns true without
// writing: the caller drains the outbox at the top of its loop.
func (h *SSEHub) waitOnce(ctx context.Context, ch chan SSEEvent, outboxWake <-chan struct{}, tick <-chan time.Time, w http.ResponseWriter, flusher http.Flusher) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-outboxWake:
			return true
		case event := <-ch:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data) //nolint:errcheck
			flusher.Flush()
		case <-tick:
			fmt.Fprintf(w, ": heartbeat\n\n") //nolint:errcheck
			flusher.Flush()
		}
	}
}

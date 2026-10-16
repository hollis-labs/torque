package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Durable state events on /events (CW-20261016-0133).
//
// Frames from the outbox carry an SSE id "<generation>:<seq>", so a client
// that drops reconnects with Last-Event-ID and is replayed what it missed. The
// other frames on the stream (CRUD pings, scheduler run events) are
// best-effort and carry no id.
//
// Each connection reads the outbox at its own pace instead of being pushed to,
// so a slow client lags and catches up; it is never silently dropped. A client
// that cannot keep up at all is disconnected by the write deadline and
// resumes from its last id.

// outboxSource is the read side of the store's event_outbox.
type outboxSource interface {
	OutboxMeta(ctx context.Context) (sqlstore.OutboxMeta, error)
	OutboxHead(ctx context.Context) (int64, error)
	OutboxAfter(ctx context.Context, after int64, limit int) ([]sqlstore.OutboxEvent, error)
}

const (
	outboxBatch        = 500
	outboxPollInterval = time.Second
	outboxWriteTimeout = 10 * time.Second
)

// outboxFeed wakes connections when the outbox may have new rows.
type outboxFeed struct {
	src outboxSource

	mu sync.Mutex
	ch chan struct{}
}

func newOutboxFeed(src outboxSource) *outboxFeed {
	return &outboxFeed{src: src, ch: make(chan struct{})}
}

// wait returns a channel closed on the next notify. Take it BEFORE reading the
// outbox so a commit between the read and the wait is not missed.
func (f *outboxFeed) wait() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ch
}

func (f *outboxFeed) notify() {
	f.mu.Lock()
	close(f.ch)
	f.ch = make(chan struct{})
	f.mu.Unlock()
}

// EnableOutbox turns on durable frames. wake is the store's commit nudge; the
// poll interval covers a lost nudge and appends from another process. Call it
// before the server takes traffic, then run RunOutbox.
func (h *SSEHub) EnableOutbox(src outboxSource) {
	h.outbox = newOutboxFeed(src)
}

// RunOutbox fans commit nudges out to every connection until ctx ends.
func (h *SSEHub) RunOutbox(ctx context.Context, wake <-chan struct{}) {
	f := h.outbox
	if f == nil {
		return
	}
	tick := time.NewTicker(outboxPollInterval)
	defer tick.Stop()
	var seen int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			f.notify()
		case <-tick.C:
			head, err := f.src.OutboxHead(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("[sse] outbox head: %v", err)
				}
				continue
			}
			if head != seen {
				seen = head
				f.notify()
			}
		}
	}
}

// parseEventID parses "<generation>:<seq>".
func parseEventID(s string) (gen, seq int64, ok bool) {
	g, q, found := strings.Cut(strings.TrimSpace(s), ":")
	if !found {
		return 0, 0, false
	}
	gen, err1 := strconv.ParseInt(g, 10, 64)
	seq, err2 := strconv.ParseInt(q, 10, 64)
	if err1 != nil || err2 != nil || gen < 0 || seq < 0 {
		return 0, 0, false
	}
	return gen, seq, true
}

// start decides where a connection begins. No id means live only (from the
// head). A usable id resumes after it. Anything the log cannot honour yields a
// gap frame first, per the stream contract: reason, earliest_available and
// high_water.
func (f *outboxFeed) start(ctx context.Context, lastID string) (cur int64, gap *SSEEvent, err error) {
	meta, err := f.src.OutboxMeta(ctx)
	if err != nil {
		return 0, nil, err
	}
	head, err := f.src.OutboxHead(ctx)
	if err != nil {
		return 0, nil, err
	}
	if strings.TrimSpace(lastID) == "" {
		return head, nil, nil
	}
	gen, seq, ok := parseEventID(lastID)
	mk := func(reason string, resumeAt int64) *SSEEvent {
		return &SSEEvent{
			// The gap frame names where the stream resumes, so the browser's
			// Last-Event-ID moves past the position the log could not honour and a
			// reconnect does not repeat the gap.
			id:   fmt.Sprintf("%d:%d", meta.Generation, resumeAt),
			Type: "gap",
			Data: map[string]interface{}{
				"reason":             reason,
				"earliest_available": fmt.Sprintf("%d:%d", meta.Generation, meta.PrunedThrough+1),
				"high_water":         fmt.Sprintf("%d:%d", meta.Generation, head),
			},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
	}
	switch {
	case !ok:
		return head, mk("bad_cursor", head), nil
	case gen != meta.Generation:
		return head, mk("generation_changed", head), nil
	case seq > head:
		return head, mk("cursor_ahead", head), nil
	case seq < meta.PrunedThrough:
		return meta.PrunedThrough, mk("pruned", meta.PrunedThrough), nil
	}
	return seq, nil, nil
}

// drain writes every outbox row after *cur to w, advancing *cur. If the log
// has been trimmed past *cur since the last read, the client gets a gap frame
// (and *cur moves to the trim point) instead of silently missing rows. Each
// batch has a write deadline; a client that cannot take it in time is dropped
// and resumes from its last id.
func (f *outboxFeed) drain(ctx context.Context, w http.ResponseWriter, rc *http.ResponseController, fl http.Flusher, cur *int64) error {
	for {
		rows, err := f.src.OutboxAfter(ctx, *cur, outboxBatch)
		var pruned *sqlstore.OutboxPrunedError
		if errors.As(err, &pruned) {
			_ = rc.SetWriteDeadline(time.Now().Add(outboxWriteTimeout))
			writeFrame(w, fl, SSEEvent{
				Type: "gap",
				Data: map[string]interface{}{
					"reason":             "pruned",
					"earliest_available": fmt.Sprintf("%d:%d", pruned.Generation, pruned.PrunedThrough+1),
					"high_water":         fmt.Sprintf("%d:%d", pruned.Generation, pruned.Head),
				},
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				id:        fmt.Sprintf("%d:%d", pruned.Generation, pruned.PrunedThrough),
			})
			_ = rc.SetWriteDeadline(time.Time{})
			*cur = pruned.PrunedThrough
			continue
		}
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		_ = rc.SetWriteDeadline(time.Now().Add(outboxWriteTimeout))
		for _, r := range rows {
			frame, err := outboxFrame(r)
			if err != nil {
				// A row that cannot be rendered must not wedge the stream.
				log.Printf("[sse] outbox seq %d: %v", r.Seq, err)
				*cur = r.Seq
				continue
			}
			data, err := json.Marshal(frame)
			if err != nil {
				*cur = r.Seq
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d:%d\ndata: %s\n\n", r.Generation, r.Seq, data); err != nil {
				return err
			}
			*cur = r.Seq
		}
		fl.Flush()
		_ = rc.SetWriteDeadline(time.Time{})
		if len(rows) < outboxBatch {
			return nil
		}
	}
}

// outboxFrame renders one stored event as an SSE frame. One frame per event:
// for task.state_changed the top level keeps the shape the GUI already reads
// (type task.transitioned, data.task_id, data.status, plus from/to and the
// scheduler bridge's payload.to) and the full v1.1 event rides in Envelope.
// Other kinds are sent under their own name with the payload as data.
func outboxFrame(r sqlstore.OutboxEvent) (SSEEvent, error) {
	ev := r.Event
	envelope, err := json.Marshal(ev)
	if err != nil {
		return SSEEvent{}, err
	}
	var payload map[string]interface{}
	if len(ev.Payload) > 0 {
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			return SSEEvent{}, fmt.Errorf("payload: %w", err)
		}
	}
	ts := ev.Time.UTC().Format(time.RFC3339)

	if string(ev.Kind) == "task.state_changed" {
		entity, _ := payload["entity"].(map[string]interface{})
		id, _ := entity["id"].(string)
		to, _ := payload["to"].(string)
		from, _ := payload["from"].(string)
		data := map[string]interface{}{
			"task_id": id,
			"status":  to,
			"from":    from,
			"to":      to,
			"payload": map[string]interface{}{"from": from, "to": to},
		}
		if v, ok := payload["reason"]; ok {
			data["reason"] = v
		}
		if v, ok := payload["forced"]; ok {
			data["forced"] = v
		}
		if v, ok := payload["trigger"]; ok {
			data["trigger"] = v
		}
		return SSEEvent{Type: "task.transitioned", Data: data, Timestamp: ts, Envelope: envelope}, nil
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	return SSEEvent{Type: string(ev.Kind), Data: payload, Timestamp: ts, Envelope: envelope}, nil
}

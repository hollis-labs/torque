package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	gomsg "github.com/hollis-labs/go-messaging"
)

// SetMessaging wires a messaging.Store into the HTTP server. Routes for
// /api/v1/messages/* are always registered (in routes()); when no Store
// is wired they 503. Mirror of the sched=nil pattern.
func (s *Server) SetMessaging(store gomsg.Store) {
	s.msg = store
}

// /api/v1/messages
//   POST  /                       — Send (201 Created / 400 / 422 / 503)
//   GET   /{id}                   — Get (200 / 404 / 503)
//   POST  /{id}/cancel            — Cancel (204 No Content / 404 / 503)
//   POST  /{id}/consume           — Consume (204 No Content / 404 / 422 / 503; body: {recipient: <urn>})
//   GET   /inbox?to=<urn>...      — Inbox; drains, marks delivered (200 / 422 / 503)
//   GET   /thread/{thread_id}     — Thread; read-only (200 / 422 / 503)
//   GET   /subscribe?to=<urn>...  — SSE live stream (200 / 422 / 503)

type sendMessageRequest struct {
	Kind        gomsg.Kind        `json:"kind"`
	Channel     gomsg.Channel     `json:"channel,omitempty"`
	From        gomsg.Address     `json:"from"`
	To          gomsg.Address     `json:"to"`
	ThreadID    string            `json:"thread_id,omitempty"`
	InReplyTo   string            `json:"in_reply_to,omitempty"`
	Payload     json.RawMessage   `json:"payload,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	if s.msg == nil {
		writeError(w, http.StatusServiceUnavailable, "messaging store not configured")
		return
	}
	var req sendMessageRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.Kind == "" {
		writeError(w, http.StatusUnprocessableEntity, "kind is required")
		return
	}
	if req.From.IsZero() {
		writeError(w, http.StatusUnprocessableEntity, "from is required")
		return
	}
	if req.To.IsZero() {
		writeError(w, http.StatusUnprocessableEntity, "to is required")
		return
	}
	env := gomsg.Envelope{
		Kind:        req.Kind,
		Channel:     req.Channel,
		From:        req.From,
		To:          req.To,
		ThreadID:    req.ThreadID,
		InReplyTo:   req.InReplyTo,
		Payload:     req.Payload,
		ContentType: req.ContentType,
		Metadata:    req.Metadata,
	}
	out, err := s.msg.Send(r.Context(), env)
	if err != nil {
		if errors.Is(err, gomsg.ErrPresetLifecycle) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	if s.msg == nil {
		writeError(w, http.StatusServiceUnavailable, "messaging store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	env, err := s.msg.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, gomsg.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, env)
}

func (s *Server) cancelMessage(w http.ResponseWriter, r *http.Request) {
	if s.msg == nil {
		writeError(w, http.StatusServiceUnavailable, "messaging store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.msg.Cancel(r.Context(), id); err != nil {
		if errors.Is(err, gomsg.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type consumeMessageRequest struct {
	Recipient string `json:"recipient"`
}

func (s *Server) consumeMessage(w http.ResponseWriter, r *http.Request) {
	if s.msg == nil {
		writeError(w, http.StatusServiceUnavailable, "messaging store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	var req consumeMessageRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.Recipient == "" {
		writeError(w, http.StatusUnprocessableEntity, "recipient is required")
		return
	}
	addr, err := gomsg.ParseURN(req.Recipient)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "recipient: "+err.Error())
		return
	}
	if err := s.msg.Consume(r.Context(), id, addr); err != nil {
		if errors.Is(err, gomsg.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listInbox(w http.ResponseWriter, r *http.Request) {
	if s.msg == nil {
		writeError(w, http.StatusServiceUnavailable, "messaging store not configured")
		return
	}
	addr, err := parseAddrParam(r, "to")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	envs, err := s.msg.Inbox(r.Context(), addr, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"messages": envs})
}

func (s *Server) listThread(w http.ResponseWriter, r *http.Request) {
	if s.msg == nil {
		writeError(w, http.StatusServiceUnavailable, "messaging store not configured")
		return
	}
	threadID := chi.URLParam(r, "thread_id")
	if threadID == "" {
		writeError(w, http.StatusBadRequest, "missing thread_id")
		return
	}
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	envs, err := s.msg.Thread(r.Context(), threadID, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"messages": envs})
}

// subscribeMessages opens an SSE stream of new envelopes for ?to=<urn>.
// Closes when the client disconnects or the request context cancels.
func (s *Server) subscribeMessages(w http.ResponseWriter, r *http.Request) {
	if s.msg == nil {
		writeError(w, http.StatusServiceUnavailable, "messaging store not configured")
		return
	}
	addr, err := parseAddrParam(r, "to")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "SSE not supported by underlying ResponseWriter")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	ch, err := s.msg.Subscribe(ctx, addr, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fmt.Fprintf(w, ": subscribed to=%s\n\n", addr.URN()) //nolint:errcheck
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case env, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(env)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data) //nolint:errcheck
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n") //nolint:errcheck
			flusher.Flush()
		}
	}
}

func parseAddrParam(r *http.Request, name string) (gomsg.Address, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return gomsg.Address{}, fmt.Errorf("%s is required", name)
	}
	addr, err := gomsg.ParseURN(raw)
	if err != nil {
		return gomsg.Address{}, fmt.Errorf("%s: %w", name, err)
	}
	return addr, nil
}

func parseFilter(r *http.Request) (gomsg.Filter, error) {
	q := r.URL.Query()
	var f gomsg.Filter
	if vs := q["kind"]; len(vs) > 0 {
		for _, v := range vs {
			if v != "" {
				f.Kind = append(f.Kind, gomsg.Kind(v))
			}
		}
	}
	if vs := q["channel"]; len(vs) > 0 {
		for _, v := range vs {
			if v != "" {
				f.Channel = append(f.Channel, gomsg.Channel(v))
			}
		}
	}
	if v := q.Get("thread_id"); v != "" {
		f.ThreadID = v
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return gomsg.Filter{}, fmt.Errorf("limit must be a non-negative integer")
		}
		f.Limit = n
	}
	return f, nil
}

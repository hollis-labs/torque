package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"

	"github.com/hollis-labs/clockwork-manifold/internal/broker"
)

// SetBroker wires a typed envelope broker into the HTTP server. Routes
// for /api/v1/broker/* are always registered; when no broker is wired,
// handlers 503 (mirrors SetMessaging / SetScheduler / WithSessionMgr).
//
// CW-20260503-0013 (S1.3) — broker layers Clockwork-specific validation
// and SSE publishing on top of the messaging.Store wired via SetMessaging.
func (s *Server) SetBroker(b *broker.Broker) {
	s.broker = b
}

// /api/v1/broker
//   POST  /send                 — Send a typed envelope (200/422/413/503)
//   POST  /request              — Send kind=request, block until response
//   GET   /inbox?to=<urn>...    — Drain inbox; publishes envelope.delivered

type brokerSendRequest struct {
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

func (s *Server) brokerSend(w http.ResponseWriter, r *http.Request) {
	if s.broker == nil {
		writeError(w, http.StatusServiceUnavailable, "broker not configured")
		return
	}
	var req brokerSendRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
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
	out, err := s.broker.Send(r.Context(), env)
	if err != nil {
		s.brokerWriteSendErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

type brokerRequestPayload struct {
	Channel        gomsg.Channel     `json:"channel,omitempty"`
	From           gomsg.Address     `json:"from"`
	To             gomsg.Address     `json:"to"`
	ThreadID       string            `json:"thread_id,omitempty"`
	Payload        json.RawMessage   `json:"payload,omitempty"`
	ContentType    string            `json:"content_type,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

func (s *Server) brokerRequest(w http.ResponseWriter, r *http.Request) {
	if s.broker == nil {
		writeError(w, http.StatusServiceUnavailable, "broker not configured")
		return
	}
	var req brokerRequestPayload
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	timeout := req.TimeoutSeconds
	if timeout == 0 {
		timeout = broker.DefaultRequestTimeout
	}
	if timeout < broker.MinRequestTimeout || timeout > broker.MaxRequestTimeout {
		writeError(w, http.StatusUnprocessableEntity, "timeout_seconds out of range")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()
	env := gomsg.Envelope{
		Channel:     req.Channel,
		From:        req.From,
		To:          req.To,
		ThreadID:    req.ThreadID,
		Payload:     req.Payload,
		ContentType: req.ContentType,
		Metadata:    req.Metadata,
	}
	resp, err := s.broker.Request(ctx, env)
	if err != nil {
		switch {
		case errors.Is(err, gomsg.ErrRequestTimeout):
			writeError(w, http.StatusGatewayTimeout, err.Error())
		case errors.Is(err, broker.ErrValidation):
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		case errors.Is(err, broker.ErrPayloadTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) brokerInbox(w http.ResponseWriter, r *http.Request) {
	if s.broker == nil {
		writeError(w, http.StatusServiceUnavailable, "broker not configured")
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
	envs, err := s.broker.Inbox(r.Context(), addr, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"envelopes": envs})
}

// brokerWriteSendErr maps broker.Send errors to HTTP status codes:
//   - ErrValidation → 422
//   - ErrPayloadTooLarge → 413
//   - gomsg.ErrPresetLifecycle → 422
//   - else → 500
func (s *Server) brokerWriteSendErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, broker.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, broker.ErrPayloadTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, gomsg.ErrPresetLifecycle):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

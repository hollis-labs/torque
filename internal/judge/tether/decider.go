// Package tether is the judge.Decider that asks Tether's Decide operation
// (POST /ai/decide), which routes to a decision provider such as JEV. The wire
// shape is Tether's as built (docs/api/README.md in the tether repo): the request
// is {"request": {caller_id, model_hint, decision: {state, purpose, on_error,
// questions}}} and the response {"response": {provider, model, decision:
// {answers, latency_ms, request_id}, usage, route}}. Only encode and decode
// depend on it.
//
// It reads the base URL and token the same way the rest of Torque does
// (TORQUE_TETHER_URL, TORQUE_TETHER_TOKEN). It never logs, prints or returns
// the token, and never includes request text in an error.
package tether

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/judge"
)

// DefaultModel is the pinned provider model. Aliases such as jev-latest can
// move to a newer model; a pinned id keeps an evaluation reproducible.
const DefaultModel = "jev-1.13.0"

// CallerID names Torque to the gateway's budgets and audit.
const CallerID = "torque"

// Decider implements judge.Decider over Tether's HTTP API.
type Decider struct {
	BaseURL string
	Token   string
	Model   string
	HTTP    *http.Client
	// Backoff is the wait before the first retry, doubling after each
	// (default 500ms). Only 429 and 5xx-overload statuses are retried.
	Backoff  time.Duration
	Attempts int // default 3
}

// New returns a Decider for baseURL, or nil when baseURL is empty. baseURL is
// either an http(s) URL or unix:///absolute/path.sock for Tether's local
// socket (its default transport; ~/.tether/run/tetherd.sock).
func New(baseURL, token string) *Decider {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if sock, ok := strings.CutPrefix(baseURL, "unix://"); ok {
		client.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}
		baseURL = "http://unix" // the host is a placeholder; the dialer decides where the bytes go
	}
	return &Decider{BaseURL: baseURL, Token: token, Model: DefaultModel, HTTP: client}
}

// Name implements judge.Decider.
func (d *Decider) Name() string { return "tether-decide" }

// Error is a failed Decide call. Message is Tether's own error text, never
// the request.
type Error struct {
	Status  int
	Code    string // Tether's error code: invalid_request, decision_failed, internal_error, ...
	Kind    string // for decision_failed: auth, rate_limited, overloaded, timeout, circuit_open, unavailable
	Message string

	retryAfter time.Duration // from a Retry-After header, capped
}

func (e *Error) Error() string {
	head := fmt.Sprintf("tether decide: HTTP %d", e.Status)
	if e.Kind != "" {
		head += " (" + e.Kind + ")"
	}
	if e.Message == "" {
		return head
	}
	return head + ": " + e.Message
}

// Retryable reports whether trying again could succeed: rate limited (429),
// overloaded (503) or a timeout (504). Not an open circuit breaker (waiting
// is the point of it), not 502 (auth or unavailable), and not a budget
// rejection or a malformed request. The gateway already retries the
// provider's own 429 and 529.
func (e *Error) Retryable() bool {
	if e.Kind == "circuit_open" || e.Kind == "auth" || e.Kind == "unavailable" {
		return false
	}
	switch e.Status {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// wireRequest is Tether's /ai/decide request.
type wireRequest struct {
	Request struct {
		CallerID  string       `json:"caller_id,omitempty"`
		ModelHint string       `json:"model_hint,omitempty"`
		Decision  wireDecision `json:"decision"`
	} `json:"request"`
}

type wireDecision struct {
	State     any              `json:"state"`
	Purpose   string           `json:"purpose"`
	OnError   string           `json:"on_error,omitempty"`
	Questions []judge.Question `json:"questions"`
}

// wireResponse is Tether's /ai/decide response.
type wireResponse struct {
	Response struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Decision struct {
			Answers   map[string]judge.Answer `json:"answers"`
			LatencyMS int64                   `json:"latency_ms"`
			RequestID string                  `json:"request_id"`
		} `json:"decision"`
		Usage judge.Usage `json:"usage"`
	} `json:"response"`
}

// ErrNoPurpose is returned before any call is made: Tether's budget is scoped
// by purpose and rejects a decide request that carries none.
var ErrNoPurpose = errors.New("tether decide: a request must carry a purpose (spend accounting is per purpose)")

func (d *Decider) encode(req judge.Request) ([]byte, error) {
	if strings.TrimSpace(req.Purpose) == "" {
		return nil, ErrNoPurpose
	}
	model := req.Model
	if model == "" {
		model = d.Model
	}
	if model == "" {
		model = DefaultModel
	}
	var w wireRequest
	w.Request.CallerID = req.CallerID
	if w.Request.CallerID == "" {
		w.Request.CallerID = CallerID
	}
	w.Request.ModelHint = model
	w.Request.Decision = wireDecision{State: req.State, Purpose: req.Purpose, OnError: req.OnError, Questions: req.Questions}
	if w.Request.Decision.OnError == "" {
		w.Request.Decision.OnError = "fail_open"
	}
	return json.Marshal(w) // questions and options are slices: their order is preserved on the wire
}

func (d *Decider) decode(req judge.Request, body []byte) (judge.Response, error) {
	var w wireResponse
	if err := json.Unmarshal(body, &w); err != nil {
		return judge.Response{}, fmt.Errorf("tether decide: bad response: %w", err)
	}
	r := w.Response
	resp := judge.Response{Model: r.Model, Answers: map[string]judge.Answer{}, Usage: r.Usage, Latency: time.Duration(r.Decision.LatencyMS) * time.Millisecond}
	for _, q := range req.Questions {
		a, ok := r.Decision.Answers[q.Key]
		if !ok {
			return judge.Response{}, fmt.Errorf("tether decide: no answer for question %q", q.Key)
		}
		if a.Type == "" {
			a.Type = q.Type
		}
		if a.Type != q.Type {
			return judge.Response{}, fmt.Errorf("tether decide: question %q is %s but the answer is %s", q.Key, q.Type, a.Type)
		}
		switch a.Type {
		case judge.Choice:
			if a.Choice == "" {
				return judge.Response{}, fmt.Errorf("tether decide: question %q has no choice", q.Key)
			}
			if a.Confidence == 0 && len(a.Probabilities) > 0 {
				a.Confidence = judge.ChoiceConfidence(a.Probabilities)
			}
		case judge.Score:
			if a.Confidence == 0 && len(a.Probabilities) > 0 {
				a.Confidence = judge.ScoreConfidence(judge.ScoreLevels(a.Probabilities))
			}
		case judge.Noul:
			// No confidence of its own: judge.NoulConfidence derives it from the probability.
			if a.Noul < 0 || a.Noul > 1 {
				return judge.Response{}, fmt.Errorf("tether decide: question %q probability %v is outside 0..1", q.Key, a.Noul)
			}
		}
		resp.Answers[q.Key] = a
	}
	return resp, nil
}

// Decide implements judge.Decider.
func (d *Decider) Decide(ctx context.Context, req judge.Request) (judge.Response, error) {
	body, err := d.encode(req)
	if err != nil {
		return judge.Response{}, err
	}
	attempts, backoff := d.Attempts, d.Backoff
	if attempts < 1 {
		attempts = 3
	}
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	start := time.Now()
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			wait := backoff << (i - 1)
			var te *Error
			if errors.As(last, &te) && te.retryAfter > 0 {
				wait = te.retryAfter
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return judge.Response{}, ctx.Err()
			}
		}
		resp, err := d.once(ctx, req, body)
		if err == nil {
			if resp.Latency == 0 {
				resp.Latency = time.Since(start)
			}
			return resp, nil
		}
		last = err
		var te *Error
		if !errors.As(err, &te) || !te.Retryable() {
			return judge.Response{}, err
		}
	}
	return judge.Response{}, last
}

func (d *Decider) once(ctx context.Context, req judge.Request, body []byte) (judge.Response, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, d.BaseURL+"/ai/decide", bytes.NewReader(body))
	if err != nil {
		return judge.Response{}, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json")
	if d.Token != "" {
		hr.Header.Set("Authorization", "Bearer "+d.Token)
	}
	client := d.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(hr)
	if err != nil {
		// Do not wrap the request: it carries task text. A transport error names only the URL.
		return judge.Response{}, fmt.Errorf("tether decide: %w", sanitize(err, d.Token))
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		code, kind, msg := errorFields(raw, d.Token)
		e := &Error{Status: res.StatusCode, Code: code, Kind: kind, Message: msg}
		if s := res.Header.Get("Retry-After"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 10 {
				e.retryAfter = time.Duration(n) * time.Second
			}
		}
		return judge.Response{}, e
	}
	return d.decode(req, raw)
}

// errorFields reads Tether's error envelope: {"error": {"code", "message",
// "details": {"kind", ...}}}. It never returns the token.
func errorFields(raw []byte, token string) (code, kind, msg string) {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Kind string `json:"kind"`
			} `json:"details"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &env) == nil {
		code, kind, msg = env.Error.Code, env.Error.Details.Kind, env.Error.Message
		if msg == "" {
			msg = env.Message
		}
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return code, kind, sanitizeText(msg, token)
}

func sanitize(err error, token string) error {
	return errors.New(sanitizeText(err.Error(), token))
}

func sanitizeText(s, token string) string {
	if len(token) >= 4 {
		s = strings.ReplaceAll(s, token, "[redacted]")
	}
	return judge.Scrub(s)
}

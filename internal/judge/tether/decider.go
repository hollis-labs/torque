// Package tether is the judge.Decider that asks Tether's Decide operation
// (POST /ai/decide), which routes to a decision provider such as JEV. The wire
// shape is the contract in the Tether handoff (Tesseract workspace
// project/tether/workspace/handoff/jev_decide_operation_and_adapter_2026_10_11);
// if Tether's as-built shape differs, only encode and decode here change.
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

// New returns a Decider for baseURL, or nil when baseURL is empty.
func New(baseURL, token string) *Decider {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &Decider{BaseURL: baseURL, Token: token, Model: DefaultModel, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Name implements judge.Decider.
func (d *Decider) Name() string { return "tether-decide" }

// Error is a failed Decide call. Message is Tether's own error text, never
// the request.
type Error struct {
	Status  int
	Message string

	retryAfter time.Duration // from a Retry-After header, capped
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("tether decide: HTTP %d", e.Status)
	}
	return fmt.Sprintf("tether decide: HTTP %d: %s", e.Status, e.Message)
}

// Retryable reports whether trying again could succeed: rate limited (429),
// overloaded (529) or a transient gateway failure.
func (e *Error) Retryable() bool {
	switch e.Status {
	case http.StatusTooManyRequests, 529, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// wireResponse is Tether's /ai/decide response.
type wireResponse struct {
	Answers   map[string]judge.Answer `json:"answers"`
	Usage     judge.Usage             `json:"usage"`
	Provider  string                  `json:"provider"`
	Model     string                  `json:"model"`
	LatencyMS int64                   `json:"latency_ms"`
	RequestID string                  `json:"request_id"`
}

func (d *Decider) encode(req judge.Request) ([]byte, error) {
	if req.Model == "" {
		req.Model = d.Model
	}
	if req.Model == "" {
		req.Model = DefaultModel
	}
	if req.CallerID == "" {
		req.CallerID = CallerID
	}
	if req.OnError == "" {
		req.OnError = "fail_open"
	}
	return json.Marshal(req) // questions and options are slices: their order is preserved on the wire
}

func (d *Decider) decode(req judge.Request, body []byte) (judge.Response, error) {
	var w wireResponse
	if err := json.Unmarshal(body, &w); err != nil {
		return judge.Response{}, fmt.Errorf("tether decide: bad response: %w", err)
	}
	resp := judge.Response{Model: w.Model, Answers: map[string]judge.Answer{}, Usage: w.Usage, Latency: time.Duration(w.LatencyMS) * time.Millisecond}
	for _, q := range req.Questions {
		a, ok := w.Answers[q.Key]
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
		e := &Error{Status: res.StatusCode, Message: errorMessage(raw, d.Token)}
		if s := res.Header.Get("Retry-After"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 10 {
				e.retryAfter = time.Duration(n) * time.Second
			}
		}
		return judge.Response{}, e
	}
	return d.decode(req, raw)
}

// errorMessage pulls a short message out of an error body, never the token.
func errorMessage(raw []byte, token string) string {
	var env struct {
		Error   any    `json:"error"`
		Message string `json:"message"`
	}
	msg := ""
	if json.Unmarshal(raw, &env) == nil {
		switch e := env.Error.(type) {
		case string:
			msg = e
		case map[string]any:
			if m, ok := e["message"].(string); ok {
				msg = m
			}
		}
		if msg == "" {
			msg = env.Message
		}
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return sanitizeText(msg, token)
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

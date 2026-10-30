package tetherclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// APIError is a non-2xx answer from Tether in its error envelope
// ({"error":{"code","message"}}).
type APIError struct {
	Status  int
	Code    string
	Message string
	Path    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tether %s: HTTP %d %s: %s", e.Path, e.Status, e.Code, e.Message)
}

// Launch refusals that leave the session in `created`: retry the launch later.
const (
	CodeExecutionPaused           = "execution_paused"
	CodeEnvironmentCapReach       = "environment_cap_reached"
	CodeProjectCapReached         = "project_cap_reached"
	CodeIdempotencyConflict       = "idempotency_conflict"
	StateCreated                  = "created"
	StateLaunching                = "launching"
	StateRunning                  = "running"
	StateCompleted                = "completed"
	StateFailed                   = "failed"
	StateKilled                   = "killed"
	OutputKindFinal               = "final"
	OutputKindFailure             = "failure"
	OutputKindQuestion            = "question"
	OutputKindApproval            = "approval"
	OutputKindTerminal            = "terminal"
	EventTurnOutput               = "session.turn_output"
	EventStateChanged             = "session.state_changed"
	maxResponseBytes        int64 = 4 << 20
)

// IsCapacityRefusal reports whether err is a launch refused by Tether's
// execution policy (paused or at a cap). The session stays `created`, so the
// caller defers and calls Launch again; it is not a failure.
func IsCapacityRefusal(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusConflict {
		return false
	}
	switch ae.Code {
	case CodeExecutionPaused, CodeEnvironmentCapReach, CodeProjectCapReached:
		return true
	}
	return false
}

// IsNotFound reports a 404 from Tether.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// IsTerminalState reports whether a Tether session state is final.
func IsTerminalState(state string) bool {
	switch state {
	case StateCompleted, StateFailed, StateKilled:
		return true
	}
	return false
}

// CreateRequest is the subset of POST /sessions Torque sends. Tether is
// plumbing here: Torque decides what to launch; nothing is dispatched by
// Tether. TaskID is deliberately absent: Tether drops it from non-operator
// callers, so Torque keeps the session-to-task mapping on the run row.
type CreateRequest struct {
	Launch         string `json:"launch"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Worker         string `json:"worker,omitempty"`
	BootPrompt     string `json:"boot_prompt,omitempty"`
	PromptAppend   string `json:"prompt_append,omitempty"`
	// Override is a JSON object string (system_prompt, env, route, lifecycle).
	Override string `json:"override,omitempty"`
	// Route is a raw {"channel","kinds"} object. A launch whose route selects
	// kinds stages the turn text as a message and the turn_output event then
	// carries only message_id, no text; route kinds [] keeps the text inline.
	Route json.RawMessage `json:"route,omitempty"`
}

// Session is the id Tether assigned plus what the create/launch calls echo.
type Session struct {
	ID         string `json:"id"`
	Workspace  string `json:"workspace"`
	Log        string `json:"log"`
	ProviderID string `json:"provider_id"`
	Replayed   bool   `json:"replayed"`
}

// SessionState is GET /sessions/{id}.
type SessionState struct {
	ID           string `json:"id"`
	LaunchID     string `json:"launch_id"`
	ProjectID    string `json:"project_id"`
	State        string `json:"state"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	WorkstreamID string `json:"workstream_id,omitempty"`
	Stalled      bool   `json:"stalled,omitempty"`
	StallReason  string `json:"stall_reason,omitempty"`
}

// Event is one row of GET /sessions/{id}/events.
type Event struct {
	Seq         int64  `json:"seq"`
	At          string `json:"at"`
	Scope       string `json:"scope"`
	SessionID   string `json:"session_id"`
	Kind        string `json:"kind"`
	PayloadJSON string `json:"payload_json"`
}

// TurnOutput is the payload of a session.turn_output event.
type TurnOutput struct {
	TurnID        string `json:"turn_id"`
	Kind          string `json:"kind"` // final | question | approval | failure | terminal
	StopReason    string `json:"stop_reason"`
	Text          string `json:"text"`
	TextTruncated bool   `json:"text_truncated"`
	MessageID     string `json:"message_id"`
}

// TurnOutput decodes the event payload when Kind is session.turn_output.
func (e Event) TurnOutput() (*TurnOutput, bool) {
	if e.Kind != EventTurnOutput {
		return nil, false
	}
	var t TurnOutput
	if json.Unmarshal([]byte(e.PayloadJSON), &t) != nil {
		return nil, false
	}
	return &t, true
}

// WaitResult is GET /sessions/{id}/wait: it returns on exit only.
type WaitResult struct {
	ExitCode   int    `json:"exit_code"`
	StopReason string `json:"stop_reason"`
	OutputKind string `json:"output_kind"`
	OutputText string `json:"output_text"`
}

func (c *Client) do(ctx context.Context, hc *http.Client, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Status: resp.StatusCode, Path: path}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Error.Code != "" {
			ae.Code, ae.Message = env.Error.Code, env.Error.Message
		} else {
			ae.Message = strings.TrimSpace(string(raw))
			if len(ae.Message) > 200 {
				ae.Message = ae.Message[:200]
			}
		}
		return resp.StatusCode, ae
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("tether %s: bad response: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// CreateSession is POST /sessions. With an IdempotencyKey a retry of the same
// request returns the same session (Replayed true); a different request under
// the same key is a 409 idempotency_conflict.
func (c *Client) CreateSession(ctx context.Context, req CreateRequest) (*Session, error) {
	var out Session
	if _, err := c.do(ctx, c.HTTP, http.MethodPost, "/sessions", req, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, errors.New("tether /sessions: response has no id")
	}
	return &out, nil
}

// LaunchSession is POST /sessions/{id}/launch. A capacity refusal (see
// IsCapacityRefusal) leaves the session `created`.
func (c *Client) LaunchSession(ctx context.Context, id string) (*Session, error) {
	var out Session
	if _, err := c.do(ctx, c.HTTP, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/launch", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSession is GET /sessions/{id}.
func (c *Client) GetSession(ctx context.Context, id string) (*SessionState, error) {
	var out SessionState
	if _, err := c.do(ctx, c.HTTP, http.MethodGet, "/sessions/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StopSession is POST /sessions/{id}/stop. 204 means the signal was sent;
// 404 means the session is not running (already exited): treated as success.
func (c *Client) StopSession(ctx context.Context, id string) error {
	_, err := c.do(ctx, c.HTTP, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/stop", nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// SessionEvents is GET /sessions/{id}/events: newest first. cursor 0 starts
// at the newest; otherwise rows with seq < cursor.
func (c *Client) SessionEvents(ctx context.Context, id string, limit int, cursor int64) ([]Event, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor > 0 {
		q.Set("cursor", strconv.FormatInt(cursor, 10))
	}
	path := "/sessions/" + url.PathEscape(id) + "/events"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Events []Event `json:"events"`
	}
	if _, err := c.do(ctx, c.HTTP, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Events, nil
}

// WaitSession is GET /sessions/{id}/wait: a long-poll that returns on exit
// only, so a session that goes idle never answers. Bound it with ctx.
func (c *Client) WaitSession(ctx context.Context, id string) (*WaitResult, error) {
	hc := &http.Client{Transport: c.transport} // no overall timeout; ctx bounds it
	var out WaitResult
	if _, err := c.do(ctx, hc, http.MethodGet, "/sessions/"+url.PathEscape(id)+"/wait", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

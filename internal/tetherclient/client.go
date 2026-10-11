// Package tetherclient reads Tether's capacity and team-run state over HTTP.
// Shapes follow the agreed proposal in the CW-20261011-0015 thread (comment
// 16342); Tether has not built them yet, so a Client is only constructed when
// TORQUE_TETHER_URL is set. It never mutates.
package tetherclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Capacity is Tether's admission state for one environment.
type Capacity struct {
	EnvironmentID  string `json:"environment_id"`
	MaxSessions    int    `json:"max_sessions"`
	ActiveSessions int    `json:"active_sessions"`
	Admission      string `json:"admission"` // open | paused
	Reason         string `json:"reason"`
}

// RunState is a team run's state from the team store.
type RunState struct {
	Status  string `json:"status"`
	EndedAt string `json:"ended_at"`
	Reason  string `json:"reason"`
}

// Ended reports whether the run is over. Only an explicit ended_at or an
// ended status counts; an unknown status is not evidence of death.
func (r RunState) Ended() bool {
	if r.EndedAt != "" {
		return true
	}
	switch strings.ToLower(r.Status) {
	case "ended", "dissolved", "stopped", "completed", "failed":
		return true
	}
	return false
}

// ErrRunNotFound is returned when Tether has no run with that id.
var ErrRunNotFound = errors.New("tether: team run not found")

// Client calls Tether's HTTP API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New returns a Client for baseURL, or nil when baseURL is empty.
func New(baseURL, token string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

func (c *Client) get(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return 0, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("tether %s: HTTP %d", path, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("tether %s: bad response: %w", path, err)
	}
	return resp.StatusCode, nil
}

// Capacity reads GET /capacity for an environment.
func (c *Client) Capacity(ctx context.Context, environmentID string) (*Capacity, error) {
	var out Capacity
	path := "/capacity"
	if environmentID != "" {
		path += "?environment_id=" + url.QueryEscape(environmentID)
	}
	if _, err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Run reads GET /teams/runs/{run_id}. A 404 is ErrRunNotFound.
func (c *Client) Run(ctx context.Context, runID string) (*RunState, error) {
	var out RunState
	code, err := c.get(ctx, "/teams/runs/"+url.PathEscape(runID), &out)
	if code == http.StatusNotFound {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

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
	"strings"
)

// TeamError is a non-2xx answer from a /teams verb. Message is Tether's own
// error text with the client's token redacted.
type TeamError struct {
	Verb    string
	Status  int
	Code    string
	Message string
}

func (e *TeamError) Error() string {
	return fmt.Sprintf("tether teams/%s: HTTP %d %s: %s", e.Verb, e.Status, e.Code, e.Message)
}

// Definition pin of an authored team-member definition.
type DefinitionRef struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Digest   string `json:"digest"`
}

// TeamSlot is one named role in a team definition.
type TeamSlot struct {
	Name          string        `json:"name"`
	Role          string        `json:"role"`
	DefinitionRef DefinitionRef `json:"definition_ref"`
	Resolution    string        `json:"resolution"`
	Pool          string        `json:"pool,omitempty"`
	Identities    []string      `json:"identities,omitempty"`
	Activation    string        `json:"activation"`
	Required      bool          `json:"required"`
	Min           int           `json:"min"`
	Max           int           `json:"max"`
}

// TeamForm is the body of POST /teams/form: {team, launch}. Team is passed
// through as a generic map so this client does not mirror Tether's full schema.
type TeamForm struct {
	Team   map[string]any `json:"team"`
	Launch TeamLaunch     `json:"launch"`
}

// TeamLaunch carries formation options.
type TeamLaunch struct {
	Counts         map[string]int      `json:"counts,omitempty"`
	PoolIdentities map[string][]string `json:"pool_identities,omitempty"`
}

// TeamResult is the part of a team verb's answer Torque uses.
type TeamResult struct {
	Run *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"run,omitempty"`
	Coordinator  string   `json:"coordinator,omitempty"`
	DeliveryKeys []string `json:"delivery_keys,omitempty"`
}

// RunID is the formed run's id, or "".
func (r TeamResult) RunID() string {
	if r.Run == nil {
		return ""
	}
	return r.Run.ID
}

// TeamRunInfo is one run's state from GET /teams/roster?run_id=.
type TeamRunInfo struct {
	RunID   string
	Status  string
	Members []TeamMember
}

// TeamMember is a roster member.
type TeamMember struct {
	ID     string `json:"id"`
	Slot   string `json:"slot"`
	Status string `json:"status"`
}

// Ended reports whether the run is over: a terminal run status, or no
// non-owner member still active (the coordinator slot has no member).
func (r TeamRunInfo) Ended() bool {
	switch strings.ToLower(r.Status) {
	case "completed", "failed", "canceled", "cancelled", "rejected", "dissolved", "ended":
		return true
	}
	// Dissolve stops or releases every member; a roster with members and none
	// active is an ended run even if the run status string is unfamiliar.
	if len(r.Members) == 0 {
		return false
	}
	for _, m := range r.Members {
		if strings.EqualFold(m.Status, "active") {
			return false
		}
	}
	return true
}

func (c *Client) postTeam(ctx context.Context, verb, key string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/teams/"+verb, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return c.redact(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		te := &TeamError{Verb: verb, Status: resp.StatusCode}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil {
			te.Code, te.Message = env.Error.Code, env.Error.Message
		}
		if te.Message == "" {
			te.Message = strings.TrimSpace(string(data))
			if len(te.Message) > 300 {
				te.Message = te.Message[:300]
			}
		}
		if c.Token != "" {
			te.Message = strings.ReplaceAll(te.Message, c.Token, "[redacted]")
		}
		return te
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("tether teams/%s: bad response: %w", verb, err)
		}
	}
	return nil
}

func (c *Client) redact(err error) error {
	if c.Token == "" || err == nil {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.Token, "[redacted]"))
}

// FormTeam calls POST /teams/form. key is the idempotency key: a retry with the
// same key returns the same run.
func (c *Client) FormTeam(ctx context.Context, key string, f TeamForm) (*TeamResult, error) {
	var out TeamResult
	if err := c.postTeam(ctx, "form", key, f, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DelegateTeam hands work to a member address (e.g. "@orchestrator") with POST /teams/delegate.
func (c *Client) DelegateTeam(ctx context.Context, key, runID, address, body string) (*TeamResult, error) {
	var out TeamResult
	req := map[string]any{"run_id": runID, "address": address, "body": body}
	if err := c.postTeam(ctx, "delegate", key, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DissolveTeam ends the run and stops every member (POST /teams/dissolve).
func (c *Client) DissolveTeam(ctx context.Context, key, runID string) error {
	return c.postTeam(ctx, "dissolve", key, map[string]any{"run_id": runID}, nil)
}

// TeamRun reads one run from GET /teams/roster?run_id=. A 404 is ErrRunNotFound.
func (c *Client) TeamRun(ctx context.Context, runID string) (*TeamRunInfo, error) {
	var out struct {
		Runs []struct {
			Run struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"run"`
			Members []TeamMember `json:"members"`
		} `json:"runs"`
	}
	code, err := c.get(ctx, "/teams/roster?run_id="+url.QueryEscape(runID), &out)
	if code == http.StatusNotFound {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, c.redact(err)
	}
	if len(out.Runs) == 0 {
		return nil, ErrRunNotFound
	}
	r := out.Runs[0]
	return &TeamRunInfo{RunID: r.Run.ID, Status: r.Run.Status, Members: r.Members}, nil
}

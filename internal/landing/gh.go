package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GH reads pull-request state through the gh CLI. Dir is any directory inside
// the repository when ref is not a full URL.
type GH struct {
	Dir     string
	Timeout time.Duration
}

// PR implements SourceControl.
func (g GH) PR(ctx context.Context, ref string) (PRState, error) {
	t := g.Timeout
	if t <= 0 {
		t = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", ref, "--json", "url,state,statusCheckRollup")
	cmd.Dir = g.Dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return PRState{}, fmt.Errorf("gh pr view: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return PRState{}, fmt.Errorf("gh pr view: %w", err)
	}
	return parsePRView(out)
}

func parsePRView(b []byte) (PRState, error) {
	var v struct {
		URL    string `json:"url"`
		State  string `json:"state"`
		Rollup []struct {
			Name       string `json:"name"`    // CheckRun
			Context    string `json:"context"` // StatusContext
			Conclusion string `json:"conclusion"`
			State      string `json:"state"` // StatusContext
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return PRState{}, fmt.Errorf("parse gh output: %w", err)
	}
	st := PRState{URL: v.URL, State: v.State}
	for _, r := range v.Rollup {
		c := Check{Name: r.Name, Conclusion: r.Conclusion}
		if c.Name == "" {
			c.Name = r.Context
		}
		if c.Conclusion == "" && r.State != "" && r.State != "PENDING" {
			c.Conclusion = r.State
		}
		st.Checks = append(st.Checks, c)
	}
	return st, nil
}

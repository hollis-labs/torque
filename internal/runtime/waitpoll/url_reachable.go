package waitpoll

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// URLReachable is a predicate that issues a HEAD request and fires when the
// response is 2xx/3xx. params: {"url": "<url>"}.
//
// Connection errors (DNS, refused, timeout) are treated as not-yet rather
// than predicate errors so a target that comes online mid-poll transitions
// cleanly without needing an operator to reset the wait task.
type URLReachable struct {
	client *http.Client
}

// NewURLReachable returns a URLReachable predicate using the given client or
// a 10-second-timeout default when client is nil.
func NewURLReachable(c *http.Client) *URLReachable {
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	return &URLReachable{client: c}
}

func (p *URLReachable) Type() string { return "url_reachable" }

func (p *URLReachable) Validate(params map[string]any) error {
	u, _ := params["url"].(string)
	if u == "" {
		return fmt.Errorf("url_reachable: params.url required")
	}
	return nil
}

func (p *URLReachable) Evaluate(ctx context.Context, params map[string]any) (bool, error) {
	u, _ := params["url"].(string)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		// Unreachable — not a predicate error, just not-yet.
		return false, nil
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400, nil
}

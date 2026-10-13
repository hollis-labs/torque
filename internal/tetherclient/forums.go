package tetherclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ForumsClient uses a backend-held operator credential for the human GUI.
// It deliberately does not accept browser headers or identity fields.
type ForumsClient struct {
	BaseURL, OperatorToken string
	HTTP                   *http.Client
}

type ForumsError struct {
	Status        int
	Code, Message string
}

func (e *ForumsError) Error() string { return e.Message }
func unavailable() error {
	return &ForumsError{503, "tether_unavailable", "Forums unavailable: check Tether and the backend operator credential."}
}

// NewForums supports HTTP(S) and unix:///absolute/path.sock. Missing credentials
// leave the feature in a degraded state; session tokens are never a fallback.
func NewForums(baseURL, operatorToken string) *ForumsClient {
	c := &ForumsClient{BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), OperatorToken: strings.TrimSpace(operatorToken), HTTP: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	if sock, ok := strings.CutPrefix(c.BaseURL, "unix://"); ok {
		c.HTTP.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}
		c.BaseURL = "http://unix"
	}
	return c
}

// Do forwards only an explicitly selected forum operation and native JSON.
// Transport errors are sanitized so credentials and socket paths cannot leak.
func (c *ForumsClient) Do(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, int, error) {
	if c == nil || c.BaseURL == "" || c.OperatorToken == "" {
		return nil, 0, unavailable()
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, unavailable()
	}
	req.Header.Set("Authorization", "Bearer "+c.OperatorToken)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, unavailable()
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil || len(data) > 4<<20 || !json.Valid(data) {
		return nil, 0, unavailable()
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		if resp.StatusCode >= 500 || resp.StatusCode < 400 {
			return nil, 0, unavailable()
		}
		var out struct {
			Error struct{ Code, Message string }
		}
		if json.Unmarshal(data, &out) != nil || out.Error.Message == "" {
			return nil, 0, unavailable()
		}
		// Never echo a misconfigured upstream's credential in an error.
		return nil, 0, &ForumsError{resp.StatusCode, out.Error.Code, strings.ReplaceAll(out.Error.Message, c.OperatorToken, "[redacted]")}
	}
	return data, resp.StatusCode, nil
}

var _ error = (*ForumsError)(nil)

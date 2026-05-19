// httpstore.go — a reusable HTTP-backed messaging.Store for the federation hop.
//
// Federation in Torque is delivered by routing (ADR-0001 §4): the authority-
// routing Router (router.go) serves a local authority from the SQLite Store
// and a foreign authority from a *remote* Store that talks to the peer install
// that homes it. HTTPStore is that remote Store.
//
// It generalizes the proven `go-agentmux-client` `httpStore` pattern: a full
// messaging.Store implemented over an HTTP route surface. Two things are
// parameterized so it is reusable rather than welded to one client:
//
//   - the peer endpoint (any scheme://host[:port]); and
//   - the *http.Client — so the federation-auth layer (ADR-0002) can inject an
//     mTLS client with a client certificate and server-cert pinning, while a
//     test or a plaintext deployment can inject a bare client.
//
// Surface scope (ADR-0002 §4, §5): the federation hop carries push-delivery
// and reply correlation only — Send / Get / Thread / Consume / Cancel.
// Inbox and Subscribe are *never* federated: an install drains an inbox only
// for its own (local) recipients, so the Router resolves those to the local
// Store and never dispatches them here. HTTPStore implements them solely to
// satisfy the messaging.Store interface and fails them loudly with
// ErrStoreUnavailable rather than pretending to be a federated mailbox.
package messaging

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
	"time"

	messaging "github.com/hollis-labs/go-messaging"
)

// Compile-time assertion: *HTTPStore satisfies messaging.Store, so it is
// substitutable as a foreign route in RouterConfig.ForeignRoutes.
var _ messaging.Store = (*HTTPStore)(nil)

// DefaultFederationBasePath is the route prefix the federation HTTP surface is
// mounted at on a peer install (ADR-0002 §5: `POST /federation/v1/messages`,
// …). HTTPStore joins this to the peer endpoint. WithBasePath overrides it —
// useful for tests and for any app that reuses HTTPStore against a different
// mount point.
const DefaultFederationBasePath = "/federation/v1/messages"

// defaultHTTPTimeout bounds a single federation request. Every federated call
// is a short request/response exchange (no streaming — Subscribe is not
// federated), so a fixed ceiling is safe and keeps a hung peer from pinning a
// caller indefinitely. A caller that needs different behavior injects its own
// *http.Client via WithHTTPClient.
const defaultHTTPTimeout = 30 * time.Second

// HTTPStore is a messaging.Store backed by a peer install's HTTP federation
// surface. It is immutable after construction and safe for concurrent use to
// the same degree as the *http.Client it wraps (the stdlib client is).
type HTTPStore struct {
	// base is the fully-joined URL of the federation messages collection,
	// e.g. "https://peer.example:8443/federation/v1/messages", no trailing
	// slash.
	base   string
	client *http.Client
}

// HTTPStoreOption configures a HTTPStore at construction.
type HTTPStoreOption func(*httpStoreConfig)

type httpStoreConfig struct {
	basePath string
	client   *http.Client
}

// WithHTTPClient injects the *http.Client HTTPStore dials with. This is the
// seam the federation-auth layer (ADR-0002) uses to supply an mTLS client —
// a client certificate plus server-cert pinning via the Transport's
// tls.Config / VerifyPeerCertificate. A nil client is ignored (the default
// is kept).
func WithHTTPClient(c *http.Client) HTTPStoreOption {
	return func(cfg *httpStoreConfig) {
		if c != nil {
			cfg.client = c
		}
	}
}

// WithBasePath overrides DefaultFederationBasePath — the route prefix the
// federation surface is mounted at on the peer. An empty value is ignored.
func WithBasePath(p string) HTTPStoreOption {
	return func(cfg *httpStoreConfig) {
		if p != "" {
			cfg.basePath = p
		}
	}
}

// NewHTTPStore constructs a HTTPStore that talks to the peer install reachable
// at endpoint (a scheme://host[:port] URL). It fails if endpoint is empty or
// is not a valid absolute http/https URL — a misconfigured foreign route is
// rejected at construction time, not on first call.
func NewHTTPStore(endpoint string, opts ...HTTPStoreOption) (*HTTPStore, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("messaging: HTTPStore requires a non-empty endpoint")
	}
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return nil, fmt.Errorf("messaging: HTTPStore endpoint %q: %w", endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("messaging: HTTPStore endpoint %q: scheme must be http or https", endpoint)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("messaging: HTTPStore endpoint %q: missing host", endpoint)
	}

	cfg := httpStoreConfig{
		basePath: DefaultFederationBasePath,
		client:   &http.Client{Timeout: defaultHTTPTimeout},
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	base := strings.TrimRight(u.String(), "/") + "/" + strings.Trim(cfg.basePath, "/")
	return &HTTPStore{base: base, client: cfg.client}, nil
}

// Send delivers an envelope to the peer (POST <base>).
//
// The Store assigns ID and CreatedAt, so caller-set values are zeroed before
// the request. DeliveredAt / ConsumedAt MUST be nil on input (messaging.Store
// contract); a preset value is rejected client-side with ErrPresetLifecycle,
// matching the local Store and saving a round trip.
func (s *HTTPStore) Send(ctx context.Context, env messaging.Envelope) (messaging.Envelope, error) {
	if env.DeliveredAt != nil || env.ConsumedAt != nil {
		return messaging.Envelope{}, messaging.ErrPresetLifecycle
	}
	env.ID = ""
	env.CreatedAt = time.Time{}

	var out messaging.Envelope
	if err := s.do(ctx, http.MethodPost, "", env, http.StatusCreated, &out); err != nil {
		return messaging.Envelope{}, err
	}
	return out, nil
}

// Get retrieves a single envelope by ID (GET <base>/{id}). Returns
// messaging.ErrNotFound if the peer does not have it.
func (s *HTTPStore) Get(ctx context.Context, id string) (messaging.Envelope, error) {
	var out messaging.Envelope
	if err := s.do(ctx, http.MethodGet, url.PathEscape(id), nil, http.StatusOK, &out); err != nil {
		return messaging.Envelope{}, err
	}
	return out, nil
}

// Thread returns the envelopes sharing threadID, chronologically
// (GET <base>/thread/{thread_id}). Read-only; no delivery side effects.
func (s *HTTPStore) Thread(ctx context.Context, threadID string, f messaging.Filter) ([]messaging.Envelope, error) {
	path := "thread/" + url.PathEscape(threadID)
	if q := encodeFilter(f); q != "" {
		path += "?" + q
	}
	var out struct {
		Messages []messaging.Envelope `json:"messages"`
	}
	if err := s.do(ctx, http.MethodGet, path, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

// Consume advances ConsumedAt for (envelope, recipient) on the peer
// (POST <base>/{id}/consume, body {"recipient": <urn>}). Idempotent.
func (s *HTTPStore) Consume(ctx context.Context, id string, recipient messaging.Address) error {
	body := map[string]string{"recipient": recipient.URN()}
	return s.do(ctx, http.MethodPost, url.PathEscape(id)+"/consume", body, http.StatusNoContent, nil)
}

// Cancel marks an envelope dead on the peer (POST <base>/{id}/cancel).
// Idempotent; returns messaging.ErrNotFound only if the peer never held it.
func (s *HTTPStore) Cancel(ctx context.Context, id string) error {
	return s.do(ctx, http.MethodPost, url.PathEscape(id)+"/cancel", nil, http.StatusNoContent, nil)
}

// Inbox is not federated (ADR-0002 §4): an install drains an inbox only for
// its own local recipients, so the Router never dispatches Inbox to a foreign
// route. HTTPStore implements it only to satisfy messaging.Store and fails
// loudly rather than silently returning an empty inbox.
func (s *HTTPStore) Inbox(ctx context.Context, to messaging.Address, f messaging.Filter) ([]messaging.Envelope, error) {
	return nil, fmt.Errorf("%w: Inbox is not federated (ADR-0002 §4)", messaging.ErrStoreUnavailable)
}

// Subscribe is not federated (ADR-0002 §4) — same rationale as Inbox.
func (s *HTTPStore) Subscribe(ctx context.Context, to messaging.Address, f messaging.Filter) (<-chan messaging.Envelope, error) {
	return nil, fmt.Errorf("%w: Subscribe is not federated (ADR-0002 §4)", messaging.ErrStoreUnavailable)
}

// do performs one federation request and decodes the response.
//
//   - relPath is appended to the store's base URL (already escaped/encoded).
//   - body, when non-nil, is JSON-encoded as the request body.
//   - a status other than wantStatus is translated to a messaging sentinel
//     error by mapHTTPError.
//   - out, when non-nil, receives the JSON-decoded response body.
func (s *HTTPStore) do(ctx context.Context, method, relPath string, body any, wantStatus int, out any) error {
	target := s.base
	if relPath != "" {
		target += "/" + relPath
	}

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("messaging: encode federation request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reqBody)
	if err != nil {
		return fmt.Errorf("messaging: build federation request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		// A transport-level failure (TLS handshake, dial, timeout) means the
		// peer is unreachable — surface it as ErrStoreUnavailable so callers
		// see a clean, already-handled sentinel (ADR-0002 §5).
		return fmt.Errorf("%w: federation peer %s: %w", messaging.ErrStoreUnavailable, s.base, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != wantStatus {
		return mapHTTPError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("messaging: decode federation response: %w", err)
	}
	return nil
}

// mapHTTPError translates a non-success federation HTTP response into the
// canonical messaging sentinel errors (ADR-0002 §5), so a caller of the Router
// sees the same clean failures a local Store produces.
//
//	404 → ErrNotFound          (envelope absent / authority not homed by peer)
//	422 → ErrPresetLifecycle   (caller set Store-managed lifecycle fields)
//	403 → ErrStoreUnavailable  (authorization failure — not leaked as NotFound)
//	503 → ErrStoreUnavailable  (peer's Store not configured / unavailable)
//	otherwise → an opaque, wrapped HTTP error
func mapHTTPError(resp *http.Response) error {
	msg := readErrorBody(resp.Body)
	base := fmt.Errorf("federation peer returned HTTP %d", resp.StatusCode)
	if msg != "" {
		base = fmt.Errorf("federation peer returned HTTP %d: %s", resp.StatusCode, msg)
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %w", messaging.ErrNotFound, base)
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: %w", messaging.ErrPresetLifecycle, base)
	case http.StatusForbidden, http.StatusUnauthorized, http.StatusServiceUnavailable:
		return fmt.Errorf("%w: %w", messaging.ErrStoreUnavailable, base)
	default:
		return fmt.Errorf("messaging: %w", base)
	}
}

// readErrorBody best-effort extracts the {"error": "..."} message Torque's
// HTTP handlers emit (writeError). A body that is absent or not in that shape
// yields an empty string — error mapping then relies on the status code alone.
func readErrorBody(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, 8<<10))
	if err != nil || len(b) == 0 {
		return ""
	}
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &payload) == nil && payload.Error != "" {
		return payload.Error
	}
	return strings.TrimSpace(string(b))
}

// encodeFilter renders a messaging.Filter as the URL query the Torque messages
// handlers parse (parseFilter): repeated `kind` / `channel` params, a single
// `thread_id`, and a `limit`. An empty filter yields an empty string.
func encodeFilter(f messaging.Filter) string {
	q := url.Values{}
	for _, k := range f.Kind {
		if k != "" {
			q.Add("kind", string(k))
		}
	}
	for _, c := range f.Channel {
		if c != "" {
			q.Add("channel", string(c))
		}
	}
	if f.ThreadID != "" {
		q.Set("thread_id", f.ThreadID)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return q.Encode()
}

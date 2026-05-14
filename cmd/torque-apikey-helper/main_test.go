package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeKeychain is the test keychainAccessor. Read returns the canned
// payload; Write captures whatever the resolver pushed back so tests
// can assert on the rewritten JSON.
type fakeKeychain struct {
	mu      sync.Mutex
	payload string
	written string
	readErr error
	writeOK bool
	writeFn func(account, payload string) error
}

func (f *fakeKeychain) Read(account string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return "", f.readErr
	}
	return f.payload, nil
}

func (f *fakeKeychain) Write(account, payload string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = payload
	f.writeOK = true
	if f.writeFn != nil {
		return f.writeFn(account, payload)
	}
	return nil
}

func (f *fakeKeychain) lastWritten() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written
}

// fixedTime is a stable clock injected into the resolver. Tests pass
// epoch ms via .UnixMilli; the real now() uses time.Now.
func fixedTime(epochMS int64) func() time.Time {
	t := time.UnixMilli(epochMS)
	return func() time.Time { return t }
}

// buildKeychainJSON constructs a payload matching the shape claude
// writes. extras lets a test add unrelated top-level keys (mcpOAuth)
// to verify they round-trip on rewrite.
func buildKeychainJSON(t *testing.T, accessToken, refreshToken string, expiresAt int64, scopes []string, extras map[string]interface{}) string {
	t.Helper()
	cao := map[string]interface{}{
		"accessToken":      accessToken,
		"expiresAt":        expiresAt,
		"subscriptionType": "max",
	}
	if refreshToken != "" {
		cao["refreshToken"] = refreshToken
	}
	if len(scopes) > 0 {
		cao["scopes"] = scopes
	}
	top := map[string]interface{}{"claudeAiOauth": cao}
	for k, v := range extras {
		top[k] = v
	}
	b, err := json.Marshal(top)
	if err != nil {
		t.Fatalf("marshal keychain JSON: %v", err)
	}
	return string(b)
}

// startRefreshServer spins up an httptest.Server that mimics
// platform.claude.com's /v1/oauth/token. The handler validates the
// request shape and returns whatever the test wants.
func startRefreshServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestResolve_TokenNotExpired_ReturnsExisting(t *testing.T) {
	now := int64(1_700_000_000_000) // arbitrary epoch ms
	// expires 10 minutes from now — well outside the refresh margin
	exp := now + 10*60*1000
	payload := buildKeychainJSON(t, "sk-ant-oat01-existing", "sk-ant-ort01-rt", exp,
		[]string{"user:profile", "user:inference"}, nil)
	kc := &fakeKeychain{payload: payload}

	// HTTP doer that explodes if called — refresh must NOT happen.
	doer := httpDoerFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected refresh call: %s %s", req.Method, req.URL)
		return nil, nil
	})

	stderr := &bytes.Buffer{}
	r := &resolver{
		keychain:   kc,
		httpClient: doer,
		oauthURL:   "http://refresh.invalid/",
		now:        fixedTime(now),
		stderr:     stderr,
	}

	tok, err := r.resolveFromKeychain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-ant-oat01-existing" {
		t.Fatalf("got token %q, want sk-ant-oat01-existing", tok)
	}
	if kc.lastWritten() != "" {
		t.Fatalf("keychain should not have been written: %q", kc.lastWritten())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr output: %q", stderr.String())
	}
}

func TestResolve_TokenExpired_RefreshesAndPersists(t *testing.T) {
	now := int64(1_700_000_000_000)
	// expired 30s ago
	exp := now - 30_000

	srv := startRefreshServer(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", req.Method)
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q, want application/json", got)
		}
		if got := req.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Errorf("anthropic-beta = %q, want oauth-2025-04-20", got)
		}
		body, _ := io.ReadAll(req.Body)
		var got map[string]string
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got["grant_type"] != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got["grant_type"])
		}
		if got["refresh_token"] != "sk-ant-ort01-old" {
			t.Errorf("refresh_token = %q, want sk-ant-ort01-old", got["refresh_token"])
		}
		if got["client_id"] != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
			t.Errorf("client_id = %q, want claude code client id", got["client_id"])
		}
		if !strings.Contains(got["scope"], "user:inference") {
			t.Errorf("scope = %q, want it to include user:inference", got["scope"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "sk-ant-oat01-new",
			"refresh_token": "sk-ant-ort01-new",
			"expires_in":    3600,
			"scope":         "user:profile user:inference user:sessions:claude_code",
			"token_type":    "Bearer",
		})
	})

	payload := buildKeychainJSON(t, "sk-ant-oat01-old", "sk-ant-ort01-old", exp,
		[]string{"user:profile", "user:inference"},
		map[string]interface{}{
			"mcpOAuth": map[string]interface{}{"foo": "bar"},
		})
	kc := &fakeKeychain{payload: payload}

	stderr := &bytes.Buffer{}
	r := &resolver{
		keychain:   kc,
		httpClient: srv.Client(),
		oauthURL:   srv.URL,
		now:        fixedTime(now),
		stderr:     stderr,
	}

	tok, err := r.resolveFromKeychain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-ant-oat01-new" {
		t.Fatalf("got token %q, want sk-ant-oat01-new", tok)
	}

	written := kc.lastWritten()
	if written == "" {
		t.Fatalf("keychain should have been written")
	}
	var rewritten map[string]interface{}
	if err := json.Unmarshal([]byte(written), &rewritten); err != nil {
		t.Fatalf("rewritten payload not valid JSON: %v", err)
	}
	cao, ok := rewritten["claudeAiOauth"].(map[string]interface{})
	if !ok {
		t.Fatalf("rewritten payload missing claudeAiOauth: %v", rewritten)
	}
	if cao["accessToken"] != "sk-ant-oat01-new" {
		t.Errorf("rewritten accessToken = %v, want sk-ant-oat01-new", cao["accessToken"])
	}
	if cao["refreshToken"] != "sk-ant-ort01-new" {
		t.Errorf("rewritten refreshToken = %v, want sk-ant-ort01-new", cao["refreshToken"])
	}
	wantExpiresAt := float64(now + 3600*1000)
	if got, _ := cao["expiresAt"].(float64); got != wantExpiresAt {
		t.Errorf("rewritten expiresAt = %v, want %v", cao["expiresAt"], wantExpiresAt)
	}
	if cao["subscriptionType"] != "max" {
		t.Errorf("subscriptionType lost on rewrite: %v", cao["subscriptionType"])
	}
	if _, ok := rewritten["mcpOAuth"]; !ok {
		t.Errorf("mcpOAuth lost on rewrite: %v", rewritten)
	}
	// stderr should be quiet on success
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr on success: %q", stderr.String())
	}
}

func TestResolve_RefreshFails401_FallsBackToExisting(t *testing.T) {
	now := int64(1_700_000_000_000)
	exp := now - 30_000 // expired

	srv := startRefreshServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})

	payload := buildKeychainJSON(t, "sk-ant-oat01-stale", "sk-ant-ort01-rt", exp,
		[]string{"user:profile"}, nil)
	kc := &fakeKeychain{payload: payload}

	stderr := &bytes.Buffer{}
	r := &resolver{
		keychain:   kc,
		httpClient: srv.Client(),
		oauthURL:   srv.URL,
		now:        fixedTime(now),
		stderr:     stderr,
	}

	tok, err := r.resolveFromKeychain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-ant-oat01-stale" {
		t.Fatalf("got token %q, want fallback sk-ant-oat01-stale", tok)
	}
	if kc.lastWritten() != "" {
		t.Errorf("keychain should not be written when refresh fails: %q", kc.lastWritten())
	}
	if !strings.Contains(stderr.String(), "oauth refresh failed") {
		t.Errorf("stderr missing refresh-failure warning: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "401") {
		t.Errorf("stderr missing HTTP status code: %q", stderr.String())
	}
}

func TestResolve_NoRefreshToken_SkipsRefreshWithWarning(t *testing.T) {
	now := int64(1_700_000_000_000)
	exp := now - 30_000

	// HTTP doer that explodes if called — refresh must NOT be attempted.
	doer := httpDoerFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected refresh call: %s %s", req.Method, req.URL)
		return nil, nil
	})

	payload := buildKeychainJSON(t, "sk-ant-oat01-existing", "", exp,
		[]string{"user:profile"}, nil)
	kc := &fakeKeychain{payload: payload}

	stderr := &bytes.Buffer{}
	r := &resolver{
		keychain:   kc,
		httpClient: doer,
		oauthURL:   "http://refresh.invalid/",
		now:        fixedTime(now),
		stderr:     stderr,
	}

	tok, err := r.resolveFromKeychain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-ant-oat01-existing" {
		t.Fatalf("got token %q, want sk-ant-oat01-existing", tok)
	}
	if !strings.Contains(stderr.String(), "no refreshToken in keychain") {
		t.Errorf("stderr missing missing-refresh-token warning: %q", stderr.String())
	}
}

func TestResolve_NetworkTimeout_FallsBackToExisting(t *testing.T) {
	now := int64(1_700_000_000_000)
	exp := now - 30_000

	// httpDoer that always returns a timeout-style network error.
	doer := httpDoerFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp 127.0.0.1:0: i/o timeout")
	})

	payload := buildKeychainJSON(t, "sk-ant-oat01-stale", "sk-ant-ort01-rt", exp,
		[]string{"user:profile"}, nil)
	kc := &fakeKeychain{payload: payload}

	stderr := &bytes.Buffer{}
	r := &resolver{
		keychain:   kc,
		httpClient: doer,
		oauthURL:   "http://refresh.invalid/",
		now:        fixedTime(now),
		stderr:     stderr,
	}

	tok, err := r.resolveFromKeychain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-ant-oat01-stale" {
		t.Fatalf("got token %q, want fallback sk-ant-oat01-stale", tok)
	}
	if !strings.Contains(stderr.String(), "oauth refresh failed") {
		t.Errorf("stderr missing refresh-failure warning: %q", stderr.String())
	}
}

func TestResolve_NoExpiresAtField_ReturnsExistingNoRefresh(t *testing.T) {
	// Older claude versions may not write expiresAt. Helper should not
	// attempt refresh in that case (no signal that the token is stale).
	now := int64(1_700_000_000_000)

	doer := httpDoerFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected refresh call")
		return nil, nil
	})

	// Build payload with expiresAt=0 (omitempty drops it).
	payload := buildKeychainJSON(t, "sk-ant-oat01-noexp", "sk-ant-ort01-rt", 0, nil, nil)
	kc := &fakeKeychain{payload: payload}

	stderr := &bytes.Buffer{}
	r := &resolver{
		keychain:   kc,
		httpClient: doer,
		oauthURL:   "http://refresh.invalid/",
		now:        fixedTime(now),
		stderr:     stderr,
	}

	tok, err := r.resolveFromKeychain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-ant-oat01-noexp" {
		t.Fatalf("got token %q, want sk-ant-oat01-noexp", tok)
	}
}

func TestResolve_RefreshResponseRotatesRefreshToken(t *testing.T) {
	// Even when the server returns the same refresh_token, helper must
	// still rewrite expiresAt — otherwise next invocation re-refreshes.
	now := int64(1_700_000_000_000)
	exp := now - 30_000

	srv := startRefreshServer(t, func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "sk-ant-oat01-new",
			// refresh_token absent — server kept the old one
			"expires_in": 1800,
			"scope":      "user:profile",
		})
	})

	payload := buildKeychainJSON(t, "sk-ant-oat01-old", "sk-ant-ort01-stable", exp,
		[]string{"user:profile"}, nil)
	kc := &fakeKeychain{payload: payload}

	r := &resolver{
		keychain:   kc,
		httpClient: srv.Client(),
		oauthURL:   srv.URL,
		now:        fixedTime(now),
		stderr:     &bytes.Buffer{},
	}

	tok, err := r.resolveFromKeychain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-ant-oat01-new" {
		t.Fatalf("got token %q, want sk-ant-oat01-new", tok)
	}
	var rewritten map[string]interface{}
	if err := json.Unmarshal([]byte(kc.lastWritten()), &rewritten); err != nil {
		t.Fatalf("rewritten payload not JSON: %v", err)
	}
	cao := rewritten["claudeAiOauth"].(map[string]interface{})
	if cao["refreshToken"] != "sk-ant-ort01-stable" {
		t.Errorf("refreshToken = %v, want preserved sk-ant-ort01-stable", cao["refreshToken"])
	}
	if got := cao["expiresAt"].(float64); got != float64(now+1800*1000) {
		t.Errorf("expiresAt = %v, want %v", got, now+1800*1000)
	}
}

func TestResolve_RefreshSucceedsButKeychainWriteFails_StillReturnsNewToken(t *testing.T) {
	now := int64(1_700_000_000_000)
	exp := now - 30_000

	srv := startRefreshServer(t, func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "sk-ant-oat01-new",
			"refresh_token": "sk-ant-ort01-new",
			"expires_in":    3600,
			"scope":         "user:profile",
		})
	})

	payload := buildKeychainJSON(t, "sk-ant-oat01-old", "sk-ant-ort01-rt", exp,
		[]string{"user:profile"}, nil)
	kc := &fakeKeychain{
		payload: payload,
		writeFn: func(account, payload string) error {
			return fmt.Errorf("simulated keychain write failure")
		},
	}

	stderr := &bytes.Buffer{}
	r := &resolver{
		keychain:   kc,
		httpClient: srv.Client(),
		oauthURL:   srv.URL,
		now:        fixedTime(now),
		stderr:     stderr,
	}

	tok, err := r.resolveFromKeychain()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Refresh succeeded, write failed — but the refreshed token is
	// still valid in memory and useful for the current dispatch.
	if tok != "sk-ant-oat01-new" {
		t.Fatalf("got token %q, want sk-ant-oat01-new", tok)
	}
	if !strings.Contains(stderr.String(), "keychain write-back failed") {
		t.Errorf("stderr missing write-back warning: %q", stderr.String())
	}
}

func TestResolve_EnvVarPrecedence(t *testing.T) {
	// ANTHROPIC_API_KEY wins over keychain. We exercise the full
	// resolve() path here, not just resolveFromKeychain.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-env-wins")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")

	kc := &fakeKeychain{
		readErr: fmt.Errorf("keychain should not be read"),
	}
	r := &resolver{
		keychain: kc,
		httpClient: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("http should not be called")
		}),
		oauthURL: "http://refresh.invalid/",
		now:      fixedTime(0),
		stderr:   &bytes.Buffer{},
	}

	tok, err := r.resolve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-ant-api03-env-wins" {
		t.Fatalf("got token %q, want sk-ant-api03-env-wins", tok)
	}
}

func TestResolve_AuthTokenFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-from-env")

	kc := &fakeKeychain{readErr: fmt.Errorf("keychain should not be read")}
	r := &resolver{
		keychain: kc,
		httpClient: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("http should not be called")
		}),
		oauthURL: "http://refresh.invalid/",
		now:      fixedTime(0),
		stderr:   &bytes.Buffer{},
	}

	tok, err := r.resolve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "sk-ant-oat01-from-env" {
		t.Fatalf("got token %q, want sk-ant-oat01-from-env", tok)
	}
}

func TestRewriteKeychainPayload_PreservesUnknownFields(t *testing.T) {
	// Round-trip an mcpOAuth blob plus a custom future field to ensure
	// rewrite touches only claudeAiOauth's typed members.
	raw := `{
		"claudeAiOauth": {
			"accessToken": "old-at",
			"refreshToken": "old-rt",
			"expiresAt": 1,
			"scopes": ["user:profile"],
			"subscriptionType": "max",
			"rateLimitTier": "default_claude_max_20x",
			"futureField": "should-survive"
		},
		"mcpOAuth": {"server-1": {"x": 1}},
		"someTopLevelExtra": true
	}`

	updated, err := rewriteKeychainPayload(raw, "new-at", "new-rt", 999, []string{"user:profile", "user:inference"})
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(updated), &got); err != nil {
		t.Fatalf("rewritten payload not JSON: %v", err)
	}
	cao := got["claudeAiOauth"].(map[string]interface{})
	if cao["accessToken"] != "new-at" || cao["refreshToken"] != "new-rt" {
		t.Errorf("tokens not rotated: %v", cao)
	}
	if got, _ := cao["expiresAt"].(float64); got != 999 {
		t.Errorf("expiresAt not updated: %v", cao["expiresAt"])
	}
	if cao["subscriptionType"] != "max" {
		t.Errorf("subscriptionType lost: %v", cao["subscriptionType"])
	}
	if cao["rateLimitTier"] != "default_claude_max_20x" {
		t.Errorf("rateLimitTier lost: %v", cao["rateLimitTier"])
	}
	if cao["futureField"] != "should-survive" {
		t.Errorf("future field lost: %v", cao["futureField"])
	}
	if _, ok := got["mcpOAuth"]; !ok {
		t.Errorf("mcpOAuth lost: %v", got)
	}
	if got["someTopLevelExtra"] != true {
		t.Errorf("top-level extra lost: %v", got["someTopLevelExtra"])
	}
}

// httpDoerFunc adapts a plain function to httpDoer for tests that need
// an HTTP client without a real server (e.g. timeout simulation).
type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

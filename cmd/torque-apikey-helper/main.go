// Command torque-apikey-helper resolves an Anthropic API bearer token
// for the bare-mode claude CLI's apiKeyHelper hook.
//
// Resolution order:
//
//  1. ANTHROPIC_API_KEY in env — printed verbatim, exit 0.
//  2. ANTHROPIC_AUTH_TOKEN in env — printed verbatim, exit 0.
//  3. macOS keychain (security find-generic-password -s "Claude Code-credentials"):
//     parses the keychain payload's claudeAiOauth.accessToken (the
//     `sk-ant-oat01-...` OAuth access token) and prints it.
//     If `expiresAt` is past or within 60s of now, the helper attempts an
//     OAuth refresh (POST refresh_token to platform.claude.com), rewrites
//     the keychain entry with the rotated tokens, and prints the new
//     access token. Refresh failures are logged to stderr and the helper
//     falls back to printing the (possibly-expired) existing token —
//     letting the API surface a 401 the caller can react to is preferable
//     to a hard helper failure that breaks every dispatch.
//  4. (No fallbacks beyond macOS today.) Other-OS branches print a clear
//     error to stderr and exit 1; consumers on those platforms must set
//     ANTHROPIC_API_KEY explicitly until a portable secret-store path
//     lands.
//
// Stdout discipline: exactly one line containing the token, no trailing
// whitespace beyond the implicit newline. Bare-mode claude consumes the
// first line of stdout per its docs.
//
// Exit codes:
//   - 0: token resolved, written to stdout.
//   - 1: no token available; reason logged to stderr.
//
// Token format empirically verified (CW-20260509-0016): the keychain's
// `claudeAiOauth.accessToken` (`sk-ant-oat01-...`) authenticates against
// the Anthropic API directly via apiKeyHelper — no exchange to
// `sk-ant-api03-` needed. Subscription users (Pro/Max/Team) authenticated
// via `claude` interactive login → keychain are covered automatically.
//
// OAuth refresh details (verified by inspecting claude 2.1.138):
//   - Endpoint: https://platform.claude.com/v1/oauth/token
//   - Method: POST, Content-Type: application/json
//   - Body: {"grant_type":"refresh_token","refresh_token":"<rt>",
//     "client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e","scope":"<space-joined>"}
//   - anthropic-beta: oauth-2025-04-20
//   - Response: {"access_token":"...","refresh_token":"...",
//     "expires_in":<seconds>,"scope":"..."}
//   - keychain stores expiresAt as epoch-ms; we recompute as
//     now_ms + expires_in*1000.
//
// For testing, the OAuth URL can be overridden via the
// TORQUE_APIKEY_OAUTH_URL env var so tests don't hit the live endpoint.
// The keychain layer is also testable via the keychainAccessor interface.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// claudeCodeClientID is Anthropic's published OAuth client_id for the
// Claude Code CLI, extracted from the binary at versions/2.1.138 (search
// for `CLIENT_ID:"..."` in the bundled JS). Refresh requests must include
// this client_id; sending a different value yields HTTP 400.
const claudeCodeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// defaultOAuthTokenURL is the production OAuth refresh endpoint. Tests
// override this via TORQUE_APIKEY_OAUTH_URL.
const defaultOAuthTokenURL = "https://platform.claude.com/v1/oauth/token"

// anthropicBetaOAuth is the beta header value claude sends with refresh
// requests; including it preserves wire compatibility with the live
// auth flow.
const anthropicBetaOAuth = "oauth-2025-04-20"

// refreshMarginMs: refresh when the token has fewer than this many
// milliseconds left until expiry. 60s gives slow daemon dispatch room
// to complete the in-flight request before the new token is needed.
const refreshMarginMs = 60_000

// refreshTimeout caps the HTTP refresh round-trip. Slow networks
// shouldn't hang every helper invocation; if refresh stalls, fall back
// to the existing token.
const refreshTimeout = 8 * time.Second

// keychainServiceName matches the service `claude` uses when writing
// the credentials entry via `security add-generic-password`.
const keychainServiceName = "Claude Code-credentials"

// keychainPayload mirrors the JSON structure written to the macOS
// keychain by `claude` interactive login. Only the fields we read or
// rewrite are typed; everything else is preserved verbatim via the
// rawClaudeAI map so unrelated keys (mcpOAuth, additional oauth
// metadata) round-trip untouched.
type keychainPayload struct {
	ClaudeAIOauth claudeAIOauth          `json:"claudeAiOauth"`
	Extras        map[string]interface{} `json:"-"`
}

type claudeAIOauth struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken,omitempty"`
	ExpiresAt        int64    `json:"expiresAt,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	SubscriptionType string   `json:"subscriptionType,omitempty"`
	RateLimitTier    string   `json:"rateLimitTier,omitempty"`
}

// refreshResponse is the JSON body returned by the OAuth token endpoint.
// Anthropic's response includes additional metadata (token_type, scope)
// that we don't need — only access_token, refresh_token, and expires_in
// are load-bearing for keychain rewrite.
type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// keychainAccessor abstracts the macOS keychain so tests can inject a
// fake. Production code uses the macOSKeychain implementation which
// shells out to `security`. Read returns the raw JSON payload; Write
// replaces the entry atomically (delete-then-add to avoid the `-U`
// flag's edge cases on stale entries).
type keychainAccessor interface {
	Read(account string) (string, error)
	Write(account, payload string) error
}

// httpDoer is an http.Client subset; tests substitute httptest.Server's
// client so the real refresh endpoint is never contacted.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// resolver is the helper's main work unit. main() builds one with
// production deps; tests build one with fakes.
type resolver struct {
	keychain   keychainAccessor
	httpClient httpDoer
	oauthURL   string
	now        func() time.Time
	stderr     io.Writer
}

func main() {
	r := &resolver{
		keychain:   macOSKeychain{},
		httpClient: &http.Client{Timeout: refreshTimeout},
		oauthURL:   resolveOAuthURL(),
		now:        time.Now,
		stderr:     os.Stderr,
	}
	tok, err := r.resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "torque-apikey-helper: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(tok)
}

// resolveOAuthURL honors TORQUE_APIKEY_OAUTH_URL for tests; falls
// back to the production endpoint. Trimmed so a stray newline in the
// env doesn't break URL parsing downstream.
func resolveOAuthURL() string {
	if u := strings.TrimSpace(os.Getenv("TORQUE_APIKEY_OAUTH_URL")); u != "" {
		return u
	}
	return defaultOAuthTokenURL
}

func (r *resolver) resolve() (string, error) {
	if tok := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); tok != "" {
		return tok, nil
	}
	if tok := strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")); tok != "" {
		return tok, nil
	}
	return r.resolveFromKeychain()
}

// resolveFromKeychain extracts the OAuth access token from the macOS
// keychain entry written by `claude` interactive login. Returns an error
// (not exit) so main() can format the stderr message uniformly.
func (r *resolver) resolveFromKeychain() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf(
			"keychain fallback only available on macOS (GOOS=%s); "+
				"set ANTHROPIC_API_KEY in env or run `claude setup-token` to generate one",
			runtime.GOOS)
	}

	user := os.Getenv("USER")
	if user == "" {
		// LOGNAME is the POSIX-portable spelling; some daemon contexts (e.g.
		// launchd plists that don't propagate USER) still set LOGNAME.
		user = os.Getenv("LOGNAME")
	}
	if user == "" {
		return "", fmt.Errorf(
			"USER and LOGNAME both empty; cannot key the keychain lookup. " +
				"Set USER in the daemon env or set ANTHROPIC_API_KEY directly")
	}

	raw, err := r.keychain.Read(user)
	if err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("keychain entry is empty (account=%s)", user)
	}

	payload, err := parseKeychainPayload(raw)
	if err != nil {
		return "", fmt.Errorf(
			"parse keychain payload (account=%s): %w; "+
				"the entry may be from an older claude version with a different shape",
			user, err)
	}

	tok := strings.TrimSpace(payload.ClaudeAIOauth.AccessToken)
	if tok == "" {
		return "", fmt.Errorf(
			"keychain entry has no claudeAiOauth.accessToken (account=%s); "+
				"the keychain may be in a broken state — sign in again via `claude`",
			user)
	}

	// Decide whether refresh is needed. Conservative checks:
	//   - expiresAt <= 0 means the field is absent (older claude versions);
	//     skip refresh, return existing token unchanged.
	//   - refreshToken empty means we can't refresh even if we wanted;
	//     warn and return existing.
	//   - now+margin >= expiresAt means refresh.
	expMS := payload.ClaudeAIOauth.ExpiresAt
	rt := strings.TrimSpace(payload.ClaudeAIOauth.RefreshToken)
	if expMS <= 0 {
		return tok, nil
	}
	nowMS := r.now().UnixMilli()
	if nowMS+refreshMarginMs < expMS {
		// Token healthy; no refresh needed.
		return tok, nil
	}
	if rt == "" {
		fmt.Fprintf(r.stderr,
			"torque-apikey-helper: token near/past expiry but no refreshToken in keychain; "+
				"returning existing token. Re-login via `claude` to populate refreshToken.\n")
		return tok, nil
	}

	newTok, err := r.refreshAndPersist(user, raw, payload, rt)
	if err != nil {
		// Log explicitly and fall back to the existing token. An expired
		// token getting a 401 is recoverable (user re-logs in); a hard
		// helper failure breaks every spawned session.
		fmt.Fprintf(r.stderr,
			"torque-apikey-helper: oauth refresh failed (%v); returning existing token. "+
				"If the API responds 401, run `claude` interactively to refresh.\n", err)
		return tok, nil
	}
	return newTok, nil
}

// refreshAndPersist POSTs to the OAuth endpoint, rewrites the keychain
// entry with the rotated tokens, and returns the new access token.
// The original raw JSON is preserved with claudeAiOauth.{accessToken,
// refreshToken, expiresAt, scopes} replaced; all other fields
// (mcpOAuth, future additions) round-trip untouched.
func (r *resolver) refreshAndPersist(user, raw string, payload keychainPayload, refreshToken string) (string, error) {
	scope := strings.Join(payload.ClaudeAIOauth.Scopes, " ")
	if strings.TrimSpace(scope) == "" {
		// Default scope set claude uses when not given one; keeps the
		// refresh request shape valid even for older keychain entries
		// that didn't store scopes.
		scope = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	}

	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     claudeCodeClientID,
		"scope":         scope,
	})
	if err != nil {
		return "", fmt.Errorf("marshal refresh body: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.oauthURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", anthropicBetaOAuth)
	req.Header.Set("User-Agent", "torque-apikey-helper/1")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth POST %s: %w", r.oauthURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Cap the body we slurp so an HTML error page doesn't blow up
		// the stderr log.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("oauth refresh HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var rr refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return "", fmt.Errorf("decode refresh response: %w", err)
	}
	newAccess := strings.TrimSpace(rr.AccessToken)
	if newAccess == "" {
		return "", fmt.Errorf("refresh response missing access_token")
	}
	if rr.ExpiresIn <= 0 {
		return "", fmt.Errorf("refresh response missing/invalid expires_in (%d)", rr.ExpiresIn)
	}
	// Refresh tokens often rotate; if the server didn't return a new one,
	// preserve the previous one (matches claude's own behavior — see
	// the binary's `{access_token:z, refresh_token:$=H, ...}` default).
	newRefresh := strings.TrimSpace(rr.RefreshToken)
	if newRefresh == "" {
		newRefresh = refreshToken
	}
	newScopes := payload.ClaudeAIOauth.Scopes
	if s := strings.TrimSpace(rr.Scope); s != "" {
		newScopes = strings.Fields(s)
	}

	newExpiresAt := r.now().UnixMilli() + rr.ExpiresIn*1000

	updated, err := rewriteKeychainPayload(raw, newAccess, newRefresh, newExpiresAt, newScopes)
	if err != nil {
		return "", fmt.Errorf("rewrite keychain payload: %w", err)
	}

	if err := r.keychain.Write(user, updated); err != nil {
		// Rewrite failed — we still have a valid new access token in
		// memory, but the next invocation will re-refresh because the
		// keychain still has the old expiry. Surface the warning but
		// return the new token so the current dispatch succeeds.
		fmt.Fprintf(r.stderr,
			"torque-apikey-helper: keychain write-back failed after refresh (%v); "+
				"returning fresh token but next invocation will re-refresh.\n", err)
	}
	return newAccess, nil
}

// parseKeychainPayload decodes the typed fields and stashes any unknown
// top-level keys in Extras so we can round-trip them on rewrite.
func parseKeychainPayload(raw string) (keychainPayload, error) {
	var p keychainPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return p, err
	}
	// Capture extras (e.g. mcpOAuth) so rewrite can preserve them.
	var extras map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &extras); err != nil {
		return p, err
	}
	delete(extras, "claudeAiOauth")
	p.Extras = extras
	return p, nil
}

// rewriteKeychainPayload produces a new JSON payload with claudeAiOauth
// fields updated and all other top-level keys preserved verbatim.
// Operates on the raw JSON via a generic map so unknown fields inside
// claudeAiOauth (e.g. subscriptionType, rateLimitTier) survive untouched.
func rewriteKeychainPayload(raw, accessToken, refreshToken string, expiresAt int64, scopes []string) (string, error) {
	var top map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return "", err
	}
	cao, _ := top["claudeAiOauth"].(map[string]interface{})
	if cao == nil {
		cao = map[string]interface{}{}
	}
	cao["accessToken"] = accessToken
	cao["refreshToken"] = refreshToken
	cao["expiresAt"] = expiresAt
	if len(scopes) > 0 {
		// JSON-friendly slice — marshaling []string would also work,
		// but going through []interface{} keeps the map round-trip
		// numeric-stable.
		s := make([]interface{}, len(scopes))
		for i, v := range scopes {
			s[i] = v
		}
		cao["scopes"] = s
	}
	top["claudeAiOauth"] = cao
	out, err := json.Marshal(top)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// macOSKeychain is the production keychainAccessor implementation,
// shelling out to the `security` binary.
type macOSKeychain struct{}

func (macOSKeychain) Read(account string) (string, error) {
	cmd := exec.Command("security",
		"find-generic-password",
		"-s", keychainServiceName,
		"-a", account,
		"-w",
	)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	out, err := cmd.Output()
	if err != nil {
		stderrText := strings.TrimSpace(stderrBuf.String())
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf(
				"`security` binary not on PATH: %w; this helper relies on "+
					"macOS keychain extraction. Set ANTHROPIC_API_KEY in env, "+
					"or ensure /usr/bin is on the daemon's PATH",
				err)
		}
		if stderrText != "" {
			return "", fmt.Errorf(
				"security find-generic-password (%s, account=%s): %w (stderr: %s); "+
					"set ANTHROPIC_API_KEY in env, run `claude setup-token`, or "+
					"sign in via `claude` interactively first",
				keychainServiceName, account, err, stderrText)
		}
		return "", fmt.Errorf(
			"security find-generic-password (%s, account=%s): %w; "+
				"set ANTHROPIC_API_KEY in env, run `claude setup-token`, or "+
				"sign in via `claude` interactively first",
			keychainServiceName, account, err)
	}
	return string(out), nil
}

func (macOSKeychain) Write(account, payload string) error {
	// `security add-generic-password -U` updates in place when the entry
	// exists, but historical reports show edge cases where ACLs end up
	// stripped. Delete-then-add is the documented-safe path; failure of
	// delete (NotFound) is benign because add will create the entry.
	delCmd := exec.Command("security",
		"delete-generic-password",
		"-s", keychainServiceName,
		"-a", account,
	)
	// Ignore delete errors — entry may not exist or may already be in
	// the desired state; the subsequent add covers both paths.
	_ = delCmd.Run()

	addCmd := exec.Command("security",
		"add-generic-password",
		"-s", keychainServiceName,
		"-a", account,
		"-w", payload,
		"-U",
	)
	var stderrBuf bytes.Buffer
	addCmd.Stderr = &stderrBuf
	if err := addCmd.Run(); err != nil {
		return fmt.Errorf("security add-generic-password: %w (stderr: %s)",
			err, strings.TrimSpace(stderrBuf.String()))
	}
	return nil
}

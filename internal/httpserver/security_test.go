package httpserver_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSecuredServer(t *testing.T, sec httpserver.Security) *httptest.Server {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	handler := httpserver.New(service.New(store), nil).WithSecurity(sec)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	require.NoError(t, err)
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestDefaultAddrIsLoopback(t *testing.T) {
	assert.Equal(t, "127.0.0.1:8990", httpserver.DefaultAddr(8990))
	require.NoError(t, httpserver.ValidateBind(httpserver.DefaultAddr(8990), ""))
}

func TestValidateBind(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		token   string
		wantErr bool
	}{
		{"ipv4 loopback", "127.0.0.1:8990", "", false},
		{"ipv6 loopback", "[::1]:8990", "", false},
		{"localhost", "localhost:8990", "", false},
		{"empty host is all interfaces", ":8990", "", true},
		{"all interfaces without token", "0.0.0.0:8990", "", true},
		{"lan address without token", "192.168.1.10:8990", "", true},
		{"all interfaces with token", "0.0.0.0:8990", "t", false},
		{"empty host with token", ":8990", "t", false},
		{"malformed", "8990", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := httpserver.ValidateBind(tc.addr, tc.token)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateBind(%q, %q) err = %v, wantErr %v", tc.addr, tc.token, err, tc.wantErr)
			}
		})
	}
}

func TestNoToken_LoopbackClientNeedsNoAuth(t *testing.T) {
	ts := setupSecuredServer(t, httpserver.Security{})
	resp := do(t, http.MethodGet, ts.URL+"/api/v1/tasks", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestNoToken_NonLoopbackHostRefused(t *testing.T) {
	ts := setupSecuredServer(t, httpserver.Security{})
	resp := do(t, http.MethodGet, ts.URL+"/api/v1/tasks", map[string]string{"Host": "rebind.example:8990"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestToken_RequiredAndChecked(t *testing.T) {
	ts := setupSecuredServer(t, httpserver.Security{Token: "secret"})
	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "Bearer nope", http.StatusUnauthorized},
		{"wrong scheme", "Basic secret", http.StatusUnauthorized},
		{"correct", "Bearer secret", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := map[string]string{}
			if tc.header != "" {
				h["Authorization"] = tc.header
			}
			resp := do(t, http.MethodGet, ts.URL+"/api/v1/tasks", h)
			assert.Equal(t, tc.want, resp.StatusCode)
			if tc.want == http.StatusUnauthorized {
				assert.Contains(t, resp.Header.Get("WWW-Authenticate"), "Bearer")
			}
		})
	}
}

func TestToken_AppliesToLoopbackAndAnyHost(t *testing.T) {
	ts := setupSecuredServer(t, httpserver.Security{Token: "secret"})
	// A token-protected server may sit behind a proxy with a real hostname.
	resp := do(t, http.MethodGet, ts.URL+"/api/v1/tasks", map[string]string{
		"Host": "torque.example", "Authorization": "Bearer secret",
	})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp = do(t, http.MethodGet, ts.URL+"/api/v1/tasks", nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "loopback callers need the token once one is set")
}

func TestToken_PreflightPassesWithoutToken(t *testing.T) {
	ts := setupSecuredServer(t, httpserver.Security{Token: "secret"})
	resp := do(t, http.MethodOptions, ts.URL+"/api/v1/tasks", map[string]string{
		"Origin": "http://localhost:5182", "Access-Control-Request-Method": "POST",
	})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "http://localhost:5182", resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestCORS_Restricted(t *testing.T) {
	ts := setupSecuredServer(t, httpserver.Security{CORSOrigins: []string{"https://gui.example"}})
	for _, tc := range []struct {
		origin  string
		allowed bool
	}{
		{"http://localhost:5182", true},
		{"http://127.0.0.1:8990", true},
		{"http://[::1]:5185", true},
		{"https://gui.example", true},
		{"https://evil.example", false},
		{"http://localhost.evil.example", false},
		{"null", false},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			resp := do(t, http.MethodPost, ts.URL+"/api/v1/tasks", map[string]string{"Origin": tc.origin})
			got := resp.Header.Get("Access-Control-Allow-Origin")
			if tc.allowed {
				assert.Equal(t, tc.origin, got)
				assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)
				return
			}
			assert.Empty(t, got)
			// Refused outright, not just left without CORS headers: a
			// cross-site form POST never needs to read the response.
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
}

func TestCORS_NoWildcard(t *testing.T) {
	ts := setupSecuredServer(t, httpserver.Security{})
	resp := do(t, http.MethodGet, ts.URL+"/api/v1/tasks", map[string]string{"Origin": "http://localhost:5182"})
	assert.NotEqual(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
	resp = do(t, http.MethodGet, ts.URL+"/api/v1/tasks", nil)
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
}

func newGuardServer(t *testing.T, mode auth.Mode, token string) *httptest.Server {
	t.Helper()
	g, err := httpserver.NewGuard(context.Background(), httpserver.AuthConfig{Mode: mode, StaticToken: token})
	require.NoError(t, err)
	return setupSecuredServer(t, httpserver.Security{Token: token, Guard: g})
}

// Default (off) mode must leave the legacy gate exactly as it was.
func TestGuardOff_LegacyBehaviourUnchanged(t *testing.T) {
	ts := newGuardServer(t, auth.ModeOff, "s3cret")
	assert.Equal(t, http.StatusUnauthorized, do(t, "GET", ts.URL+"/api/v1/tasks", nil).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, do(t, "GET", ts.URL+"/api/v1/tasks", map[string]string{"Authorization": "Bearer nope"}).StatusCode)
	assert.Equal(t, http.StatusOK, do(t, "GET", ts.URL+"/api/v1/tasks", map[string]string{"Authorization": "Bearer s3cret"}).StatusCode)
}

func TestGuardOff_NoTokenStaysLoopbackOnly(t *testing.T) {
	ts := newGuardServer(t, auth.ModeOff, "")
	assert.Equal(t, http.StatusOK, do(t, "GET", ts.URL+"/api/v1/tasks", nil).StatusCode)
	assert.Equal(t, http.StatusForbidden, do(t, "GET", ts.URL+"/api/v1/tasks", map[string]string{"Host": "evil.example"}).StatusCode)
}

func TestGuardEnforce_StaticTokenStillWorksAndAnonymousIsRejected(t *testing.T) {
	ts := newGuardServer(t, auth.ModeEnforce, "s3cret")
	assert.Equal(t, http.StatusUnauthorized, do(t, "GET", ts.URL+"/api/v1/tasks", nil).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, do(t, "GET", ts.URL+"/api/v1/tasks", map[string]string{"Authorization": "Bearer nope"}).StatusCode)
	assert.Equal(t, http.StatusOK, do(t, "GET", ts.URL+"/api/v1/tasks", map[string]string{"Authorization": "Bearer s3cret"}).StatusCode)
}

func TestGuardEnforce_NoTokenConfiguredRejectsAnonymous(t *testing.T) {
	ts := newGuardServer(t, auth.ModeEnforce, "")
	assert.Equal(t, http.StatusUnauthorized, do(t, "GET", ts.URL+"/api/v1/tasks", nil).StatusCode)
}

func TestGuardJWT_VerifiedTokenAdmittedUnderLegacyTokenAndEnforce(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	keyFile := filepath.Join(t.TempDir(), "issuer.pub.pem")
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))
	mint := func(aud, scope string) string {
		enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
		in := enc(map[string]any{"alg": "EdDSA", "typ": "JWT"}) + "." + enc(map[string]any{
			"iss": "https://tether.test", "sub": "agent/x", "aud": aud, "scope": scope, "kind": "agent",
			"exp": time.Now().Add(time.Minute).Unix(), "jti": "j1"})
		return in + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(in)))
	}
	for _, mode := range []auth.Mode{auth.ModeOff, auth.ModeEnforce} {
		g, err := httpserver.NewGuard(context.Background(), httpserver.AuthConfig{
			Mode: mode, Issuer: "https://tether.test", BootstrapKeyFile: keyFile, StaticToken: "s3cret"})
		require.NoError(t, err)
		ts := setupSecuredServer(t, httpserver.Security{Token: "s3cret", Guard: g})
		bearer := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }
		assert.Equal(t, http.StatusOK, do(t, "GET", ts.URL+"/api/v1/tasks", bearer(mint("torque", "torque.read"))).StatusCode, mode)
		assert.Equal(t, http.StatusUnauthorized, do(t, "GET", ts.URL+"/api/v1/tasks", bearer(mint("tesseract", "torque.read"))).StatusCode, mode)
		if mode == auth.ModeOff {
			// Under-scoped JWT must not bypass the legacy token gate.
			assert.Equal(t, http.StatusUnauthorized, do(t, "POST", ts.URL+"/api/v1/tasks", bearer(mint("torque", "torque.read"))).StatusCode)
		}
		if mode == auth.ModeEnforce {
			assert.Equal(t, http.StatusForbidden, do(t, "POST", ts.URL+"/api/v1/tasks", bearer(mint("torque", "torque.read"))).StatusCode)
		}
	}
}

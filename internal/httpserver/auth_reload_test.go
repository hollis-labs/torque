package httpserver_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/httpserver"
)

// Auth reloads (security/auth v0.2.1): the mode and the JWT / deny-list
// settings change while `torque serve` runs; a bad reload changes nothing; a
// reload never forgets a revocation.

func bearerH(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

func listTasks(t *testing.T, ts *httptest.Server, tok string) int {
	t.Helper()
	h := map[string]string{}
	if tok != "" {
		h = bearerH(tok)
	}
	return do(t, "GET", ts.URL+"/api/v1/tasks", h).StatusCode
}

func newAuthServer(t *testing.T, c httpserver.AuthConfig) (*httpserver.Auth, *httptest.Server) {
	t.Helper()
	a, err := httpserver.NewAuth(guardContext(t), c)
	require.NoError(t, err)
	return a, setupSecuredServer(t, httpserver.Security{Token: c.StaticToken, Guard: a.Guard})
}

func (i *jwtIssuer) cfg(mode auth.Mode) httpserver.AuthConfig {
	return httpserver.AuthConfig{Mode: mode, BootstrapKeyFile: i.keyFile, StaticToken: "s3cret"}
}

func TestAuthReloadRotatesTheIssuerKeyAndKeepsTheStaticToken(t *testing.T) {
	a1, a2 := newJWTIssuer(t), newJWTIssuer(t)
	auth1, ts := newAuthServer(t, a1.cfg(auth.ModeEnforce))
	tok1 := a1.serviceToken("torque.read", "svc-1")
	tok2 := a2.serviceToken("torque.read", "svc-2")
	assert.Equal(t, http.StatusOK, listTasks(t, ts, tok1))
	assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, tok2))

	require.NoError(t, auth1.Reload(a2.cfg(auth.ModeEnforce)))
	assert.Equal(t, http.StatusOK, listTasks(t, ts, tok2), "the new key")
	assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, tok1), "the replaced key")
	assert.Equal(t, http.StatusOK, listTasks(t, ts, "s3cret"), "the startup token survives a reload")
}

func TestAuthReloadMovesTheModeLive(t *testing.T) {
	a1 := newJWTIssuer(t)
	cfg := a1.cfg(auth.ModeOff)
	cfg.StaticToken = ""
	auth1, ts := newAuthServer(t, cfg)
	assert.Equal(t, http.StatusOK, listTasks(t, ts, ""), "off + loopback, no token")
	cfg.Mode = auth.ModeEnforce
	require.NoError(t, auth1.Reload(cfg))
	assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, ""), "enforce after the reload")
	assert.Equal(t, auth.ModeEnforce, auth1.Guard.Mode())
	cfg.Mode = auth.ModePermissive
	require.NoError(t, auth1.Reload(cfg))
	assert.Equal(t, http.StatusOK, listTasks(t, ts, ""), "permissive after relaxing")
}

func TestAuthFailedReloadChangesNothing(t *testing.T) {
	a1 := newJWTIssuer(t)
	auth1, ts := newAuthServer(t, a1.cfg(auth.ModeEnforce))
	tok := a1.serviceToken("torque.read", "svc-1")
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	good := a1.cfg(auth.ModeEnforce)
	for name, cfg := range map[string]httpserver.AuthConfig{
		"unknown mode":            {Mode: "enforcing", BootstrapKeyFile: a1.keyFile},
		"unreadable bootstrap":    {Mode: auth.ModeEnforce, BootstrapKeyFile: filepath.Join(t.TempDir(), "absent.pem")},
		"jwks only, unreachable":  {Mode: auth.ModeEnforce, JWKSURL: dead.URL + "/.well-known/jwks.json", Issuer: "x"},
		"deny-list not reachable": {Mode: auth.ModeEnforce, BootstrapKeyFile: good.BootstrapKeyFile, DenyListURL: dead.URL + "/auth/denylist"},
	} {
		err := auth1.Reload(cfg)
		require.Error(t, err, name)
		assert.NotContains(t, err.Error(), dead.URL, "%s: the error must not carry the endpoint URL", name)
		assert.Equal(t, http.StatusOK, listTasks(t, ts, tok), "%s: the running configuration changed", name)
		assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, ""), "%s: enforce was lost", name)
	}
}

type denyServer struct {
	srv    *httptest.Server
	status atomic.Int32
	jti    string
}

func newDeny(t *testing.T, jti string) *denyServer {
	d := &denyServer{jti: jti}
	d.status.Store(200)
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if code := int(d.status.Load()); code != 200 {
			http.Error(w, "down", code)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jti":[%q],"sub":[],"sid":[]}`, d.jti)
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func TestAuthReloadDuringADenyListOutageKeepsKnownRevocations(t *testing.T) {
	i := newJWTIssuer(t)
	d := newDeny(t, "svc-revoked")
	cfg := i.cfg(auth.ModeEnforce)
	cfg.DenyListURL, cfg.DenyListInterval = d.srv.URL, 25*time.Millisecond
	a, ts := newAuthServer(t, cfg)
	revoked := i.serviceToken("torque.read", "svc-revoked")
	fine := i.serviceToken("torque.read", "svc-fine")
	require.Eventually(t, func() bool { return listTasks(t, ts, revoked) == http.StatusUnauthorized },
		5*time.Second, 20*time.Millisecond, "the revocation to be learned")

	d.status.Store(503)
	for n := 0; n < 3; n++ {
		require.NoError(t, a.Reload(cfg), "identical reload %d during the outage", n)
		assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, revoked), "revoked token after reload %d", n)
		assert.Equal(t, http.StatusOK, listTasks(t, ts, fine), "unrevoked token after reload %d", n)
	}
}

func TestAuthReloadToANewDenyListEndpointNeedsAValidatedFirstFetch(t *testing.T) {
	i := newJWTIssuer(t)
	oldD := newDeny(t, "svc-revoked")
	cfg := i.cfg(auth.ModeEnforce)
	cfg.DenyListURL, cfg.DenyListInterval = oldD.srv.URL, 25*time.Millisecond
	a, ts := newAuthServer(t, cfg)
	revoked := i.serviceToken("torque.read", "svc-revoked")
	require.Eventually(t, func() bool { return listTasks(t, ts, revoked) == http.StatusUnauthorized },
		5*time.Second, 20*time.Millisecond)

	down := newDeny(t, "svc-other")
	down.status.Store(503)
	bad := cfg
	bad.DenyListURL = down.srv.URL
	err := a.Reload(bad)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), down.srv.URL)
	assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, revoked), "the old revocation after the refused reload")

	up := newDeny(t, "svc-other")
	good := cfg
	good.DenyListURL = up.srv.URL
	require.NoError(t, a.Reload(good))
	assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, i.serviceToken("torque.read", "svc-other")), "revoked by the new endpoint")
	assert.Equal(t, http.StatusOK, listTasks(t, ts, revoked), "revoked only by the replaced endpoint")
}

func TestAuthReloadStopsTheLoopsOfTheGenerationItReplaces(t *testing.T) {
	i := newJWTIssuer(t)
	d := newDeny(t, "x")
	cfg := i.cfg(auth.ModeEnforce)
	cfg.DenyListURL = d.srv.URL
	a, _ := newAuthServer(t, cfg)
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()
	for n := 0; n < 25; n++ {
		require.NoError(t, a.Reload(cfg))
	}
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+8 },
		5*time.Second, 20*time.Millisecond, "reloads must not accumulate refresh or poll goroutines")
}

func TestAuthFileIsWatchedAndABadEditIsIgnored(t *testing.T) {
	i := newJWTIssuer(t)
	path := filepath.Join(t.TempDir(), httpserver.AuthFileName)
	write := func(body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	write("mode: permissive\njwt:\n  bootstrap_key_file: " + i.keyFile + "\n")
	cfg, exists, err := httpserver.LoadAuthFile(path)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, auth.ModePermissive, cfg.Mode)
	a, ts := newAuthServer(t, cfg)
	r := a.Reloader(path, 10*time.Millisecond, nil)
	r.NoSignal = true
	ctx := guardContext(t)
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	time.Sleep(60 * time.Millisecond)

	write("mode: enforce\njwt:\n  bootstrap_key_file: " + i.keyFile + "\n")
	require.Eventually(t, func() bool { return listTasks(t, ts, "") == http.StatusUnauthorized },
		5*time.Second, 20*time.Millisecond, "the edit to enforce")
	write("mode: [not yaml for this schema\n")
	write("mode: enforce\nbogus_key: 1\n") // unknown key
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, auth.ModeEnforce, a.Guard.Mode(), "a malformed or unknown-key edit changed the mode")
	write("mode: permissive\njwt:\n  bootstrap_key_file: " + i.keyFile + "\n")
	require.Eventually(t, func() bool { return listTasks(t, ts, "") == http.StatusOK }, 5*time.Second, 20*time.Millisecond, "a valid edit back to permissive")
}

func TestLoadAuthFile(t *testing.T) {
	dir := t.TempDir()
	_, exists, err := httpserver.LoadAuthFile(filepath.Join(dir, "absent.yaml"))
	require.NoError(t, err)
	assert.False(t, exists)

	empty := filepath.Join(dir, "empty.yaml")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	cfg, exists, err := httpserver.LoadAuthFile(empty)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, httpserver.AuthConfig{}, cfg)

	bad := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("mode: enforce\nissuer: typo-at-top-level\n"), 0o600))
	_, _, err = httpserver.LoadAuthFile(bad)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "issuer"), "the error names the unknown key: %v", err)
}

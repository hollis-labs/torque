package bootstrap_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
)

type bearerRT struct{ tok string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

// mcpScopeFixture serves /mcp behind the real guard in the given mode and
// returns a minter for Tether-style JWTs plus the audit events seen.
func mcpScopeFixture(t *testing.T, mode auth.Mode) (url string, mint func(scope string) string, events func() []auth.Event) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	keyFile := filepath.Join(t.TempDir(), "issuer.pub.pem")
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))
	var mu sync.Mutex
	var evs []auth.Event
	guard, err := httpserver.NewGuard(context.Background(), httpserver.AuthConfig{
		Mode: mode, Issuer: "https://tether.test", BootstrapKeyFile: keyFile, StaticToken: "s3cret",
		Audit: func(e auth.Event) { mu.Lock(); evs = append(evs, e); mu.Unlock() },
	})
	require.NoError(t, err)
	store := sqlitetest.OpenStore(t)
	svc := service.New(store)
	srv := httptest.NewServer(httpserver.New(svc, nil).
		WithSecurity(httpserver.Security{Token: "s3cret", Guard: guard}).
		WithMCP(bootstrap.DaemonMCPHandler(svc, nil, nil, nil, guard)))
	t.Cleanup(srv.Close)
	mint = func(scope string) string {
		enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
		in := enc(map[string]any{"alg": "EdDSA", "typ": "JWT"}) + "." + enc(map[string]any{
			"iss": "https://tether.test", "sub": "agent/x", "aud": "torque", "scope": scope, "kind": "agent",
			"exp": time.Now().Add(time.Minute).Unix(), "jti": "j1"})
		return in + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(in)))
	}
	events = func() []auth.Event { mu.Lock(); defer mu.Unlock(); return append([]auth.Event(nil), evs...) }
	return srv.URL + "/mcp", mint, events
}

// call returns whether the call was refused (at the transport or by tool
// scope), as opposed to reaching the tool.
func call(t *testing.T, url, tok, tool string, args map[string]any) (refused bool) {
	t.Helper()
	ctx := context.Background()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx,
		&mcpsdk.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: bearerRT{tok}}}, nil)
	if err != nil {
		return true // refused at the transport (401)
	}
	defer cs.Close()
	_, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: tool, Arguments: args})
	return err != nil
}

func TestMCPToolScopes_Enforce(t *testing.T) {
	url, mint, _ := mcpScopeFixture(t, auth.ModeEnforce)
	read, write := mint("torque.read"), mint("torque.write torque.read")
	assert.False(t, call(t, url, read, "torque_task_list", nil), "read scope lists tasks")
	assert.True(t, call(t, url, read, "torque_task_create", map[string]any{"title": "x"}), "read scope must not create")
	assert.True(t, call(t, url, read, "torque_task_delete", map[string]any{"id": "CW-1"}), "read scope must not delete")
	assert.False(t, call(t, url, write, "torque_task_create", map[string]any{"title": "x"}), "write scope creates")
	assert.True(t, call(t, url, write, "torque_settings_save", map[string]any{}), "write scope must not change settings")
	assert.False(t, call(t, url, "s3cret", "torque_settings_get", nil), "static token keeps torque.*")
}

func TestMCPToolScopes_PermissiveLogsWouldDeny(t *testing.T) {
	url, mint, events := mcpScopeFixture(t, auth.ModePermissive)
	assert.False(t, call(t, url, mint("torque.write"), "torque_settings_save", map[string]any{}), "permissive never refuses a caller admitted to /mcp")
	assert.False(t, call(t, url, mint("torque.write"), "torque_task_create", map[string]any{"title": "x"}), "permissive never refuses")
	var found bool
	for _, e := range events() {
		if e.Tool == "torque_settings_save" {
			found = true
			assert.True(t, e.WouldDeny)
			assert.Equal(t, "torque.admin", e.RequiredScope)
			assert.False(t, e.Denied)
		}
	}
	assert.True(t, found, "no audit event for the tool call")
}

func TestMCPToolScopes_OffChangesNothing(t *testing.T) {
	url, mint, _ := mcpScopeFixture(t, auth.ModeOff)
	assert.False(t, call(t, url, "s3cret", "torque_settings_get", nil))
	// A read-scoped JWT must not reach /mcp at all while auth is off: the
	// legacy token gate decides, as before per-tool scopes.
	assert.True(t, call(t, url, mint("torque.read"), "torque_task_list", nil))
	assert.True(t, call(t, url, mint("torque.read"), "torque_settings_save", map[string]any{}))
	// Write-scoped JWTs keep the old /mcp access.
	assert.False(t, call(t, url, mint("torque.write"), "torque_task_list", nil))
}

package httpserver_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/httpserver"
)

// Reload posture: the listener bounds what a reload may relax, a relaxation is
// logged, auth.yaml must not be writable by others, and file / environment
// precedence is fixed.

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestAuthReloadMayNotRelaxOnANonLoopbackListener(t *testing.T) {
	i := newJWTIssuer(t)
	cfg := i.cfg(auth.ModeEnforce)
	cfg.Addr = "0.0.0.0:7777"
	a, ts := newAuthServer(t, cfg)
	tok := i.serviceToken("torque.read", "svc-1")

	for _, to := range []auth.Mode{auth.ModePermissive, auth.ModeOff} {
		c := i.cfg(to)
		c.Addr = "0.0.0.0:7777"
		err := a.Reload(c)
		require.Error(t, err, "relaxing to %s on 0.0.0.0", to)
		assert.Contains(t, err.Error(), "non-loopback")
		assert.Equal(t, auth.ModeEnforce, a.Guard.Mode())
		assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, ""), "no token is still refused after the refused reload")
		assert.Equal(t, http.StatusOK, listTasks(t, ts, tok), "the JWT verifier is unchanged")
		assert.Equal(t, http.StatusOK, listTasks(t, ts, "s3cret"), "the static token gate is unchanged")
	}
	// Staying at enforce (a key rotation, say) is not a relaxation.
	require.NoError(t, a.Reload(i.cfg(auth.ModeEnforce)))
}

func TestAuthReloadMayStrengthenOnANonLoopbackListener(t *testing.T) {
	i := newJWTIssuer(t)
	cfg := i.cfg(auth.ModePermissive) // the legacy token-gated non-loopback start
	cfg.Addr = "0.0.0.0:7777"
	a, _ := newAuthServer(t, cfg)
	require.NoError(t, a.Reload(i.cfg(auth.ModeEnforce)))
	assert.Equal(t, auth.ModeEnforce, a.Guard.Mode())
	assert.Error(t, a.Reload(i.cfg(auth.ModePermissive)), "and back down is refused")
}

func TestAuthReloadRelaxationOnLoopbackIsAllowedAndLogged(t *testing.T) {
	i := newJWTIssuer(t)
	logs := &syncBuf{}
	cfg := i.cfg(auth.ModeEnforce)
	cfg.Addr, cfg.StaticToken = "127.0.0.1:7777", "" // no legacy token gate, so permissive admits anonymous reads
	cfg.Logger = slog.New(slog.NewTextHandler(logs, nil))
	a, ts := newAuthServer(t, cfg)

	c := i.cfg(auth.ModePermissive)
	c.Addr = cfg.Addr
	require.NoError(t, a.Reload(c))
	assert.Equal(t, http.StatusOK, listTasks(t, ts, ""), "permissive on loopback")
	out := logs.String()
	assert.Contains(t, out, "level=WARN")
	assert.Contains(t, out, "auth mode changed")
	assert.Contains(t, out, "from=enforce")
	assert.Contains(t, out, "to=permissive")
}

func TestLoadAuthFileRefusesFilesOthersCanWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), httpserver.AuthFileName)
	for _, perm := range []os.FileMode{0o664, 0o666, 0o620, 0o602} {
		require.NoError(t, os.WriteFile(path, []byte("mode: off\n"), 0o600))
		require.NoError(t, os.Chmod(path, perm))
		_, exists, err := httpserver.LoadAuthFile(path)
		require.Error(t, err, "%04o", perm)
		assert.True(t, exists)
		assert.Contains(t, err.Error(), httpserver.AuthFileName)
	}
	for _, perm := range []os.FileMode{0o600, 0o640, 0o644, 0o400} {
		require.NoError(t, os.Chmod(path, 0o600))
		require.NoError(t, os.WriteFile(path, []byte("mode: off\n"), 0o600))
		require.NoError(t, os.Chmod(path, perm))
		_, _, err := httpserver.LoadAuthFile(path)
		assert.NoError(t, err, "%04o", perm)
	}
}

func TestAuthReloadKeepsTheLastGoodWhenTheFileBecomesWritable(t *testing.T) {
	i := newJWTIssuer(t)
	path := filepath.Join(t.TempDir(), httpserver.AuthFileName)
	body := "mode: enforce\njwt:\n  bootstrap_key_file: " + i.keyFile + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	start := i.cfg(auth.ModePermissive)
	start.StaticToken = ""
	a, ts := newAuthServer(t, start)
	r := a.Reloader(path, 0, slog.New(slog.NewTextHandler(&syncBuf{}, nil)))

	require.NoError(t, os.Chmod(path, 0o666))
	err := r.Load(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), httpserver.AuthFileName)
	assert.Equal(t, auth.ModePermissive, a.Guard.Mode(), "a group/world-writable file was applied")
	assert.Equal(t, http.StatusOK, listTasks(t, ts, ""))

	require.NoError(t, os.Chmod(path, 0o600))
	require.NoError(t, r.Load(context.Background()))
	assert.Equal(t, auth.ModeEnforce, a.Guard.Mode())
	assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, ""))
}

func writeAuthFile(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, httpserver.AuthFileName), []byte(body), 0o600))
}

func start(t *testing.T, dir string) (*httpserver.Auth, string, error) {
	t.Helper()
	return httpserver.StartAuth(guardContext(t), dir, os.Getenv, "s3cret", "127.0.0.1:7777", slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
}

func TestStartAuthPrecedence(t *testing.T) {
	i := newJWTIssuer(t)
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TORQUE", "")
	t.Setenv("TORQUE_AUTH_BOOTSTRAP_KEY_FILE", "")

	t.Run("no file: environment dial and TORQUE_AUTH_*", func(t *testing.T) {
		t.Setenv("HOLLIS_AUTH_TORQUE", "enforce")
		t.Setenv("TORQUE_AUTH_BOOTSTRAP_KEY_FILE", i.keyFile)
		a, file, err := start(t, t.TempDir())
		require.NoError(t, err)
		assert.Empty(t, file)
		ts := setupSecuredServer(t, httpserver.Security{Token: "s3cret", Guard: a.Guard})
		assert.Equal(t, auth.ModeEnforce, a.Guard.Mode())
		assert.Equal(t, http.StatusOK, listTasks(t, ts, i.serviceToken("torque.read", "svc-1")), "the JWT key from the environment")
	})

	t.Run("file replaces TORQUE_AUTH_* entirely", func(t *testing.T) {
		t.Setenv("TORQUE_AUTH_BOOTSTRAP_KEY_FILE", i.keyFile)
		dir := t.TempDir()
		writeAuthFile(t, dir, "mode: enforce\n") // no jwt section: the variable must not leak in
		a, file, err := start(t, dir)
		require.NoError(t, err)
		assert.NotEmpty(t, file)
		ts := setupSecuredServer(t, httpserver.Security{Token: "s3cret", Guard: a.Guard})
		assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, i.serviceToken("torque.read", "svc-1")), "the environment's key was used although auth.yaml exists")
		assert.Equal(t, http.StatusOK, listTasks(t, ts, "s3cret"))
	})

	t.Run("the file's mode beats the environment dial", func(t *testing.T) {
		t.Setenv("HOLLIS_AUTH_TORQUE", "enforce")
		dir := t.TempDir()
		writeAuthFile(t, dir, "mode: permissive\n")
		a, _, err := start(t, dir)
		require.NoError(t, err)
		assert.Equal(t, auth.ModePermissive, a.Guard.Mode())
	})

	t.Run("absent mode and an empty file resolve explicitly to off, ignoring the dial, and say so", func(t *testing.T) {
		for name, body := range map[string]string{"empty file": "", "no mode key": "jwt:\n  audience: torque\n"} {
			dir := t.TempDir()
			writeAuthFile(t, dir, body)
			t.Setenv("HOLLIS_AUTH", "enforce")
			logs := &syncBuf{}
			a, _, err := httpserver.StartAuth(guardContext(t), dir, os.Getenv, "s3cret", "127.0.0.1:7777", slog.New(slog.NewTextHandler(logs, nil)))
			require.NoError(t, err, name)
			assert.Equal(t, auth.ModeOff, a.Guard.Mode(), "%s: the dial must not apply when auth.yaml exists", name)
			out := logs.String()
			assert.Contains(t, out, "level=WARN", name)
			assert.Contains(t, out, "mode=off", name)
			assert.Contains(t, out, `source="auth.yaml default"`, name)
			assert.Contains(t, out, "ignored_env_dial=enforce", name)
			t.Setenv("HOLLIS_AUTH", "")
			a, _, err = start(t, dir)
			require.NoError(t, err, name)
			assert.Equal(t, auth.ModeOff, a.Guard.Mode(), "%s, dial unset", name)
		}
	})

	t.Run("startup errors from a file name the file", func(t *testing.T) {
		for name, body := range map[string]string{
			"invalid mode":   "mode: sometimes\n",
			"syntax":         "mode: [oops\n",
			"unknown key":    "mode: off\nbogus: 1\n",
			"missing key":    "mode: enforce\njwt:\n  bootstrap_key_file: /nonexistent/issuer.pem\n",
			"enforce no key": "mode: enforce\n", // fine: the static token verifies; see below
		} {
			dir := t.TempDir()
			writeAuthFile(t, dir, body)
			_, _, err := start(t, dir)
			if name == "enforce no key" {
				assert.NoError(t, err)
				continue
			}
			require.Error(t, err, name)
			assert.Contains(t, err.Error(), httpserver.AuthFileName, name)
		}
		dir := t.TempDir()
		writeAuthFile(t, dir, "mode: off\n")
		require.NoError(t, os.Chmod(filepath.Join(dir, httpserver.AuthFileName), 0o666))
		_, _, err := start(t, dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), httpserver.AuthFileName)
	})

	t.Run("an invalid dial without a file is not blamed on a file", func(t *testing.T) {
		t.Setenv("HOLLIS_AUTH_TORQUE", "sometimes")
		_, file, err := start(t, t.TempDir())
		require.Error(t, err)
		assert.Empty(t, file)
		assert.False(t, strings.Contains(err.Error(), httpserver.AuthFileName))
	})
}

func TestModelessFileReloadCannotRelaxANonLoopbackListener(t *testing.T) {
	i := newJWTIssuer(t)
	dir := t.TempDir()
	writeAuthFile(t, dir, "mode: enforce\njwt:\n  bootstrap_key_file: "+i.keyFile+"\n")
	a, _, err := httpserver.StartAuth(guardContext(t), dir, os.Getenv, "s3cret", "0.0.0.0:7777", slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
	require.NoError(t, err)
	require.Equal(t, auth.ModeEnforce, a.Guard.Mode())
	writeAuthFile(t, dir, "") // emptied: resolves to off, which is a relaxation
	r := a.Reloader(filepath.Join(dir, httpserver.AuthFileName), 0, slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
	require.Error(t, r.Load(context.Background()))
	assert.Equal(t, auth.ModeEnforce, a.Guard.Mode())
}

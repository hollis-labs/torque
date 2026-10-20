package httpserver_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
		assert.Contains(t, err.Error(), "not loopback")
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

	t.Run("per key: a file without jwt inherits TORQUE_AUTH_*", func(t *testing.T) {
		t.Setenv("TORQUE_AUTH_BOOTSTRAP_KEY_FILE", i.keyFile)
		dir := t.TempDir()
		writeAuthFile(t, dir, "mode: enforce\n")
		a, file, err := start(t, dir)
		require.NoError(t, err)
		assert.NotEmpty(t, file)
		ts := setupSecuredServer(t, httpserver.Security{Token: "s3cret", Guard: a.Guard})
		assert.Equal(t, http.StatusOK, listTasks(t, ts, i.serviceToken("torque.read", "svc-1")), "the environment's key is inherited for the key the file leaves out")
	})

	t.Run("per key: a key the file sets overrides the environment for that key", func(t *testing.T) {
		other := newJWTIssuer(t)
		t.Setenv("TORQUE_AUTH_BOOTSTRAP_KEY_FILE", i.keyFile)
		dir := t.TempDir()
		writeAuthFile(t, dir, "mode: enforce\njwt:\n  bootstrap_key_file: "+other.keyFile+"\n")
		a, _, err := start(t, dir)
		require.NoError(t, err)
		ts := setupSecuredServer(t, httpserver.Security{Token: "s3cret", Guard: a.Guard})
		assert.Equal(t, http.StatusOK, listTasks(t, ts, other.serviceToken("torque.read", "svc-2")))
		assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, i.serviceToken("torque.read", "svc-1")), "the environment's key won over the file's")
	})

	t.Run("the file's mode beats the environment dial", func(t *testing.T) {
		t.Setenv("HOLLIS_AUTH_TORQUE", "enforce")
		dir := t.TempDir()
		writeAuthFile(t, dir, "mode: permissive\n")
		logs := &syncBuf{}
		a, _, err := httpserver.StartAuth(guardContext(t), dir, os.Getenv, "s3cret", "127.0.0.1:7777", slog.New(slog.NewTextHandler(logs, nil)))
		require.NoError(t, err)
		assert.Equal(t, auth.ModePermissive, a.Guard.Mode())
		assert.Contains(t, logs.String(), "mode=permissive")
		assert.Contains(t, logs.String(), "source=auth.yaml")
	})

	t.Run("an omitted mode or an empty file inherits the dial, else off, and the log says which", func(t *testing.T) {
		for name, body := range map[string]string{"empty file": "", "no mode key": "jwt:\n  audience: torque\n"} {
			dir := t.TempDir()
			writeAuthFile(t, dir, body)
			for dial, want := range map[string]struct {
				mode   auth.Mode
				source string
			}{
				"":        {auth.ModeOff, "source=default"},
				"enforce": {auth.ModeEnforce, `source="env HOLLIS_AUTH"`},
			} {
				t.Setenv("HOLLIS_AUTH", dial)
				logs := &syncBuf{}
				a, _, err := httpserver.StartAuth(guardContext(t), dir, os.Getenv, "s3cret", "127.0.0.1:7777", slog.New(slog.NewTextHandler(logs, nil)))
				require.NoError(t, err, name)
				assert.Equal(t, want.mode, a.Guard.Mode(), "%s, dial %q", name, dial)
				assert.Contains(t, logs.String(), "level=WARN")
				assert.Contains(t, logs.String(), "mode="+string(want.mode))
				assert.Contains(t, logs.String(), want.source, "%s, dial %q", name, dial)
				assert.Contains(t, logs.String(), "auth_file="+filepath.Join(dir, httpserver.AuthFileName))
			}
			t.Setenv("HOLLIS_AUTH", "")
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
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TORQUE", "")
	i := newJWTIssuer(t)
	dir := t.TempDir()
	writeAuthFile(t, dir, "mode: enforce\njwt:\n  bootstrap_key_file: "+i.keyFile+"\n")
	a, _, err := httpserver.StartAuth(guardContext(t), dir, os.Getenv, "s3cret", "0.0.0.0:7777", slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
	require.NoError(t, err)
	require.Equal(t, auth.ModeEnforce, a.Guard.Mode())
	writeAuthFile(t, dir, "") // emptied: inherits the (unset) dial, i.e. off, which is a relaxation
	r := a.Reloader(filepath.Join(dir, httpserver.AuthFileName), 0, slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
	require.Error(t, r.Load(context.Background()))
	assert.Equal(t, auth.ModeEnforce, a.Guard.Mode())
}

func TestRemovingTheFileFallsBackToTheEnvironment(t *testing.T) {
	i := newJWTIssuer(t)
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TORQUE", "permissive")
	t.Setenv("TORQUE_AUTH_BOOTSTRAP_KEY_FILE", i.keyFile)
	dir := t.TempDir()
	path := filepath.Join(dir, httpserver.AuthFileName)
	writeAuthFile(t, dir, "mode: enforce\n")
	logs := &syncBuf{}
	lg := slog.New(slog.NewTextHandler(logs, nil))
	a, _, err := httpserver.StartAuth(guardContext(t), dir, os.Getenv, "", "127.0.0.1:7777", lg)
	require.NoError(t, err)
	require.Equal(t, auth.ModeEnforce, a.Guard.Mode())
	ts := setupSecuredServer(t, httpserver.Security{Guard: a.Guard})

	ctx, cancel := context.WithCancel(guardContext(t))
	done := make(chan struct{})
	go func() { a.Watch(ctx, path, 10*time.Millisecond, lg); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(60 * time.Millisecond)

	require.NoError(t, os.Remove(path))
	require.Eventually(t, func() bool { return a.Guard.Mode() == auth.ModePermissive }, 5*time.Second, 20*time.Millisecond, "the removal to take effect")
	assert.Equal(t, http.StatusOK, listTasks(t, ts, ""), "permissive from the environment dial")
	out := logs.String()
	assert.Contains(t, out, "auth mode changed")
	assert.Contains(t, out, "from=enforce")
	assert.Contains(t, out, "to=permissive")
	assert.Contains(t, out, `source="env HOLLIS_AUTH_TORQUE"`)
	assert.Contains(t, out, "auth_file=none")
	assert.Equal(t, http.StatusOK, listTasks(t, ts, i.serviceToken("torque.read", "svc-1")), "the environment's JWT key is in use")
}

func TestRemovingTheFileCannotRelaxANonLoopbackListener(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TORQUE", "")
	i := newJWTIssuer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, httpserver.AuthFileName)
	writeAuthFile(t, dir, "mode: enforce\njwt:\n  bootstrap_key_file: "+i.keyFile+"\n")
	a, _, err := httpserver.StartAuth(guardContext(t), dir, os.Getenv, "s3cret", "0.0.0.0:7777", slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	r := a.Reloader(path, 0, slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
	require.Error(t, r.Load(context.Background()), "falling back to the (unset) dial would be off")
	assert.Equal(t, auth.ModeEnforce, a.Guard.Mode())
}

func TestAuthReloadMayOnlyRelaxOnLoopback(t *testing.T) {
	for addr, allowed := range map[string]bool{
		"127.0.0.1:7777": true, "localhost:7777": true, "[::1]:7777": true, "tcp:127.0.0.1:7777": true, "unix:/tmp/torque.sock": true,
		"100.64.1.2:7777": false, "host.tail1234.ts.net:7777": false, "0.0.0.0:7777": false, ":7777": false, "10.0.0.5:7777": false, "garbage": false,
	} {
		i := newJWTIssuer(t)
		cfg := i.cfg(auth.ModeEnforce)
		cfg.Addr = addr
		a, _ := newAuthServer(t, cfg)
		err := a.Reload(i.cfg(auth.ModePermissive))
		if allowed {
			assert.NoError(t, err, addr)
			assert.Equal(t, auth.ModePermissive, a.Guard.Mode(), addr)
		} else {
			assert.Error(t, err, addr)
			assert.Equal(t, auth.ModeEnforce, a.Guard.Mode(), addr)
		}
	}
}

// Through the serve wiring (StartAuth + Watch + auth.yaml): a tailnet-style bind
// may start in any mode, may be tightened by an edit, and may not be loosened by
// one; the refusal is logged and the last good configuration stays.
func TestTailnetBindThroughTheServeWiringTightensButNeverLoosens(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TORQUE", "")
	i := newJWTIssuer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, httpserver.AuthFileName)
	keyed := func(mode string) {
		writeAuthFile(t, dir, "mode: "+mode+"\njwt:\n  bootstrap_key_file: "+i.keyFile+"\n")
	}
	keyed("permissive") // the legacy token-gated non-loopback start
	logs := &syncBuf{}
	lg := slog.New(slog.NewTextHandler(logs, nil))
	a, _, err := httpserver.StartAuth(guardContext(t), dir, os.Getenv, "s3cret", "100.64.1.2:7777", lg)
	require.NoError(t, err, "startup bind posture is unchanged")
	require.Equal(t, auth.ModePermissive, a.Guard.Mode())

	ctx, cancel := context.WithCancel(guardContext(t))
	done := make(chan struct{})
	go func() { a.Watch(ctx, path, 10*time.Millisecond, lg); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(60 * time.Millisecond)

	keyed("enforce") // tightening is allowed
	require.Eventually(t, func() bool { return a.Guard.Mode() == auth.ModeEnforce }, 5*time.Second, 20*time.Millisecond)

	keyed("off") // loosening is refused, logged, last good kept
	require.Eventually(t, func() bool { return strings.Contains(logs.String(), "keeping previous configuration") }, 5*time.Second, 20*time.Millisecond)
	assert.Equal(t, auth.ModeEnforce, a.Guard.Mode())
	out := logs.String()
	assert.Contains(t, out, "level=WARN")
	assert.Contains(t, out, "not loopback")
	assert.NotContains(t, out, "to=off", "the refused mode must not have been applied")
}

func TestExplicitEmptyKeyClearsAnInheritedValueButAnOmittedKeyInherits(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TORQUE", "")
	i := newJWTIssuer(t)
	t.Setenv("TORQUE_AUTH_BOOTSTRAP_KEY_FILE", i.keyFile)
	tok := i.serviceToken("torque.read", "svc-1")
	for name, c := range map[string]struct {
		body    string
		inherit bool
	}{
		"omitted key inherits":           {"mode: enforce\njwt:\n  audience: torque\n", true},
		"explicit empty clears":          {"mode: enforce\njwt:\n  bootstrap_key_file: \"\"\n", false},
		"explicit empty (single quotes)": {"mode: enforce\njwt:\n  bootstrap_key_file: ''\n", false},
		"null is omitted":                {"mode: enforce\njwt:\n  bootstrap_key_file: null\n", true},
	} {
		dir := t.TempDir()
		writeAuthFile(t, dir, c.body)
		a, _, err := start(t, dir)
		require.NoError(t, err, name)
		ts := setupSecuredServer(t, httpserver.Security{Token: "s3cret", Guard: a.Guard})
		want := http.StatusUnauthorized
		if c.inherit {
			want = http.StatusOK
		}
		assert.Equal(t, want, listTasks(t, ts, tok), name)
		assert.Equal(t, http.StatusOK, listTasks(t, ts, "s3cret"), "%s: the static token is unaffected", name)
	}

	// The same through a reload: clearing the inherited key rotates it away.
	dir := t.TempDir()
	writeAuthFile(t, dir, "mode: enforce\n")
	a, _, err := start(t, dir)
	require.NoError(t, err)
	ts := setupSecuredServer(t, httpserver.Security{Token: "s3cret", Guard: a.Guard})
	require.Equal(t, http.StatusOK, listTasks(t, ts, tok), "inherited before the edit")
	writeAuthFile(t, dir, "mode: enforce\njwt:\n  bootstrap_key_file: \"\"\n")
	r := a.Reloader(filepath.Join(dir, httpserver.AuthFileName), 0, slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
	require.NoError(t, r.Load(context.Background()))
	assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, tok), "cleared after the reload")
}

// Every string key is presence-aware: omitted inherits TORQUE_AUTH_*, written
// empty clears it, written non-empty overrides it. Mode is the exception: an
// empty mode inherits the dial, and an explicit "off" selects off.
func TestEveryStringKeyIsPresenceAware(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TORQUE", "")
	env := map[string]string{
		"TORQUE_AUTH_ISSUER": "https://env.example", "TORQUE_AUTH_AUDIENCE": "env-aud", "TORQUE_AUTH_JWKS_URL": "https://env.example/jwks",
		"TORQUE_AUTH_BOOTSTRAP_KEY_FILE": "/env/key.pem", "TORQUE_AUTH_DENYLIST_URL": "https://env.example/deny",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	type keyCase struct {
		name, yaml string
		get        func(httpserver.AuthConfig) string
		envVal     string
	}
	keys := []keyCase{
		{"issuer", "jwt:\n  issuer: %s\n", func(c httpserver.AuthConfig) string { return c.Issuer }, env["TORQUE_AUTH_ISSUER"]},
		{"audience", "jwt:\n  audience: %s\n", func(c httpserver.AuthConfig) string { return c.Audience }, env["TORQUE_AUTH_AUDIENCE"]},
		{"jwks_url", "jwt:\n  jwks_url: %s\n", func(c httpserver.AuthConfig) string { return c.JWKSURL }, env["TORQUE_AUTH_JWKS_URL"]},
		{"bootstrap_key_file", "jwt:\n  bootstrap_key_file: %s\n", func(c httpserver.AuthConfig) string { return c.BootstrapKeyFile }, env["TORQUE_AUTH_BOOTSTRAP_KEY_FILE"]},
		{"deny_list.url", "deny_list:\n  url: %s\n", func(c httpserver.AuthConfig) string { return c.DenyListURL }, env["TORQUE_AUTH_DENYLIST_URL"]},
	}
	for _, k := range keys {
		load := func(body string) httpserver.AuthConfig {
			dir := t.TempDir()
			writeAuthFile(t, dir, body)
			cfg, file, err := httpserver.LoadAuthConfig(dir, os.Getenv)
			require.NoError(t, err, k.name)
			require.NotEmpty(t, file)
			return cfg
		}
		assert.Equal(t, k.envVal, k.get(load("mode: off\n")), "%s: omitted inherits", k.name)
		assert.Equal(t, k.envVal, k.get(load(fmt.Sprintf(k.yaml, "null"))), "%s: null inherits", k.name)
		assert.Equal(t, "", k.get(load(fmt.Sprintf(k.yaml, `""`))), "%s: written empty clears", k.name)
		assert.Equal(t, "", k.get(load(fmt.Sprintf(k.yaml, `''`))), "%s: written empty (single quotes) clears", k.name)
		assert.Equal(t, "file-value", k.get(load(fmt.Sprintf(k.yaml, "file-value"))), "%s: written value overrides", k.name)
	}

	t.Setenv("HOLLIS_AUTH_TORQUE", "enforce")
	load := func(body string) httpserver.AuthConfig {
		dir := t.TempDir()
		writeAuthFile(t, dir, body)
		cfg, _, err := httpserver.LoadAuthConfig(dir, os.Getenv)
		require.NoError(t, err)
		return cfg
	}
	assert.Equal(t, auth.Mode(""), load("mode: \"\"\n").Mode, "an empty mode is not set: the dial applies")
	assert.Equal(t, "env HOLLIS_AUTH_TORQUE", load("mode: \"\"\n").ModeSource)
	assert.Equal(t, auth.ModeOff, load("mode: off\n").Mode, "an explicit off selects off (subject to the relaxation checks)")
	assert.Equal(t, httpserver.AuthFileName, load("mode: off\n").ModeSource)
}

// A tailnet-style bind through the wiring also accepts a same-mode reload (key
// rotation) — only loosening is refused.
func TestTailnetBindAcceptsASameModeReloadThroughTheServeWiring(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	t.Setenv("HOLLIS_AUTH_TORQUE", "")
	one, two := newJWTIssuer(t), newJWTIssuer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, httpserver.AuthFileName)
	writeAuthFile(t, dir, "mode: enforce\njwt:\n  bootstrap_key_file: "+one.keyFile+"\n")
	a, _, err := httpserver.StartAuth(guardContext(t), dir, os.Getenv, "s3cret", "100.101.102.103:7777", slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
	require.NoError(t, err)
	ts := setupSecuredServer(t, httpserver.Security{Token: "s3cret", Guard: a.Guard})
	require.Equal(t, http.StatusOK, listTasks(t, ts, one.serviceToken("torque.read", "svc-1")))

	writeAuthFile(t, dir, "mode: enforce\njwt:\n  bootstrap_key_file: "+two.keyFile+"\n")
	r := a.Reloader(path, 0, slog.New(slog.NewTextHandler(&syncBuf{}, nil)))
	require.NoError(t, r.Load(context.Background()), "same mode is not a loosening")
	assert.Equal(t, http.StatusOK, listTasks(t, ts, two.serviceToken("torque.read", "svc-2")), "the rotated key")
	assert.Equal(t, http.StatusUnauthorized, listTasks(t, ts, one.serviceToken("torque.read", "svc-1")))

	writeAuthFile(t, dir, "mode: permissive\njwt:\n  bootstrap_key_file: "+two.keyFile+"\n")
	require.Error(t, r.Load(context.Background()), "loosening on 100.x")
	assert.Equal(t, auth.ModeEnforce, a.Guard.Mode())
}

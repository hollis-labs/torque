package httpserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/auth"
	"gopkg.in/yaml.v3"
)

// AuthFileName is the optional auth configuration file inside Torque's config
// directory. Precedence is PER KEY: a key the file sets explicitly overrides
// the environment for that key only; anything the file leaves out (including
// an empty file) is inherited from TORQUE_AUTH_* (JWT / deny-list) and from the
// HOLLIS_AUTH_TORQUE / HOLLIS_AUTH dial (mode), else off. Removing the file at
// runtime falls back to the environment the same way. The effective mode and
// where it came from are logged at WARN at start and after every reload, and a
// reload that would weaken the mode on a non-loopback listener is refused.
//
// `torque serve` re-reads the file on an edit or SIGHUP (Auth.Watch). The file
// must not be group- or world-writable, at startup and on every reload.
//
//	mode: enforce            # off | permissive | enforce
//	jwt:
//	  issuer: https://tether.example
//	  audience: torque       # default torque
//	  jwks_url: ...          # default <issuer>/.well-known/jwks.json
//	  bootstrap_key_file: /path/issuer.pub.pem
//	deny_list:
//	  url: ...               # default <issuer>/auth/denylist
//
// The static token (TORQUE_API_TOKEN / --token) is a startup input and is not
// part of the file.
const AuthFileName = "auth.yaml"

type authFile struct {
	Mode string `yaml:"mode"`
	JWT  struct {
		Issuer           string `yaml:"issuer"`
		Audience         string `yaml:"audience"`
		JWKSURL          string `yaml:"jwks_url"`
		BootstrapKeyFile string `yaml:"bootstrap_key_file"`
	} `yaml:"jwt"`
	DenyList struct {
		URL string `yaml:"url"`
	} `yaml:"deny_list"`
}

// LoadAuthFile reads path and returns only what the file sets explicitly. exists is false (and the error nil) when the file is
// absent. Unknown keys are an error: a typo must not silently drop a setting.
func LoadAuthFile(path string) (cfg AuthConfig, exists bool, err error) {
	f0, err := os.Open(path) // #nosec G304 -- operator-configured path
	if os.IsNotExist(err) {
		return AuthConfig{}, false, nil
	}
	if err != nil {
		return AuthConfig{}, true, fmt.Errorf("%s: %w", AuthFileName, err)
	}
	defer func() { _ = f0.Close() }()
	// Checked on the open file, so the file that is read is the file that was checked.
	fi, err := f0.Stat()
	if err != nil {
		return AuthConfig{}, true, fmt.Errorf("%s: %w", AuthFileName, err)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return AuthConfig{}, true, fmt.Errorf("%s: refusing a group- or world-writable file (mode %04o); chmod go-w", AuthFileName, fi.Mode().Perm())
	}
	b, err := io.ReadAll(io.LimitReader(f0, 64<<10))
	if err != nil {
		return AuthConfig{}, true, fmt.Errorf("%s: %w", AuthFileName, err)
	}
	var f authFile
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && err.Error() != "EOF" {
		return AuthConfig{}, true, fmt.Errorf("%s: %w", AuthFileName, err)
	}
	return AuthConfig{
		Mode:             auth.Mode(strings.TrimSpace(f.Mode)),
		Issuer:           strings.TrimSpace(f.JWT.Issuer),
		Audience:         strings.TrimSpace(f.JWT.Audience),
		JWKSURL:          strings.TrimSpace(f.JWT.JWKSURL),
		BootstrapKeyFile: strings.TrimSpace(f.JWT.BootstrapKeyFile),
		DenyListURL:      strings.TrimSpace(f.DenyList.URL),
	}, true, nil
}

// Reloader returns the watcher Watch runs: it re-reads path when its content
// changes (every interval, default 2s, once stable for two checks), on SIGHUP
// and on Trigger, and applies it with Reload. A file that fails to parse or is
// group/world-writable, or a configuration that fails to build or validate, is
// logged and ignored: the running configuration stays. A file that does not
// exist means the environment alone (that is how a removal takes effect).
func (a *Auth) Reloader(path string, interval time.Duration, logger *slog.Logger) *auth.Reloader {
	return &auth.Reloader{
		Path: path, Interval: interval, Logger: logger,
		Load: func(context.Context) error {
			cfg, _, err := LoadAuthConfigAt(path, os.Getenv)
			if err != nil {
				return err
			}
			return a.Reload(cfg)
		},
	}
}

// Watch runs the Reloader until ctx is done. The Reloader only notices content
// changes of an existing file, so Watch also notices the file disappearing (two
// checks in a row, so an editor's replace-by-rename is not mistaken for it)
// and triggers a reload, which then runs on the environment alone.
func (a *Auth) Watch(ctx context.Context, path string, interval time.Duration, logger *slog.Logger) {
	r := a.Reloader(path, interval, logger)
	iv := interval
	if iv <= 0 {
		iv = 2 * time.Second
	}
	go func() {
		t := time.NewTicker(iv)
		defer t.Stop()
		_, err := os.Stat(path)
		existed, absent := err == nil, 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if _, err := os.Stat(path); err == nil {
				existed, absent = true, 0
			} else if os.IsNotExist(err) && existed {
				if absent++; absent == 2 {
					existed = false
					r.Trigger()
				}
			}
		}
	}()
	_ = r.Run(ctx)
}

// envAuthConfig is the TORQUE_AUTH_* environment.
func envAuthConfig(getenv func(string) string) AuthConfig {
	e := func(k string) string { return strings.TrimSpace(getenv(k)) }
	return AuthConfig{
		Issuer: e("TORQUE_AUTH_ISSUER"), Audience: e("TORQUE_AUTH_AUDIENCE"), JWKSURL: e("TORQUE_AUTH_JWKS_URL"),
		BootstrapKeyFile: e("TORQUE_AUTH_BOOTSTRAP_KEY_FILE"), DenyListURL: e("TORQUE_AUTH_DENYLIST_URL"),
	}
}

// modeSource names where the effective mode comes from.
func modeSource(fromFile bool, getenv func(string) string) string {
	if fromFile {
		return AuthFileName
	}
	for _, name := range []string{"HOLLIS_AUTH_TORQUE", "HOLLIS_AUTH"} {
		if strings.TrimSpace(getenv(name)) != "" {
			return "env " + name
		}
	}
	return "default"
}

// LoadAuthConfigAt is the configuration for the auth file at path: the
// TORQUE_AUTH_* environment with each key the file sets explicitly laid over
// it. file is path when the file exists, else "". The result's ModeSource says
// whether the mode is the file's, the HOLLIS_AUTH_* dial's, or the default.
func LoadAuthConfigAt(path string, getenv func(string) string) (cfg AuthConfig, file string, err error) {
	fc, exists, err := LoadAuthFile(path)
	if err != nil {
		return AuthConfig{}, path, err
	}
	cfg = envAuthConfig(getenv)
	if exists {
		file = path
		set := func(dst *string, v string) {
			if v != "" {
				*dst = v
			}
		}
		set(&cfg.Issuer, fc.Issuer)
		set(&cfg.Audience, fc.Audience)
		set(&cfg.JWKSURL, fc.JWKSURL)
		set(&cfg.BootstrapKeyFile, fc.BootstrapKeyFile)
		set(&cfg.DenyListURL, fc.DenyListURL)
		cfg.Mode = fc.Mode
	}
	cfg.ModeSource, cfg.File = modeSource(cfg.Mode != "", getenv), file
	return cfg, file, nil
}

// LoadAuthConfig is LoadAuthConfigAt for auth.yaml in configDir.
func LoadAuthConfig(configDir string, getenv func(string) string) (AuthConfig, string, error) {
	return LoadAuthConfigAt(filepath.Join(configDir, AuthFileName), getenv)
}

// StartAuth is what `torque serve` runs: it resolves the configuration (see
// AuthFileName), adds the startup-only inputs (static token, listener) and
// builds the Auth. An error from a configuration that involves the file names it.
func StartAuth(ctx context.Context, configDir string, getenv func(string) string, staticToken, addr string, logger *slog.Logger) (a *Auth, file string, err error) {
	cfg, file, err := LoadAuthConfig(configDir, getenv)
	if err != nil {
		return nil, file, fmt.Errorf("auth: %w", err)
	}
	cfg.StaticToken, cfg.Addr, cfg.Logger = staticToken, addr, logger
	a, err = NewAuth(ctx, cfg)
	if err != nil {
		if file != "" {
			return nil, file, fmt.Errorf("auth (from %s): %w", file, err)
		}
		return nil, file, fmt.Errorf("auth: %w", err)
	}
	return a, file, nil
}

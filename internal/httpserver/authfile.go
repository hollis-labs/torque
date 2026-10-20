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
// directory. Precedence:
//
//   - No file: TORQUE_AUTH_* (JWT / deny-list) and HOLLIS_AUTH_TORQUE /
//     HOLLIS_AUTH (mode) are read once at startup, as before.
//   - File present: it REPLACES the TORQUE_AUTH_* variables completely (an
//     absent key means unset, not "fall back to the variable"). The mode is the
//     file's `mode` when set; when it is absent or the file is empty the
//     posture dial HOLLIS_AUTH_TORQUE / HOLLIS_AUTH applies, else off. The
//     dial stays an environment-level control on purpose, so an empty file
//     cannot silently relax a deployment that sets HOLLIS_AUTH_TORQUE=enforce.
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

// LoadAuthFile reads path. exists is false (and the error nil) when the file is
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
// and on Trigger, and applies it with Reload. A file that fails to parse, a
// missing file, or a configuration that fails to build or validate is logged
// and ignored: the running configuration stays.
func (a *Auth) Reloader(path string, interval time.Duration, logger *slog.Logger) *auth.Reloader {
	return &auth.Reloader{
		Path: path, Interval: interval, Logger: logger,
		Load: func(context.Context) error {
			cfg, exists, err := LoadAuthFile(path)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%s does not exist; keeping the running configuration", path)
			}
			return a.Reload(cfg)
		},
	}
}

// Watch runs the Reloader until ctx is done.
func (a *Auth) Watch(ctx context.Context, path string, interval time.Duration, logger *slog.Logger) {
	_ = a.Reloader(path, interval, logger).Run(ctx)
}

// LoadAuthConfig is the startup configuration: auth.yaml in configDir when it
// exists (replacing TORQUE_AUTH_*), else the TORQUE_AUTH_* variables. file is
// the path when the config came from it, else "".
func LoadAuthConfig(configDir string, getenv func(string) string) (cfg AuthConfig, file string, err error) {
	path := filepath.Join(configDir, AuthFileName)
	fileCfg, exists, err := LoadAuthFile(path)
	if err != nil {
		return AuthConfig{}, path, err
	}
	if exists {
		return fileCfg, path, nil
	}
	e := func(k string) string { return strings.TrimSpace(getenv(k)) }
	return AuthConfig{
		Issuer: e("TORQUE_AUTH_ISSUER"), Audience: e("TORQUE_AUTH_AUDIENCE"), JWKSURL: e("TORQUE_AUTH_JWKS_URL"),
		BootstrapKeyFile: e("TORQUE_AUTH_BOOTSTRAP_KEY_FILE"), DenyListURL: e("TORQUE_AUTH_DENYLIST_URL"),
	}, "", nil
}

// StartAuth is what `torque serve` runs: it resolves the configuration (see
// AuthFileName), adds the startup-only inputs (static token, listener) and
// builds the Auth. An error from a file-derived configuration names the file.
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

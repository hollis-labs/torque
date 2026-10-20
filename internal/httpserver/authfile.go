package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/auth"
	"gopkg.in/yaml.v3"
)

// AuthFileName is the optional auth configuration file inside Torque's config
// directory. When it exists it REPLACES the TORQUE_AUTH_* environment as the
// source of the mode and the JWT / deny-list settings, and `torque serve`
// re-reads it on an edit or SIGHUP (Auth.Watch). Without it nothing changes:
// the environment is read once at startup, as before.
//
//	mode: enforce            # off | permissive | enforce; HOLLIS_AUTH_TORQUE / HOLLIS_AUTH apply when absent
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
	b, err := os.ReadFile(path) // #nosec G304 -- operator-configured path
	if os.IsNotExist(err) {
		return AuthConfig{}, false, nil
	}
	if err != nil {
		return AuthConfig{}, true, err
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

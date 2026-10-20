package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/torque/internal/mcpscopes"
)

// AuthApp is the app id Torque uses for its scope strings, its audience
// default and the HOLLIS_AUTH_<APP> mode override.
const AuthApp = "torque"

// AuthConfig configures the shared go-auth guard (libs/security/auth).
// Tether is the token issuer: it mints Ed25519 JWTs that Torque verifies
// offline against the issuer's JWKS.
type AuthConfig struct {
	// Mode is off, permissive or enforce. Empty means ModeFromEnv with the
	// pre-cutover default, off: a presented token is verified and attributed,
	// but the legacy TORQUE_API_TOKEN gate still decides admission, so
	// current clients keep working.
	Mode auth.Mode
	// Issuer is the Tether base URL (also the required "iss" value). With no
	// Issuer and no JWKSURL/BootstrapKeyFile, JWT verification is off and
	// only the static token is recognised.
	Issuer           string
	Audience         string // default "torque"
	JWKSURL          string // default <Issuer>/.well-known/jwks.json
	BootstrapKeyFile string
	DenyListURL      string // default <Issuer>/auth/denylist when Issuer is set
	// DenyListInterval is the deny-list poll period; default 30s. It bounds
	// how long a revoked token (including a long-lived service token) is
	// still accepted.
	DenyListInterval time.Duration
	// StaticToken is the legacy TORQUE_API_TOKEN. It is registered as an
	// API-key principal with torque.* so it keeps working under enforce.
	StaticToken string
	Audit       func(auth.Event)
	Logger      *slog.Logger
}

// NewGuard builds the guard and starts its background refreshers (JWKS,
// deny-list), which stop when ctx is cancelled.
func NewGuard(ctx context.Context, c AuthConfig) (*auth.Guard, error) {
	mode := c.Mode
	if mode == "" {
		m, err := auth.ModeFromEnv(AuthApp, auth.ModeOff)
		if err != nil {
			return nil, err
		}
		mode = m
	}
	var vs []auth.Verifier
	if c.Issuer != "" || c.JWKSURL != "" || c.BootstrapKeyFile != "" {
		issuer := strings.TrimRight(c.Issuer, "/")
		jwksURL := c.JWKSURL
		if jwksURL == "" && issuer != "" {
			jwksURL = issuer + "/.well-known/jwks.json"
		}
		audience := c.Audience
		if audience == "" {
			audience = AuthApp
		}
		cfg := auth.JWTConfig{Issuer: c.Issuer, Audience: audience, JWKSURL: jwksURL, BootstrapKeyFile: c.BootstrapKeyFile}
		denyURL := c.DenyListURL
		if denyURL == "" && issuer != "" {
			denyURL = issuer + "/auth/denylist"
		}
		if denyURL != "" {
			dl := auth.NewDenyList()
			cfg.DenyList = dl
			go (&auth.DenyListPoller{URL: denyURL, List: dl, Interval: c.DenyListInterval}).Run(ctx)
		}
		jv, err := auth.NewJWTVerifier(cfg)
		if err != nil {
			return nil, fmt.Errorf("auth: %w", err)
		}
		go jv.Run(ctx)
		vs = append(vs, jv)
	}
	if c.StaticToken != "" {
		vs = append(vs, auth.NewAPIKeyVerifier(auth.APIKey{
			Hash: auth.HashKey(c.StaticToken), ID: "torque/static-token", Display: "TORQUE_API_TOKEN",
			Scopes: []string{AuthApp + ".*"},
		}))
	}
	return auth.New(auth.Config{Mode: mode, Verifier: auth.Chain(vs...), Scopes: scopeTable(), Audit: c.Audit, Logger: c.Logger})
}

// scopeTable maps the HTTP surface to two scopes: torque.read for safe
// methods and torque.write for the rest. /mcp needs authentication only at
// the transport; each tool call is scoped by mcpscopes (AuthorizeTool).
func scopeTable() *auth.ScopeTable {
	return mcpscopes.AddTo(auth.NewScopeTable()).
		Route("GET", "*", AuthApp+".read").
		Route("HEAD", "*", AuthApp+".read").
		Route("*", "*", AuthApp+".write")
}

package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
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
	// DenyListInterval is the deny-list poll period; default 30s. Revocation
	// takes effect within one interval after a successful poll. If the
	// deny-list endpoint is unreachable the last list stays in force (stale),
	// so this is not an upper bound during an outage, which matters for
	// long-lived service tokens.
	DenyListInterval time.Duration
	// StaticToken is the legacy TORQUE_API_TOKEN. It is registered as an
	// API-key principal with torque.* so it keeps working under enforce.
	StaticToken string
	Audit       func(auth.Event)
	Logger      *slog.Logger
}

// reloadFetchTimeout bounds the first JWKS / deny-list fetch a reload requires.
const reloadFetchTimeout = 10 * time.Second

// Auth is the go-auth guard plus what has to outlive a configuration reload:
// the deny-list and the one poller that feeds it, the static token, and the
// loops of the current verifier. Reload swaps the mode and verifier through
// Guard.Apply (security/auth v0.2.1), which pins {mode, verifier} per request.
// Torque keeps no per-generation application data, so the guard's own pin is
// the whole generation.
type Auth struct {
	Guard *auth.Guard

	base   context.Context
	static string // TORQUE_API_TOKEN / --token: a startup input, kept across reloads
	scopes *auth.ScopeTable

	mu  sync.Mutex // serializes Reload
	cur *generation
}

// generation is what one configuration started: its mode and verifier, and the
// loops (JWKS refresh, deny-list poll) that belong to it.
type generation struct {
	mode     auth.Mode
	verifier auth.Verifier
	verStop  context.CancelFunc // JWKS refresh loop of this verifier
	denyStop context.CancelFunc // poller feeding denyList (shared across generations while the URL is unchanged)
	denyURL  string
	denyList *auth.DenyList
}

func (g *generation) stopNew(prev *generation) {
	// Stop only what this generation started: its verifier loop, and its own
	// poller when it did not inherit the previous one.
	if g.verStop != nil {
		g.verStop()
	}
	if g.denyStop != nil && (prev == nil || g.denyList != prev.denyList) {
		g.denyStop()
	}
}

// NewGuard builds the guard and starts its background refreshers (JWKS,
// deny-list), which stop when ctx is cancelled.
func NewGuard(ctx context.Context, c AuthConfig) (*auth.Guard, error) {
	a, err := NewAuth(ctx, c)
	if err != nil {
		return nil, err
	}
	return a.Guard, nil
}

// NewAuth is NewGuard plus the handle Reload and Watch need.
func NewAuth(ctx context.Context, c AuthConfig) (*Auth, error) {
	a := &Auth{base: ctx, static: c.StaticToken, scopes: scopeTable()}
	gen, err := a.build(c, nil)
	if err != nil {
		return nil, err
	}
	g, err := auth.New(auth.Config{Mode: gen.mode, Verifier: gen.verifier, Scopes: a.scopes, Audit: c.Audit, Logger: c.Logger})
	if err != nil {
		gen.stopNew(nil)
		return nil, err
	}
	a.Guard, a.cur = g, gen
	return a, nil
}

// Reload applies c as the new auth configuration, all or nothing. The next
// generation (verifier chain, a fresh JWKS loop) is built and validated first:
// a reload must not publish a verifier that cannot verify yet (a JWKS URL with
// no bootstrap key needs its first fetch to succeed) or a deny-list endpoint it
// has never heard from (that needs a validated first poll). The deny-list of an
// unchanged endpoint is kept, so an edit or SIGHUP during an outage of that
// endpoint cannot un-revoke a token. Any failure leaves the running
// configuration untouched. The static token comes from startup and is kept.
func (a *Auth) Reload(c AuthConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	next, err := a.build(c, a.cur)
	if err != nil {
		return fmt.Errorf("auth reload: %w", err)
	}
	if err := a.Guard.Apply(next.mode, next.verifier); err != nil {
		next.stopNew(a.cur)
		return fmt.Errorf("auth reload: %w", err)
	}
	prev := a.cur
	a.cur = next
	if prev.verStop != nil {
		prev.verStop()
	}
	if prev.denyStop != nil && prev.denyList != next.denyList {
		prev.denyStop() // the old endpoint's poller and list are replaced
	}
	if next.denyStop == nil && prev.denyList == next.denyList {
		next.denyStop = prev.denyStop // the shared list keeps its one poller
	}
	return nil
}

// resolveMode is the configured mode, else HOLLIS_AUTH_TORQUE / HOLLIS_AUTH,
// else off. An invalid value is an error, never a fall-through.
func resolveMode(c AuthConfig) (auth.Mode, error) {
	if c.Mode != "" {
		return auth.ParseMode(string(c.Mode))
	}
	return auth.ModeFromEnv(AuthApp, auth.ModeOff)
}

// build resolves the mode and builds the verifier chain for c. prev is the
// running generation (nil at startup); a non-nil prev means this is a reload.
// Anything build starts is stopped again if build fails.
func (a *Auth) build(c AuthConfig, prev *generation) (*generation, error) {
	mode, err := resolveMode(c)
	if err != nil {
		return nil, err
	}
	gen := &generation{mode: mode}
	ok := false
	defer func() {
		if !ok {
			gen.stopNew(prev)
		}
	}()
	reload := prev != nil
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
		reusing := false
		if denyURL != "" {
			gen.denyURL = denyURL
			if prev != nil && prev.denyList != nil && prev.denyURL == denyURL {
				gen.denyList, gen.denyStop, reusing = prev.denyList, prev.denyStop, true
			} else {
				gen.denyList = auth.NewDenyList()
			}
			cfg.DenyList = gen.denyList
		}
		jv, err := auth.NewJWTVerifier(cfg)
		if err != nil {
			return nil, fmt.Errorf("auth: %w", err)
		}
		if reload && jwksURL != "" && c.BootstrapKeyFile == "" {
			rctx, cancel := context.WithTimeout(a.base, reloadFetchTimeout)
			rerr := jv.Refresh(rctx)
			cancel()
			if rerr != nil {
				return nil, fmt.Errorf("auth: first JWKS fetch failed (the running configuration is kept): %s", scrubTransport(rerr))
			}
		}
		if denyURL != "" && !reusing {
			poller := &auth.DenyListPoller{URL: denyURL, List: gen.denyList, Interval: c.DenyListInterval}
			if reload {
				pctx, cancel := context.WithTimeout(a.base, reloadFetchTimeout)
				perr := poller.Poll(pctx)
				cancel()
				if perr != nil {
					return nil, fmt.Errorf("auth: first fetch from the new deny-list endpoint failed (the running configuration is kept): %s", scrubTransport(perr))
				}
			}
			dctx, dcancel := context.WithCancel(a.base)
			gen.denyStop = dcancel
			go poller.Run(dctx)
		}
		vctx, vcancel := context.WithCancel(a.base)
		gen.verStop = vcancel
		go jv.Run(vctx)
		vs = append(vs, jv)
	}
	if a.static != "" {
		vs = append(vs, auth.NewAPIKeyVerifier(auth.APIKey{
			Hash: auth.HashKey(a.static), ID: "torque/static-token", Display: "TORQUE_API_TOKEN",
			Scopes: []string{AuthApp + ".*"},
		}))
	}
	gen.verifier = auth.Chain(vs...)
	ok = true
	return gen, nil
}

// scrubTransport describes a fetch error without the request URL, which comes
// from configuration and may embed a credential.
func scrubTransport(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err.Error()
	}
	msg := err.Error()
	if i := strings.Index(msg, "http"); i >= 0 {
		return strings.TrimSpace(msg[:i]) + " (request URL omitted)"
	}
	return msg
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

// registry.go — the foreign-route registry: foreign authority → peer endpoint.
//
// ADR-0001 §4 makes a Torque install federated by *routing*: the authority-
// routing Router (router.go) serves a foreign authority from a remote Store.
// The Registry is the configured map of which authority is served by which
// peer install — the "address book" the Router's foreign branch is built from.
//
// Each ForeignRoute names a foreign `authority` and the peer `endpoint` that
// homes it. The Registry materializes a HTTPStore per route; ForeignStores()
// hands the Router the `map[authority]messaging.Store` that RouterConfig
// expects, and NewRouterWithRegistry wires the two together in one call.
//
// Standalone guarantee (ADR-0001 §4): a Torque-only install registers no
// foreign routes. An empty (or nil) Registry yields an empty foreign-route map,
// so the Router is a catch-all passthrough and messaging behaves exactly as
// today — zero extra configuration.
//
// Credentials (ADR-0002 §7): a route may carry pinned peer-cert fingerprints
// and an mTLS-configured *http.Client. Those fields are transport material —
// the Registry carries them and threads the client into the route's HTTPStore,
// but the trust decision itself is enforced by the receiving install's
// federation listener, not here.
package messaging

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	messaging "github.com/hollis-labs/go-messaging"
)

// ForeignRoute is one entry in the foreign-route registry: the peer install
// that homes a given foreign authority, plus the transport material for
// reaching it.
type ForeignRoute struct {
	// Authority is the foreign `authority` URN segment this route serves.
	// Required; must be unique within a Registry.
	Authority string

	// Endpoint is the peer install's base URL (scheme://host[:port]). The
	// federation route prefix (DefaultFederationBasePath) is appended by the
	// HTTPStore. Required.
	Endpoint string

	// Pins are the peer's pinned server-certificate SHA-256 fingerprints
	// (ADR-0002 §3: a list, to allow zero-downtime rotation). They are
	// transport material consumed by the mTLS layer that builds Client; the
	// Registry carries them but does not itself verify certificates.
	// Empty for a plaintext / non-pinned endpoint.
	Pins []string

	// Client is the *http.Client used to reach this peer. The federation-auth
	// layer (ADR-0002) injects a client whose Transport carries the install's
	// mTLS identity and pins this peer's server cert. Nil → HTTPStore's
	// default plaintext client (valid for http:// endpoints and tests).
	Client *http.Client
}

// Registry is the foreign-authority → peer-endpoint registry consumed by the
// authority-routing Router. It is immutable after construction and safe for
// concurrent use.
type Registry struct {
	routes map[string]ForeignRoute
	stores map[string]messaging.Store
}

// NewRegistry builds a Registry from the given foreign routes. An empty call
// (no routes) yields the standalone registry — valid, and the basis of the
// standalone guarantee.
//
// It fails if a route has an empty Authority or Endpoint, if an authority is
// declared more than once, or if an endpoint is not a usable URL (the route's
// HTTPStore is constructed eagerly so a bad endpoint fails at config time, not
// on first federated call).
func NewRegistry(routes ...ForeignRoute) (*Registry, error) {
	reg := &Registry{
		routes: make(map[string]ForeignRoute, len(routes)),
		stores: make(map[string]messaging.Store, len(routes)),
	}
	for _, rt := range routes {
		if rt.Authority == "" {
			return nil, errors.New("messaging: foreign route has an empty authority")
		}
		if rt.Endpoint == "" {
			return nil, fmt.Errorf("messaging: foreign route %q has an empty endpoint", rt.Authority)
		}
		if _, dup := reg.routes[rt.Authority]; dup {
			return nil, fmt.Errorf("messaging: foreign authority %q is registered twice", rt.Authority)
		}

		var opts []HTTPStoreOption
		if rt.Client != nil {
			opts = append(opts, WithHTTPClient(rt.Client))
		}
		store, err := NewHTTPStore(rt.Endpoint, opts...)
		if err != nil {
			return nil, fmt.Errorf("messaging: foreign route %q: %w", rt.Authority, err)
		}

		reg.routes[rt.Authority] = rt
		reg.stores[rt.Authority] = store
	}
	return reg, nil
}

// Authorities returns the foreign authorities this Registry routes, sorted.
func (r *Registry) Authorities() []string {
	out := make([]string, 0, len(r.routes))
	for a := range r.routes {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Route returns the ForeignRoute for authority, and whether one is registered.
func (r *Registry) Route(authority string) (ForeignRoute, bool) {
	rt, ok := r.routes[authority]
	return rt, ok
}

// Len reports how many foreign routes are registered. Zero means standalone.
func (r *Registry) Len() int { return len(r.routes) }

// ForeignStores materializes the foreign-route map the Router consumes:
// authority → the remote messaging.Store that serves it. The returned map is
// a fresh copy, safe for the caller to retain or mutate. For a standalone
// (empty) Registry it is empty — RouterConfig.ForeignRoutes then routes every
// authority to the local Store.
func (r *Registry) ForeignStores() map[string]messaging.Store {
	out := make(map[string]messaging.Store, len(r.stores))
	for a, s := range r.stores {
		out[a] = s
	}
	return out
}

// NewRouterWithRegistry builds an authority-routing Router whose foreign branch
// is populated from reg. It is the consumption seam between the foreign-route
// registry and the routing decorator (ADR-0001 §4): the Router serves every
// authority in reg from its registered peer Store and every other authority
// from local.
//
// A nil or empty reg yields a standalone Router — a catch-all passthrough when
// localAuthorities is also empty (the standalone guarantee).
func NewRouterWithRegistry(local messaging.Store, localAuthorities []string, reg *Registry) (*Router, error) {
	cfg := RouterConfig{
		Local:            local,
		LocalAuthorities: localAuthorities,
	}
	if reg != nil {
		cfg.ForeignRoutes = reg.ForeignStores()
	}
	return NewRouter(cfg)
}

// router.go — the authority-routing messaging.Store decorator.
//
// Federation in Torque is delivered by routing, never by forking the
// envelope schema (ADR-0001 §4). The Router is the mechanism: it implements
// the go-messaging Store contract in full and dispatches each call by the
// `authority` segment of the governing Address.
//
//   - An authority with a registered foreign route  → that route's Store
//     (a remote / HTTP-backed Store; see ph-4 federation tasks).
//   - Any other authority                           → the local Store
//     (Torque's SQLite Store).
//
// "Internal vs. external" is exactly this one question — is the authority
// foreign-routed? — and nothing else. There is no separate internal/external
// schema, table, or Kind.
//
// Standalone guarantee (ADR-0001 §4): a Torque-only install registers no
// foreign routes. With an empty route registry every authority resolves to
// the local Store and the Router is a transparent passthrough — messaging
// behaves exactly as a bare *Store, with zero extra configuration. The
// shared go-messaging contract suite is run against a standalone Router in
// router_test.go to prove this.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"sort"

	messaging "github.com/hollis-labs/go-messaging"
)

// Compile-time assertion: *Router satisfies messaging.Store, so it is
// substitutable anywhere a Store is expected (broker, HTTP handlers, …).
var _ messaging.Store = (*Router)(nil)

// ErrUnroutableAuthority is returned when a call's governing authority is
// neither a declared local authority nor a registered foreign route. It
// upholds go-messaging's "no silent drop" stance (ADR-0001 §4): a
// misaddressed envelope fails loudly rather than landing in the wrong Store.
//
// It is only ever returned when the Router is in strict mode — i.e. the
// install has declared its local authorities. A standalone / catch-all
// Router never produces it.
var ErrUnroutableAuthority = errors.New("messaging: unroutable authority")

// RouterConfig configures an authority-routing Router.
type RouterConfig struct {
	// Local is the Store that serves every locally-homed authority —
	// Torque's SQLite Store. Required.
	Local messaging.Store

	// LocalAuthorities enumerates the authorities this install homes.
	//
	//   - Empty  → catch-all mode: every authority without a foreign
	//     route resolves to Local. This is the standalone default and
	//     makes the Router behave exactly as a bare Store.
	//   - Non-empty → strict mode: an authority that is neither listed
	//     here nor registered as a foreign route is rejected with
	//     ErrUnroutableAuthority.
	LocalAuthorities []string

	// ForeignRoutes maps a foreign authority to the Store that serves it
	// (a remote / HTTP-backed Store). Empty for a standalone install.
	ForeignRoutes map[string]messaging.Store
}

// Router is an authority-routing messaging.Store decorator. It is immutable
// after construction and safe for concurrent use to the same degree as the
// Stores it wraps.
type Router struct {
	local            messaging.Store
	localAuthorities map[string]struct{}
	foreign          map[string]messaging.Store
}

// NewRouter constructs a Router from cfg.
//
// It fails if Local is nil, if a foreign route's Store is nil, or if any
// authority appears in both LocalAuthorities and ForeignRoutes (an authority
// cannot be both homed locally and routed to a peer).
func NewRouter(cfg RouterConfig) (*Router, error) {
	if cfg.Local == nil {
		return nil, errors.New("messaging: Router requires a non-nil Local Store")
	}

	local := make(map[string]struct{}, len(cfg.LocalAuthorities))
	for _, a := range cfg.LocalAuthorities {
		local[a] = struct{}{}
	}

	foreign := make(map[string]messaging.Store, len(cfg.ForeignRoutes))
	for authority, store := range cfg.ForeignRoutes {
		if store == nil {
			return nil, fmt.Errorf("messaging: foreign route %q has a nil Store", authority)
		}
		if _, clash := local[authority]; clash {
			return nil, fmt.Errorf("messaging: authority %q is both local and a foreign route", authority)
		}
		foreign[authority] = store
	}

	return &Router{local: cfg.Local, localAuthorities: local, foreign: foreign}, nil
}

// route resolves the Store that serves the given authority. See the package
// doc for the rule; the catch-all branch is what preserves the standalone
// guarantee.
func (r *Router) route(authority string) (messaging.Store, error) {
	if s, ok := r.foreign[authority]; ok {
		return s, nil
	}
	if len(r.localAuthorities) == 0 {
		// Catch-all / standalone: no declared local identity, so every
		// non-foreign authority is served locally.
		return r.local, nil
	}
	if _, ok := r.localAuthorities[authority]; ok {
		return r.local, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnroutableAuthority, authority)
}

// Send routes by the recipient (To) authority — the governing authority for
// a Send (ADR-0001 §4).
func (r *Router) Send(ctx context.Context, env messaging.Envelope) (messaging.Envelope, error) {
	store, err := r.route(env.To.Authority)
	if err != nil {
		return messaging.Envelope{}, err
	}
	return store.Send(ctx, env)
}

// Inbox routes by the recipient authority. In practice an install only ever
// drains inboxes for its own recipients (ADR-0002 §4: "Inbox/Subscribe are
// never federated — by construction"), so this resolves to the local Store;
// the Router routes uniformly regardless.
func (r *Router) Inbox(ctx context.Context, to messaging.Address, f messaging.Filter) ([]messaging.Envelope, error) {
	store, err := r.route(to.Authority)
	if err != nil {
		return nil, err
	}
	return store.Inbox(ctx, to, f)
}

// Subscribe routes by the recipient authority. As with Inbox, this resolves
// to the local Store in practice.
func (r *Router) Subscribe(ctx context.Context, to messaging.Address, f messaging.Filter) (<-chan messaging.Envelope, error) {
	// Internal system subscriptions observe local traffic only, including
	// when federation requires explicit authorities for addressed calls.
	if to == (messaging.Address{}) {
		return r.local.Subscribe(ctx, to, f)
	}
	store, err := r.route(to.Authority)
	if err != nil {
		return nil, err
	}
	return store.Subscribe(ctx, to, f)
}

// Consume routes by the recipient authority — the recipient owns the
// delivery/consumption record being advanced.
func (r *Router) Consume(ctx context.Context, id string, recipient messaging.Address) error {
	store, err := r.route(recipient.Authority)
	if err != nil {
		return err
	}
	return store.Consume(ctx, id, recipient)
}

// Get retrieves an envelope by ID.
//
// Get carries only an opaque ID — the contract gives no Address to route on
// — so authority-based dispatch is impossible here. The Router resolves it
// local-first, then falls back to each foreign route until the envelope is
// found. For a standalone install (no foreign routes) this is a pure
// passthrough to the local Store.
//
// If no Store has the envelope, ErrNotFound is returned. If the envelope is
// absent locally and a foreign route is unavailable, the foreign error is
// surfaced rather than masked as ErrNotFound — the result is genuinely
// unknown, not known-absent.
func (r *Router) Get(ctx context.Context, id string) (messaging.Envelope, error) {
	env, err := r.local.Get(ctx, id)
	if err == nil {
		return env, nil
	}
	if !errors.Is(err, messaging.ErrNotFound) {
		return messaging.Envelope{}, err
	}

	var deferred error
	for _, store := range r.foreign {
		env, ferr := store.Get(ctx, id)
		if ferr == nil {
			return env, nil
		}
		if !errors.Is(ferr, messaging.ErrNotFound) {
			deferred = ferr
		}
	}
	if deferred != nil {
		return messaging.Envelope{}, deferred
	}
	return messaging.Envelope{}, messaging.ErrNotFound
}

// Cancel marks an envelope dead. Like Get it carries only an ID, so it
// resolves local-first then falls back to each foreign route. Cancel is
// idempotent; ErrNotFound is returned only when no Store has ever held the
// envelope.
func (r *Router) Cancel(ctx context.Context, id string) error {
	err := r.local.Cancel(ctx, id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, messaging.ErrNotFound) {
		return err
	}

	var deferred error
	for _, store := range r.foreign {
		ferr := store.Cancel(ctx, id)
		if ferr == nil {
			return nil
		}
		if !errors.Is(ferr, messaging.ErrNotFound) {
			deferred = ferr
		}
	}
	if deferred != nil {
		return deferred
	}
	return messaging.ErrNotFound
}

// Thread returns the envelopes sharing a ThreadID, chronologically.
//
// A thread carries no authority and may legitimately span Stores (a request
// sent to a peer plus replies received locally), so the Router queries the
// local Store and every foreign route, then merges the results. Envelopes
// are de-duplicated by ID and ordered by CreatedAt (ties broken by ID),
// matching the local Store's ordering. A non-nil error from any Store fails
// the whole call — a partial thread view is never silently returned.
//
// For a standalone install (no foreign routes) this is a pure passthrough.
func (r *Router) Thread(ctx context.Context, threadID string, f messaging.Filter) ([]messaging.Envelope, error) {
	merged, err := r.local.Thread(ctx, threadID, f)
	if err != nil {
		return nil, err
	}
	if len(r.foreign) > 0 {
		merged = append([]messaging.Envelope(nil), merged...)
		for _, store := range r.foreign {
			part, ferr := store.Thread(ctx, threadID, f)
			if ferr != nil {
				return nil, ferr
			}
			merged = append(merged, part...)
		}
		merged = dedupSortEnvelopes(merged)
		if f.Limit > 0 && len(merged) > f.Limit {
			merged = merged[:f.Limit]
		}
	}
	return merged, nil
}

// dedupSortEnvelopes orders envelopes by (CreatedAt, ID) and drops duplicate
// IDs — keeping the first occurrence — so a merged cross-Store thread reads
// the same as a single-Store one.
func dedupSortEnvelopes(envs []messaging.Envelope) []messaging.Envelope {
	sort.SliceStable(envs, func(i, j int) bool {
		if envs[i].CreatedAt.Equal(envs[j].CreatedAt) {
			return envs[i].ID < envs[j].ID
		}
		return envs[i].CreatedAt.Before(envs[j].CreatedAt)
	})
	seen := make(map[string]struct{}, len(envs))
	out := envs[:0]
	for _, e := range envs {
		if _, dup := seen[e.ID]; dup {
			continue
		}
		seen[e.ID] = struct{}{}
		out = append(out, e)
	}
	return out
}

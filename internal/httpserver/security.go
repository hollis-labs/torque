package httpserver

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/hollis-labs/libs/security/auth"
)

// Security controls who may call the HTTP API. The zero value is the
// loopback-only default: no token, and only loopback browser origins.
type Security struct {
	// Token, when set, is required as "Authorization: Bearer <token>" on
	// every /api/v1 and /mcp request, loopback callers included. It is mandatory when
	// serve binds a non-loopback address (see ValidateBind).
	Token string
	// CORSOrigins are extra browser origins (scheme://host[:port]) allowed
	// to call the API. Loopback origins are always allowed.
	CORSOrigins []string
	// Guard, optional, adds go-auth bearer verification (Tether-issued JWTs
	// and the static token). In off and permissive modes it only attributes
	// verified callers; the Token / loopback rules below still decide
	// admission for everyone else. Under enforce it alone decides.
	Guard *auth.Guard
}

// WithSecurity installs the API's auth and origin policy. Like
// WithSessions, call it before the listener accepts connections.
func (s *Server) WithSecurity(sec Security) *Server {
	s.security = sec
	return s
}

// DefaultAddr is the listen address serve uses when --addr is empty.
func DefaultAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
}

// ValidateBind refuses to expose the API beyond loopback without a token.
// An empty host (":8990") listens on every interface and counts as
// non-loopback.
func ValidateBind(addr, token string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	if token == "" && !IsLoopbackHost(host) {
		return fmt.Errorf("refusing to listen on %q without a token: set --token or TORQUE_API_TOKEN, or bind to 127.0.0.1", addr)
	}
	return nil
}

// IsLoopbackHost reports whether host is localhost or a loopback IP. An
// empty host is not loopback: it means every interface.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// originAllowed reports whether a browser Origin may call the API: any
// loopback origin, or one listed in Security.CORSOrigins.
func (s *Server) originAllowed(origin string) bool {
	for _, o := range s.security.CORSOrigins {
		if strings.EqualFold(strings.TrimRight(o, "/"), origin) {
			return true
		}
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return IsLoopbackHost(u.Hostname())
}

// corsMiddleware echoes allowed origins and refuses requests from any other
// browser origin. Refusing (rather than only withholding the CORS headers)
// matters because a cross-site form POST is a "simple" request the browser
// sends without a preflight: without this, any web page could create and
// dispatch tasks against a loopback Torque. Requests without an Origin
// header (CLI, server-side clients, same-origin GETs) are unaffected.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Add("Vary", "Origin")
			if !s.originAllowed(origin) {
				writeError(w, http.StatusForbidden, "origin not allowed: set --cors-origin or TORQUE_CORS_ORIGINS to allow it")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token, Accept, Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireToken rejects /api/v1 and /mcp requests that do not carry the bearer token.
// Preflight requests pass through so CORS can answer them.
//
// With no token configured the API is loopback-only, and requests must
// name a loopback Host. That refuses DNS rebinding, where a hostile page
// re-points its own domain at 127.0.0.1 and reads the API as same-origin
// (no Origin header, so corsMiddleware cannot see it).
func (s *Server) requireToken(next http.Handler) http.Handler {
	legacy := s.requireLegacyToken(next)
	// routes() runs before WithSecurity, so the guard is looked up per
	// request, and its middleware built once on first use.
	var once sync.Once
	var guarded http.Handler
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g := s.security.Guard
		if g == nil {
			legacy.ServeHTTP(w, r)
			return
		}
		once.Do(func() {
			// A caller the guard verified (JWT, or the static token as an
			// API key) is admitted; everyone else falls to the legacy rules
			// unless the guard is enforcing, in which case it has already
			// rejected them.
			guarded = g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Off and permissive stamp an under-scoped verified caller
				// without rejecting it, so admission also needs the scope
				// this method requires; otherwise the legacy rules decide.
				if p, ok := auth.FromContext(r.Context()); ok && p.Verified && (requiredScope(r) == "" || p.HasScope(requiredScope(r))) {
					next.ServeHTTP(w, r)
					return
				}
				legacy.ServeHTTP(w, r)
			}))
		})
		guarded.ServeHTTP(w, r)
	})
}

func (s *Server) requireLegacyToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.security.Token
		if token == "" {
			if !IsLoopbackHost(requestHostname(r)) {
				writeError(w, http.StatusForbidden, "host not allowed: without TORQUE_API_TOKEN the API answers only loopback hosts (127.0.0.1, localhost)")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		want := []byte("Bearer " + token)
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="torque"`)
			writeError(w, http.StatusUnauthorized, "unauthorized: send Authorization: Bearer <TORQUE_API_TOKEN>")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestHostname is the Host header without its port.
func requestHostname(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.Host); err == nil {
		return host
	}
	return r.Host
}

// requiredScope is the scope scopeTable demands for r at the transport.
// /mcp needs none (each tool call is scoped separately).
func requiredScope(r *http.Request) string {
	if r.URL.Path == "/mcp" {
		return ""
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return AuthApp + ".read"
	}
	return AuthApp + ".write"
}

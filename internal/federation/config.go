package federation

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	tqmsg "github.com/hollis-labs/torque/internal/messaging"
)

// Config is the federation config block (ADR-0002 §7). It is a structured
// file section — peer lists and certificate paths do not fit the env-var
// convention of internal/config — loaded by LoadConfig from a JSON file. When
// the file is absent federation is disabled; see LoadConfig and Enable.
//
// The exported fields are the file schema. The unexported fields are
// populated by finalize() once the config validates: the loaded TLS identity,
// its parsed leaf certificate, and the inbound peer registry.
type Config struct {
	// ListenAddr is the bind address for the dedicated federation TLS
	// listener (ADR-0002 §5) — e.g. ":8443" or "127.0.0.1:8443". This
	// listener is separate from the plaintext /api/v1/* GUI surface.
	ListenAddr string `json:"listen_addr"`

	// LocalAuthorities enumerates the authorities this install homes
	// (localAuthorities(S), ADR-0002 §4). It feeds both the authority-routing
	// Router's strict mode and the federation authorization layer's routing
	// invariant. At least one is required when federation is configured — an
	// install cannot tell local from foreign without declaring its identity.
	LocalAuthorities []string `json:"local_authorities"`

	// Identity is the path pair for this install's federation certificate +
	// private key (ADR-0002 §3). The same certificate is presented as the TLS
	// client cert when dialing out and the TLS server cert when accepting.
	Identity IdentityConfig `json:"identity"`

	// Peers is the inbound peer registry (ADR-0002 §7): who may connect, the
	// fingerprints that identify them, and the authorities each may act for.
	Peers []PeerConfig `json:"peers"`

	// ForeignRoutes is the outbound foreign-authority → peer-endpoint registry
	// (ADR-0001 §4, extended by ADR-0002 §7 to carry the server-cert pins).
	ForeignRoutes []RouteConfig `json:"foreign_routes"`

	sourceDir string          // dir of the config file — anchors relative cert paths
	identity  tls.Certificate // loaded by finalize()
	leaf      *x509.Certificate
	peers     *PeerRegistry
}

// IdentityConfig is the path pair for this install's federation identity. A
// relative path is resolved against the directory holding the config file.
type IdentityConfig struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// PeerConfig is one inbound peer: a label, the pinned SHA-256 fingerprints of
// its certificate (a list, for rotation overlap — ADR-0002 §3), and the
// authorities it is registered authoritative for.
type PeerConfig struct {
	Label        string   `json:"label"`
	Fingerprints []string `json:"fingerprints"`
	Authorities  []string `json:"authorities"`
}

// RouteConfig is one outbound foreign route: the foreign authority, the peer
// endpoint that homes it, and the pinned SHA-256 fingerprint(s) of that
// endpoint's server certificate.
type RouteConfig struct {
	Authority  string   `json:"authority"`
	Endpoint   string   `json:"endpoint"`
	ServerPins []string `json:"server_pins"`
}

// LoadConfig reads and validates the federation config at path.
//
//   - Absent file → (nil, nil): federation is disabled. This is the
//     standalone guarantee — no config, no listener, no surface.
//   - Present and valid → (*Config, nil).
//   - Present but malformed/invalid → (nil, error): federation is a security
//     boundary, so a broken config fails loudly rather than silently
//     degrading to an open or disabled state.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("federation: read config %s: %w", path, err)
	}

	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // a typo'd key in a security config is an error, not a default
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("federation: parse config %s: %w", path, err)
	}
	c.sourceDir = filepath.Dir(path)
	if err := c.finalize(); err != nil {
		return nil, fmt.Errorf("federation: invalid config %s: %w", path, err)
	}
	return &c, nil
}

// finalize validates the parsed config and populates the derived fields:
// loads + parses the TLS identity, and builds the inbound peer registry.
func (c *Config) finalize() error {
	if c.ListenAddr == "" {
		return errors.New("listen_addr is required")
	}
	if len(c.LocalAuthorities) == 0 {
		return errors.New("local_authorities must list at least one authority this install homes")
	}
	local := make(map[string]struct{}, len(c.LocalAuthorities))
	for _, a := range c.LocalAuthorities {
		if a == "" {
			return errors.New("local_authorities entries must be non-empty")
		}
		if _, dup := local[a]; dup {
			return fmt.Errorf("local authority %q is listed twice", a)
		}
		local[a] = struct{}{}
	}

	// Identity: load + parse the leaf. A bad key pair fails at config time,
	// not on the first handshake.
	if c.Identity.CertFile == "" || c.Identity.KeyFile == "" {
		return errors.New("identity.cert_file and identity.key_file are both required")
	}
	cert, err := tls.LoadX509KeyPair(c.resolve(c.Identity.CertFile), c.resolve(c.Identity.KeyFile))
	if err != nil {
		return fmt.Errorf("load identity key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse identity certificate: %w", err)
	}
	cert.Leaf = leaf
	c.identity = cert
	c.leaf = leaf

	// Inbound peer registry.
	peers, err := NewPeerRegistry(c.Peers)
	if err != nil {
		return err
	}
	c.peers = peers

	// Outbound foreign routes.
	seen := make(map[string]struct{}, len(c.ForeignRoutes))
	for _, rt := range c.ForeignRoutes {
		if rt.Authority == "" {
			return errors.New("a foreign route has an empty authority")
		}
		if _, dup := seen[rt.Authority]; dup {
			return fmt.Errorf("foreign route authority %q is declared twice", rt.Authority)
		}
		seen[rt.Authority] = struct{}{}
		if _, clash := local[rt.Authority]; clash {
			return fmt.Errorf("authority %q is both a local authority and a foreign route", rt.Authority)
		}
		if err := validateEndpoint(rt.Endpoint); err != nil {
			return fmt.Errorf("foreign route %q: %w", rt.Authority, err)
		}
		if len(rt.ServerPins) == 0 {
			return fmt.Errorf("foreign route %q: at least one server_pins fingerprint is required (the hop is pinned, ADR-0002 §3)", rt.Authority)
		}
		for _, pin := range rt.ServerPins {
			if _, err := ValidateFingerprint(pin); err != nil {
				return fmt.Errorf("foreign route %q: %w", rt.Authority, err)
			}
		}
	}
	return nil
}

// validateEndpoint checks a foreign-route endpoint is a usable absolute URL.
// The federation hop is mTLS, so the scheme MUST be https — a plaintext
// endpoint cannot carry a client certificate.
func validateEndpoint(endpoint string) error {
	if endpoint == "" {
		return errors.New("endpoint is required")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint %q: %w", endpoint, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("endpoint %q: scheme must be https (the federation hop is mTLS)", endpoint)
	}
	if u.Host == "" {
		return fmt.Errorf("endpoint %q: missing host", endpoint)
	}
	return nil
}

// resolve turns a possibly-relative path from the config file into an
// absolute one, anchored at the config file's directory.
func (c *Config) resolve(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(c.sourceDir, path)
}

// buildForeignRoutes materializes the outbound foreign routes as
// internal/messaging ForeignRoute values, each carrying an mTLS *http.Client
// that presents this install's identity and pins the peer's server cert
// (ADR-0002 §7/§8.7-8). The result is fed to messaging.NewRegistry, which the
// authority-routing Router consumes.
func (c *Config) buildForeignRoutes() ([]tqmsg.ForeignRoute, error) {
	out := make([]tqmsg.ForeignRoute, 0, len(c.ForeignRoutes))
	for _, rt := range c.ForeignRoutes {
		client, err := ClientHTTPClient(c.identity, rt.ServerPins)
		if err != nil {
			return nil, fmt.Errorf("federation: foreign route %q: %w", rt.Authority, err)
		}
		pins := make([]string, 0, len(rt.ServerPins))
		for _, p := range rt.ServerPins {
			pins = append(pins, NormalizeFingerprint(p))
		}
		out = append(out, tqmsg.ForeignRoute{
			Authority: rt.Authority,
			Endpoint:  rt.Endpoint,
			Pins:      pins,
			Client:    client,
		})
	}
	return out, nil
}

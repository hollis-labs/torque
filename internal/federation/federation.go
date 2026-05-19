package federation

import (
	"context"

	gomsg "github.com/hollis-labs/go-messaging"

	tqmsg "github.com/hollis-labs/torque/internal/messaging"
)

// Federation is a fully-configured, ready-to-run federation subsystem: the
// inbound mTLS surface plus the material the authority-routing Router needs
// for the outbound hop. It is the single seam cmd/torque/serve.go integrates.
type Federation struct {
	cfg    *Config
	server *Server
}

// Enable loads the federation config at path and, if present, builds the
// federation subsystem against localStore (the install's local SQLite Store).
//
//   - File absent → (nil, nil): federation is disabled. The caller treats a
//     nil *Federation as "standalone" — no listener, no foreign routes, the
//     Router stays a catch-all passthrough. This is the standalone guarantee
//     (ADR-0001/0002).
//   - File present and valid → (*Federation, nil).
//   - File present but invalid → (nil, error): fail loudly.
func Enable(path string, localStore gomsg.Store) (*Federation, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	return &Federation{
		cfg:    cfg,
		server: NewServer(localStore, cfg.peers, cfg.LocalAuthorities),
	}, nil
}

// ListenAddr is the bind address of the federation TLS listener.
func (f *Federation) ListenAddr() string { return f.cfg.ListenAddr }

// LocalAuthorities is the set of authorities this install homes — fed to
// messaging.RouterConfig.LocalAuthorities to put the Router in strict mode.
func (f *Federation) LocalAuthorities() []string {
	return append([]string(nil), f.cfg.LocalAuthorities...)
}

// ForeignRoutes builds the outbound foreign routes — each carrying an mTLS
// *http.Client — for messaging.NewRegistry. The Router's foreign branch is
// constructed from these (ADR-0002 §8.8).
func (f *Federation) ForeignRoutes() ([]tqmsg.ForeignRoute, error) {
	return f.cfg.buildForeignRoutes()
}

// Run serves the federation mTLS listener until ctx is cancelled.
func (f *Federation) Run(ctx context.Context) error {
	return f.server.Run(ctx, f.cfg.ListenAddr, ServerTLSConfig(f.cfg.identity, f.cfg.peers))
}

// Server exposes the underlying federation HTTP surface (for tests and
// embedding).
func (f *Federation) Server() *Server { return f.server }

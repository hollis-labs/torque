package federation_e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"math/big"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/broker"
	fed "github.com/hollis-labs/torque/internal/federation"
	"github.com/hollis-labs/torque/internal/httpserver"
	tqmsg "github.com/hollis-labs/torque/internal/messaging"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
)

// install is one federated app in the mesh — Torque, Nanite, or Tether. It is
// a complete, production-shaped stack: a SQLite-backed local Store, a typed
// broker, an authority-routing Router with mTLS foreign routes to every peer,
// and an inbound federation mTLS listener. With guiURL set it also exposes the
// /api/v1/messages HTTP surface the messaging GUI client consumes.
type install struct {
	name        string // "torque" / "nanite" / "tether"
	authority   string // the URN authority segment this install homes
	identity    tlsIdentity
	fingerprint string

	db       *sql.DB
	msgStore *tqmsg.Store // the local SQLite messaging Store

	fedServer *fed.Server      // inbound federation surface (mTLS)
	fedTS     *httptest.Server // the mTLS listener it is served on

	router *tqmsg.Router  // authority-routing decorator: local + foreign hops
	broker *broker.Broker // typed envelope layer over the Router

	guiURL string // base URL of the /api/v1/* GUI HTTP surface; "" when unset
}

// agent returns the canonical agent address homed by this install.
func (in *install) agent() gomsg.Address {
	return gomsg.Address{Kind: gomsg.KindAgent, Authority: in.authority, ID: in.name + "-agent"}
}

// mesh is a fully-wired set of federated installs keyed by app name.
type mesh map[string]*install

// meshAppNames is the fixed three-app topology — Torque ⇄ Nanite ⇄ Tether.
var meshAppNames = []string{"torque", "nanite", "tether"}

// newMesh stands up the three-app federation mesh: every install homes its own
// authority, pins every peer's certificate, and registers a foreign route to
// every peer. Any name passed in withGUI additionally gets the /api/v1/*
// HTTP surface wired over its federation Router.
//
// Build order is three phases because the wiring is cyclic: a Router needs its
// peers' listener URLs, and a listener needs its peers' certificate
// fingerprints. Phase 1 mints identities + Stores, phase 2 starts every
// listener (URLs now known), phase 3 builds every Router against those URLs.
func newMesh(t *testing.T, withGUI ...string) mesh {
	t.Helper()
	m := make(mesh, len(meshAppNames))

	// Phase 1 — identity, database, local Store.
	for _, name := range meshAppNames {
		in := &install{name: name, authority: name}
		in.identity = newIdentity(t)
		in.fingerprint = fed.Fingerprint(in.identity.leaf)
		in.db = newDB(t)
		in.msgStore = tqmsg.NewStore(in.db)
		m[name] = in
	}

	// Phase 2 — inbound peer registry + federation mTLS listener. The peer
	// registry pins every OTHER install's cert and authorizes it for exactly
	// its own authority — the trust boundary the cross-app tests probe.
	for _, name := range meshAppNames {
		in := m[name]
		var peerCfgs []fed.PeerConfig
		for _, peer := range m.others(name) {
			peerCfgs = append(peerCfgs, fed.PeerConfig{
				Label:        peer.name,
				Fingerprints: []string{peer.fingerprint},
				Authorities:  []string{peer.authority},
			})
		}
		peers, err := fed.NewPeerRegistry(peerCfgs)
		require.NoError(t, err, "%s: peer registry", name)

		in.fedServer = fed.NewServer(in.msgStore, peers, []string{in.authority})
		ts := httptest.NewUnstartedServer(in.fedServer.Handler())
		ts.TLS = fed.ServerTLSConfig(in.identity.cert, peers)
		ts.StartTLS()
		t.Cleanup(ts.Close)
		in.fedTS = ts
	}

	// Phase 3 — outbound foreign routes + Router + broker. Each route carries
	// an mTLS *http.Client presenting this install's identity and pinning the
	// peer's server cert. The Router is in strict mode (LocalAuthorities set),
	// so a misaddressed authority fails loudly rather than routing wrong.
	for _, name := range meshAppNames {
		in := m[name]
		var routes []tqmsg.ForeignRoute
		for _, peer := range m.others(name) {
			client, err := fed.ClientHTTPClient(in.identity.cert, []string{peer.fingerprint})
			require.NoError(t, err, "%s→%s: mTLS client", name, peer.name)
			routes = append(routes, tqmsg.ForeignRoute{
				Authority: peer.authority,
				Endpoint:  peer.fedTS.URL,
				Client:    client,
			})
		}
		reg, err := tqmsg.NewRegistry(routes...)
		require.NoError(t, err, "%s: foreign-route registry", name)

		router, err := tqmsg.NewRouterWithRegistry(in.msgStore, []string{in.authority}, reg)
		require.NoError(t, err, "%s: router", name)
		in.router = router
		in.broker = broker.New(router, nil)
	}

	// Optional — the GUI HTTP surface, wired over the federation Router so a
	// GUI-originated Send/Get is routed exactly as an agent's would be.
	for _, name := range withGUI {
		in := m[name]
		store, err := sqlstore.New(in.db, "sqlite")
		require.NoError(t, err, "%s: sqlstore", name)
		handler := httpserver.New(service.New(store), nil)
		handler.SetMessaging(in.router)
		ts := httptest.NewServer(handler)
		t.Cleanup(ts.Close)
		in.guiURL = ts.URL
	}

	return m
}

// others returns the installs other than name, in mesh order.
func (m mesh) others(name string) []*install {
	out := make([]*install, 0, len(m)-1)
	for _, n := range meshAppNames {
		if n != name {
			out = append(out, m[n])
		}
	}
	return out
}

// newDB opens a fresh file-backed SQLite database with every Torque migration
// applied — a real schema, including 022_messages, shared by the messaging
// Store and (when a GUI is wired) the service layer. A file path rather than
// :memory: so the *sql.DB connection pool sees one consistent database.
func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "federation-e2e.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, migrations.Run(db))
	return db
}

// tlsIdentity is a self-signed federation identity plus its parsed leaf — the
// shape ADR-0002 §3 pins. The same certificate is presented as the TLS server
// cert when accepting and the client cert when dialing out.
type tlsIdentity struct {
	cert tls.Certificate
	leaf *x509.Certificate
}

// newIdentity mints a one-year-valid, self-signed ECDSA P-256 federation
// identity usable as both server and client certificate — the federation
// trust model pins the leaf directly, so no CA chain is built.
func newIdentity(t *testing.T) tlsIdentity {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: "torque-federation-e2e"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	return tlsIdentity{
		cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf},
		leaf: leaf,
	}
}

package federation

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	gomsg "github.com/hollis-labs/go-messaging"

	"github.com/hollis-labs/torque/internal/broker"
)

// FederationBasePath is the route prefix the federation surface is mounted at
// (ADR-0002 §5). It matches messaging.DefaultFederationBasePath, the prefix
// the outbound HTTPStore joins to a peer endpoint.
const FederationBasePath = "/federation/v1/messages"

// maxRequestBytes bounds a federation request body. The envelope payload cap
// is broker.MaxPayloadBytes; the doubling is slack for the rest of the
// envelope JSON (addresses, metadata, framing). A body past this is rejected
// before it is fully buffered.
const maxRequestBytes = 2 * broker.MaxPayloadBytes

// Server is the federation HTTP surface (ADR-0002 §5): a dedicated, mTLS-only
// listener exposing push-delivery + reply-correlation routes — Send, Get,
// Thread, Consume, Cancel — over the install's *local* messaging Store.
//
// It wraps the local Store directly, never the authority-routing Router: a
// federated request has already been routed to this install because this
// install homes the governing authority, and the authorization layer re-checks
// that on arrival (ADR-0002 §4 routing invariant). Wrapping the Router would
// risk re-dispatching a request straight back out — the relay this design
// forbids. Inbox and Subscribe are intentionally not mounted.
type Server struct {
	store   gomsg.Store
	peers   *PeerRegistry
	authz   *authorizer
	handler http.Handler
}

// NewServer builds the federation surface over localStore. peers is the
// inbound peer registry; localAuthorities are the authorities this install
// homes (ADR-0002 §4).
func NewServer(localStore gomsg.Store, peers *PeerRegistry, localAuthorities []string) *Server {
	s := &Server{
		store: localStore,
		peers: peers,
		authz: newAuthorizer(localAuthorities),
	}
	s.handler = s.routes()
	return s
}

// Handler exposes the federation HTTP handler — for embedding in a TLS
// listener (Run) and for tests.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(s.identifyPeer)

	// Static `thread` is registered alongside the `{id}` wildcard exactly as
	// the local /api/v1/messages/* routes do; chi resolves the static segment
	// first, so there is no ambiguity.
	r.Route(FederationBasePath, func(r chi.Router) {
		r.Post("/", s.handleSend)
		r.Get("/thread/{thread_id}", s.handleThread)
		r.Get("/{id}", s.handleGet)
		r.Post("/{id}/consume", s.handleConsume)
		r.Post("/{id}/cancel", s.handleCancel)
	})
	return r
}

// Run starts the federation TLS listener on addr and serves until ctx is
// cancelled. The listener is mTLS — tlsConf MUST be a ServerTLSConfig. It is
// physically separate from Torque's plaintext HTTP server (ADR-0002 §5).
func (s *Server) Run(ctx context.Context, addr string, tlsConf *tls.Config) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("federation: listen %s: %w", addr, err)
	}
	hs := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := hs.Shutdown(shutdownCtx); err != nil {
			_ = hs.Close()
		}
	}()
	log.Printf("[federation] mTLS listener on %s — %d local authorities, %d peers (%d pinned certs)",
		addr, s.authz.count(), s.peers.PeerCount(), s.peers.PinCount())

	err = hs.Serve(tls.NewListener(ln, tlsConf))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// --- peer identification middleware ------------------------------------------

type ctxKey struct{}

var peerKey ctxKey

// identifiedPeer is the peer resolved for one request: the *Peer plus the
// fingerprint of the certificate that matched (audit log material).
type identifiedPeer struct {
	peer        *Peer
	fingerprint string
}

// identifyPeer resolves the connection's verified client certificate to a
// registered Peer (ADR-0002 §5 middleware step 2). The TLS handshake's
// VerifyPeerCertificate has already pin-matched and expiry-checked the cert;
// this re-derives the *Peer so the handlers have the caller's identity. A
// connection with no client cert, or one whose fingerprint is not pinned, is
// rejected 403 — fail-closed.
func (s *Server) identifyPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			writeError(w, http.StatusForbidden, "federation: no client certificate")
			return
		}
		fp := Fingerprint(r.TLS.PeerCertificates[0])
		peer, ok := s.peers.Lookup(fp)
		if !ok {
			log.Printf("[federation] reject: unpinned client certificate %s", fp)
			writeError(w, http.StatusForbidden, "federation: client certificate not pinned")
			return
		}
		ctx := context.WithValue(r.Context(), peerKey, &identifiedPeer{peer: peer, fingerprint: fp})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func peerFromContext(ctx context.Context) (*identifiedPeer, bool) {
	id, ok := ctx.Value(peerKey).(*identifiedPeer)
	return id, ok
}

// --- handlers ----------------------------------------------------------------

// handleSend — POST /federation/v1/messages. Body is a go-messaging Envelope
// (the shape messaging.HTTPStore.Send emits). Enforces the payload cap and the
// ADR-0002 §4 authorization for a Send before touching the Store.
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	id, _ := peerFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	var env gomsg.Envelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "federation: request body exceeds limit")
			return
		}
		writeError(w, http.StatusBadRequest, "federation: invalid envelope JSON: "+err.Error())
		return
	}
	if env.Kind == "" || env.From.IsZero() || env.To.IsZero() {
		writeError(w, http.StatusUnprocessableEntity, "federation: envelope kind, from and to are required")
		return
	}
	if len(env.Payload) > broker.MaxPayloadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "federation: envelope payload exceeds MaxPayloadBytes")
		return
	}
	if err := s.authz.authorizeSend(id.peer, env); err != nil {
		s.rejectAuthz(w, id, OpSend, env, err)
		return
	}

	out, err := s.store.Send(r.Context(), env)
	if err != nil {
		if errors.Is(err, gomsg.ErrPresetLifecycle) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(id, OpSend, out, true, "delivered")
	writeJSON(w, http.StatusCreated, out)
}

// handleGet — GET /federation/v1/messages/{id}. The envelope is fetched first
// so the §4 access check can run against its real From/To.
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id, _ := peerFromContext(r.Context())
	env, ok := s.fetchForAccess(w, r, OpGet, id)
	if !ok {
		return
	}
	s.audit(id, OpGet, env, true, "read")
	writeJSON(w, http.StatusOK, env)
}

// handleThread — GET /federation/v1/messages/thread/{thread_id}. A peer may
// inspect a thread it is a party to (ADR-0002 §4). An empty thread leaks
// nothing and returns an empty list.
func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	id, _ := peerFromContext(r.Context())
	threadID := chi.URLParam(r, "thread_id")
	if threadID == "" {
		writeError(w, http.StatusBadRequest, "federation: missing thread_id")
		return
	}
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "federation: "+err.Error())
		return
	}
	envs, err := s.store.Thread(r.Context(), threadID, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(envs) > 0 && !s.peerIsThreadParty(id.peer, envs) {
		probe := gomsg.Envelope{ThreadID: threadID}
		s.audit(id, OpThread, probe, false, "peer is not a party to the thread")
		writeError(w, http.StatusForbidden, errForbidden.Error())
		return
	}
	log.Printf("[federation] peer=%s op=thread thread=%s envelopes=%d decision=allow",
		id.peer.Label, threadID, len(envs))
	writeJSON(w, http.StatusOK, map[string]any{"messages": envs})
}

// handleConsume — POST /federation/v1/messages/{id}/consume, body
// {"recipient": <urn>}.
func (s *Server) handleConsume(w http.ResponseWriter, r *http.Request) {
	id, _ := peerFromContext(r.Context())
	var req struct {
		Recipient string `json:"recipient"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "federation: invalid JSON body: "+err.Error())
		return
	}
	if req.Recipient == "" {
		writeError(w, http.StatusUnprocessableEntity, "federation: recipient is required")
		return
	}
	recipient, err := gomsg.ParseURN(req.Recipient)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "federation: recipient: "+err.Error())
		return
	}
	env, ok := s.fetchForAccess(w, r, OpConsume, id)
	if !ok {
		return
	}
	if err := s.store.Consume(r.Context(), env.ID, recipient); err != nil {
		if errors.Is(err, gomsg.ErrNotFound) {
			writeError(w, http.StatusNotFound, "federation: message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(id, OpConsume, env, true, "consumed by "+recipient.URN())
	w.WriteHeader(http.StatusNoContent)
}

// handleCancel — POST /federation/v1/messages/{id}/cancel.
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id, _ := peerFromContext(r.Context())
	env, ok := s.fetchForAccess(w, r, OpCancel, id)
	if !ok {
		return
	}
	if err := s.store.Cancel(r.Context(), env.ID); err != nil {
		if errors.Is(err, gomsg.ErrNotFound) {
			writeError(w, http.StatusNotFound, "federation: message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(id, OpCancel, env, true, "cancelled")
	w.WriteHeader(http.StatusNoContent)
}

// --- shared helpers ----------------------------------------------------------

// fetchForAccess loads the {id} envelope and runs the §4 access authorization
// for an envelope-targeting op (Get/Consume/Cancel). It writes the error
// response itself on any failure; the bool reports whether the caller may
// proceed.
func (s *Server) fetchForAccess(w http.ResponseWriter, r *http.Request, op Op, id *identifiedPeer) (gomsg.Envelope, bool) {
	msgID := chi.URLParam(r, "id")
	if msgID == "" {
		writeError(w, http.StatusBadRequest, "federation: missing id")
		return gomsg.Envelope{}, false
	}
	env, err := s.store.Get(r.Context(), msgID)
	if err != nil {
		if errors.Is(err, gomsg.ErrNotFound) {
			writeError(w, http.StatusNotFound, "federation: message not found")
			return gomsg.Envelope{}, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return gomsg.Envelope{}, false
	}
	if err := s.authz.authorizeEnvelopeAccess(op, id.peer, env); err != nil {
		s.rejectAuthz(w, id, op, env, err)
		return gomsg.Envelope{}, false
	}
	return env, true
}

// peerIsThreadParty reports whether peer is a party to at least one envelope
// in the thread.
func (s *Server) peerIsThreadParty(peer *Peer, envs []gomsg.Envelope) bool {
	for _, e := range envs {
		if peer.IsAuthoritative(e.From.Authority) || peer.IsAuthoritative(e.To.Authority) {
			return true
		}
	}
	return false
}

// rejectAuthz maps an authorization error to its HTTP status (ADR-0002 §5: a
// trust-boundary failure is 403 and is NOT leaked as 404; an unroutable /
// not-homed authority is 404) and audit-logs the denial.
func (s *Server) rejectAuthz(w http.ResponseWriter, id *identifiedPeer, op Op, env gomsg.Envelope, err error) {
	s.audit(id, op, env, false, err.Error())
	if errors.Is(err, errNotHomed) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusForbidden, err.Error())
}

// audit emits the ADR-0002 §5 forensic record for one federated request:
// peer identity (label + cert fingerprint), operation, governing authority,
// envelope From/To, and the allow/deny decision with its reason.
func (s *Server) audit(id *identifiedPeer, op Op, env gomsg.Envelope, allowed bool, reason string) {
	decision := "deny"
	if allowed {
		decision = "allow"
	}
	label, fp := "<unknown>", "<none>"
	if id != nil {
		label, fp = id.peer.Label, shortFingerprint(id.fingerprint)
	}
	log.Printf("[federation] peer=%s cert=%s op=%s authority=%s from=%s to=%s decision=%s reason=%q",
		label, fp, op, s.authz.governingAuthority(op, env), env.From.URN(), env.To.URN(), decision, reason)
}

func shortFingerprint(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

// parseFilter renders the messaging.Filter query messaging.HTTPStore encodes
// (repeated kind/channel, thread_id, limit) — mirrors the local
// /api/v1/messages handler's parseFilter.
func parseFilter(r *http.Request) (gomsg.Filter, error) {
	q := r.URL.Query()
	var f gomsg.Filter
	for _, v := range q["kind"] {
		if v != "" {
			f.Kind = append(f.Kind, gomsg.Kind(v))
		}
	}
	for _, v := range q["channel"] {
		if v != "" {
			f.Channel = append(f.Channel, gomsg.Channel(v))
		}
	}
	if v := q.Get("thread_id"); v != "" {
		f.ThreadID = v
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return gomsg.Filter{}, errors.New("limit must be a non-negative integer")
		}
		f.Limit = n
	}
	return f, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

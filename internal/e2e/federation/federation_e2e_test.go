package federation_e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/broker"
	tqmsg "github.com/hollis-labs/torque/internal/messaging"
	"github.com/hollis-labs/torque/internal/runtime/steering"
)

// payload renders s as a JSON envelope payload.
func payload(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// userAddr is a human operator on the given install.
func userAddr(authority string) gomsg.Address {
	return gomsg.Address{Kind: gomsg.KindUser, Authority: authority, ID: "operator"}
}

// TestE2E_StandaloneInstall_RoutesAllLocally proves the standalone guarantee
// (ADR-0001 §4): a Torque-only install registers no foreign routes, so the
// authority-routing Router is a catch-all passthrough and messaging works
// fully — for any authority — with zero federation configuration. This is the
// "works without Tether" property.
func TestE2E_StandaloneInstall_RoutesAllLocally(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	store := tqmsg.NewStore(db)

	// nil registry + nil local authorities → catch-all standalone Router.
	router, err := tqmsg.NewRouterWithRegistry(store, nil, nil)
	require.NoError(t, err)
	b := broker.New(router, nil)

	// An authority that WOULD be foreign in a federated install ("tether")
	// still resolves locally here — there is simply no route to send it away.
	sent, err := b.Send(ctx, gomsg.Envelope{
		Kind:    gomsg.MsgKindNotice,
		From:    gomsg.Address{Kind: gomsg.KindAgent, Authority: "torque", ID: "solo"},
		To:      gomsg.Address{Kind: gomsg.KindAgent, Authority: "tether", ID: "would-be-foreign"},
		Payload: payload("standalone delivery"),
	})
	require.NoError(t, err, "standalone Send must never fail for lack of a route")

	got, err := store.Get(ctx, sent.ID)
	require.NoError(t, err, "the envelope landed in the local Store")
	assert.Equal(t, sent.ID, got.ID)
}

// TestE2E_InternalRouting_LocalAuthorityStaysLocal — an envelope addressed to
// the install's own authority is served by the local Store and never crosses
// a federation hop. "Internal" is exactly: the authority is homed here.
func TestE2E_InternalRouting_LocalAuthorityStaysLocal(t *testing.T) {
	ctx := context.Background()
	m := newMesh(t)
	torque, nanite, tether := m["torque"], m["nanite"], m["tether"]

	sent, err := torque.broker.Send(ctx, gomsg.Envelope{
		Kind:    gomsg.MsgKindNotice,
		From:    userAddr("torque"),
		To:      torque.agent(), // authority "torque" — homed locally
		Payload: payload("internal nudge"),
	})
	require.NoError(t, err)

	got, err := torque.msgStore.Get(ctx, sent.ID)
	require.NoError(t, err, "an internal envelope is in the local Store")
	assert.Equal(t, sent.ID, got.ID)

	// It went nowhere near the peers.
	for _, peer := range []*install{nanite, tether} {
		_, err := peer.msgStore.Get(ctx, sent.ID)
		assert.ErrorIs(t, err, gomsg.ErrNotFound,
			"%s must not hold an envelope homed by torque", peer.name)
	}

	// The local recipient drains it from its own inbox.
	inbox, err := torque.broker.Inbox(ctx, torque.agent(), gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	assert.Equal(t, sent.ID, inbox[0].ID)
}

// TestE2E_ExternalRouting_CrossAppHop — an envelope addressed to a FOREIGN
// authority crosses the mTLS federation hop and lands in the peer install's
// Store, and only there. "External" is exactly: the authority is homed by a
// peer. This is the Torque → Nanite cross-app delivery.
func TestE2E_ExternalRouting_CrossAppHop(t *testing.T) {
	ctx := context.Background()
	m := newMesh(t)
	torque, nanite, tether := m["torque"], m["nanite"], m["tether"]

	sent, err := torque.broker.Send(ctx, gomsg.Envelope{
		Kind:    gomsg.MsgKindNotice,
		From:    torque.agent(),
		To:      nanite.agent(), // authority "nanite" — foreign, routed over the hop
		Payload: payload("cross-app hello"),
	})
	require.NoError(t, err, "the federated Send crossed the mTLS hop")

	// Landed on Nanite.
	got, err := nanite.msgStore.Get(ctx, sent.ID)
	require.NoError(t, err, "the envelope is in Nanite's Store")
	assert.Equal(t, "torque", got.From.Authority)
	assert.Equal(t, "nanite", got.To.Authority)

	// NOT on the originating install, nor on the uninvolved peer — the hop
	// moved it, it was not copied.
	_, err = torque.msgStore.Get(ctx, sent.ID)
	assert.ErrorIs(t, err, gomsg.ErrNotFound, "the sender keeps no local copy")
	_, err = tether.msgStore.Get(ctx, sent.ID)
	assert.ErrorIs(t, err, gomsg.ErrNotFound, "an uninvolved peer never sees it")

	// The Nanite agent drains it from its own (local) inbox.
	inbox, err := nanite.broker.Inbox(ctx, nanite.agent(), gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	assert.Equal(t, sent.ID, inbox[0].ID)

	// And the sender can still read it back by ID — Router.Get falls back to
	// the foreign route when the envelope is absent locally.
	viaRouter, err := torque.router.Get(ctx, sent.ID)
	require.NoError(t, err, "cross-app Get resolves over the hop")
	assert.Equal(t, sent.ID, viaRouter.ID)
}

// TestE2E_AgentToAgent_RequestResponseRoundTrip — a Torque orchestrator and a
// Nanite worker exchange a request and a response across the federation hop in
// BOTH directions, and the conversation thread reads coherently even though
// its two envelopes are homed in different installs' Stores.
func TestE2E_AgentToAgent_RequestResponseRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newMesh(t)
	torque, nanite := m["torque"], m["nanite"]
	const threadID = "thread-collab-1"

	// Torque → Nanite: the request crosses the hop.
	req, err := torque.broker.Send(ctx, gomsg.Envelope{
		Kind:     gomsg.MsgKindRequest,
		From:     torque.agent(),
		To:       nanite.agent(),
		ThreadID: threadID,
		Payload:  payload("what is the rollout status?"),
	})
	require.NoError(t, err)

	// Nanite's worker drains the request from its inbox.
	naniteInbox, err := nanite.broker.Inbox(ctx, nanite.agent(), gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, naniteInbox, 1)
	assert.Equal(t, gomsg.MsgKindRequest, naniteInbox[0].Kind)
	assert.Equal(t, threadID, naniteInbox[0].ThreadID)

	// Nanite → Torque: the response crosses the hop back the other way.
	resp, err := nanite.broker.Send(ctx, gomsg.Envelope{
		Kind:      gomsg.MsgKindResponse,
		From:      nanite.agent(),
		To:        torque.agent(),
		ThreadID:  threadID,
		InReplyTo: req.ID,
		Payload:   payload("rollout is green"),
	})
	require.NoError(t, err)

	// Torque's orchestrator drains the response from its inbox.
	torqueInbox, err := torque.broker.Inbox(ctx, torque.agent(), gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, torqueInbox, 1)
	assert.Equal(t, gomsg.MsgKindResponse, torqueInbox[0].Kind)
	assert.Equal(t, resp.ID, torqueInbox[0].ID)

	// The thread spans two Stores — the request homed on Nanite, the response
	// on Torque. Router.Thread merges the local Store with every foreign route
	// so the orchestrator sees the whole conversation in order.
	thread, err := torque.router.Thread(ctx, threadID, gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, thread, 2, "the merged cross-Store thread has both envelopes")
	assert.Equal(t, gomsg.MsgKindRequest, thread[0].Kind, "request is first by CreatedAt")
	assert.Equal(t, gomsg.MsgKindResponse, thread[1].Kind)
}

// TestE2E_UserToAgent_SteeringBridge — a user's notice addressed to a live
// agent session is carried into that session's turn loop by the steering
// bridge (inject-at-turn-boundary, design decision #1), and a notice addressed
// to a session with no live process is left durable for later pickup.
func TestE2E_UserToAgent_SteeringBridge(t *testing.T) {
	ctx := context.Background()
	m := newMesh(t)
	torque := m["torque"]

	liveSession := gomsg.Address{Kind: gomsg.KindSession, Authority: "torque", ID: "SES-LIVE"}
	idleSession := gomsg.Address{Kind: gomsg.KindSession, Authority: "torque", ID: "SES-IDLE"}

	gw := &fakeGateway{live: map[string]string{liveSession.URN(): "SES-LIVE"}}
	// The broker is the bridge's EnvelopeConsumer — a delivered envelope is
	// marked consumed so a later inbox drain does not double-handle it.
	bridge := steering.New(gw, torque.broker)

	// A user steers a RUNNING orchestrator. The notice is persisted through
	// the broker exactly as the GUI compose path would persist it.
	steered, err := torque.broker.Notice(ctx, userAddr("torque"), liveSession,
		payload("please pause the rollout"))
	require.NoError(t, err)

	res := bridge.Deliver(ctx, steered)
	assert.Equal(t, steering.OutcomeDelivered, res.Outcome)
	assert.Equal(t, "SES-LIVE", res.SessionID)
	require.NoError(t, res.Err)

	turns := gw.deliveredTurns()
	require.Len(t, turns, 1, "exactly one turn injected into the live session")
	assert.Equal(t, "SES-LIVE", turns[0].sessionID)
	assert.Contains(t, turns[0].text, "pause the rollout", "the turn carries the notice")

	// Delivered + consumed → the session's durable inbox no longer offers it.
	inbox, err := torque.broker.Inbox(ctx, liveSession, gomsg.Filter{})
	require.NoError(t, err)
	assert.Empty(t, inbox, "a steered envelope is consumed, not left to drain again")

	// A notice for a session with no live process is NOT a failure — it stays
	// durable so an inbox poll or a future catch-up pass can still pick it up.
	parked, err := torque.broker.Notice(ctx, userAddr("torque"), idleSession,
		payload("redeploy when you wake up"))
	require.NoError(t, err)

	res = bridge.Deliver(ctx, parked)
	assert.Equal(t, steering.OutcomeNoLiveSession, res.Outcome)
	require.NoError(t, res.Err)
	assert.Len(t, gw.deliveredTurns(), 1, "no extra turn injected for an idle session")

	parkedInbox, err := torque.broker.Inbox(ctx, idleSession, gomsg.Filter{})
	require.NoError(t, err)
	require.Len(t, parkedInbox, 1, "the un-steered notice is left durable")
	assert.Equal(t, parked.ID, parkedInbox[0].ID)
}

// TestE2E_ThreeAppMesh_AllPairsRoute — every ordered pair of the three apps
// can deliver to every other. With internal routing already covered, this is
// the all-external sweep: six cross-app hops, each landing in the right Store.
func TestE2E_ThreeAppMesh_AllPairsRoute(t *testing.T) {
	ctx := context.Background()
	m := newMesh(t)

	for _, srcName := range meshAppNames {
		for _, dstName := range meshAppNames {
			if srcName == dstName {
				continue
			}
			src, dst := m[srcName], m[dstName]
			t.Run(srcName+"_to_"+dstName, func(t *testing.T) {
				sent, err := src.broker.Send(ctx, gomsg.Envelope{
					Kind:    gomsg.MsgKindNotice,
					From:    src.agent(),
					To:      dst.agent(),
					Payload: payload(srcName + " greets " + dstName),
				})
				require.NoError(t, err, "%s → %s hop", srcName, dstName)

				got, err := dst.msgStore.Get(ctx, sent.ID)
				require.NoError(t, err, "envelope landed in %s's Store", dstName)
				assert.Equal(t, src.authority, got.From.Authority)
				assert.Equal(t, dst.authority, got.To.Authority)
			})
		}
	}
}

// TestE2E_CrossAppTrustBoundary_RejectsImpersonation — the federation hop is
// authenticated, not just routed. A peer may only originate mail for an
// authority the receiving install has registered its certificate for; an
// envelope forging a third app's From.Authority is rejected at the trust
// boundary (ADR-0002 §4) and surfaces as ErrStoreUnavailable (HTTP 403).
func TestE2E_CrossAppTrustBoundary_RejectsImpersonation(t *testing.T) {
	ctx := context.Background()
	m := newMesh(t)
	torque, nanite := m["torque"], m["nanite"]

	// Torque routes by recipient authority, so this still hops to Nanite —
	// but the envelope claims to originate from "tether", an authority Nanite
	// has registered Torque's certificate for. Nanite fails it closed.
	_, err := torque.broker.Send(ctx, gomsg.Envelope{
		Kind:    gomsg.MsgKindNotice,
		From:    gomsg.Address{Kind: gomsg.KindAgent, Authority: "tether", ID: "imposter"},
		To:      nanite.agent(),
		Payload: payload("mail wearing someone else's return address"),
	})
	require.Error(t, err, "an impersonated From.Authority must be rejected")
	assert.ErrorIs(t, err, gomsg.ErrStoreUnavailable,
		"a trust-boundary denial maps to ErrStoreUnavailable (403)")

	// An honest Send from the same install still succeeds — the rejection was
	// the forged authority, not the hop itself.
	ok, err := torque.broker.Send(ctx, gomsg.Envelope{
		Kind:    gomsg.MsgKindNotice,
		From:    torque.agent(),
		To:      nanite.agent(),
		Payload: payload("honest mail"),
	})
	require.NoError(t, err)
	_, err = nanite.msgStore.Get(ctx, ok.ID)
	require.NoError(t, err, "the legitimate envelope crossed the same hop")
}

// TestE2E_GUISurface_MessagingAPIOverFederation — the /api/v1/messages HTTP
// surface the messaging GUI client consumes, wired over a federation Router.
// A GUI-originated Send to a foreign authority crosses the hop transparently;
// a GUI Get resolves it back over the hop; the GUI inbox surface drains a
// local recipient. This is the Go-side contract behind the cross-app GUI.
func TestE2E_GUISurface_MessagingAPIOverFederation(t *testing.T) {
	ctx := context.Background()
	m := newMesh(t, "torque") // only Torque needs the GUI HTTP surface
	torque, nanite := m["torque"], m["nanite"]

	// GUI compose → POST /api/v1/messages, addressed cross-app to Nanite.
	sent := guiSend(t, torque.guiURL, map[string]any{
		"kind":    "request",
		"from":    torque.agent().URN(),
		"to":      nanite.agent().URN(),
		"payload": json.RawMessage(`{"ask":"deploy?"}`),
	})
	require.NotEmpty(t, sent.ID, "the GUI Send returned a Store-assigned ID")

	// The GUI Send routed over the federation hop into Nanite's Store.
	_, err := nanite.msgStore.Get(ctx, sent.ID)
	require.NoError(t, err, "the GUI-originated envelope crossed the hop to Nanite")

	// GUI message detail → GET /api/v1/messages/{id}: the Router resolves a
	// foreign-homed envelope back over the hop.
	fetched := guiGetMessage(t, torque.guiURL, sent.ID)
	assert.Equal(t, sent.ID, fetched.ID)
	assert.Equal(t, "nanite", fetched.To.Authority)

	// GUI inbox → GET /api/v1/messages/inbox: a local recipient's inbox is
	// served from the local Store (inbox is never federated, by design).
	local := guiSend(t, torque.guiURL, map[string]any{
		"kind":    "notice",
		"from":    userAddr("torque").URN(),
		"to":      torque.agent().URN(),
		"payload": json.RawMessage(`{"note":"local"}`),
	})
	inbox := guiInbox(t, torque.guiURL, torque.agent())
	require.Len(t, inbox, 1, "the GUI inbox surface drains the local recipient")
	assert.Equal(t, local.ID, inbox[0].ID)
}

// --- steering test double -----------------------------------------------------

// fakeGateway is a steering.SessionGateway with a fixed live-session table —
// it stands in for agent.Manager so the bridge is exercised without spawning a
// real session. It records every injected turn.
type fakeGateway struct {
	mu    sync.Mutex
	live  map[string]string // recipient URN → live session ID
	turns []injectedTurn
}

type injectedTurn struct {
	sessionID string
	text      string
}

func (g *fakeGateway) LiveSession(addr gomsg.Address) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.live[addr.URN()]
	return id, ok
}

// TaskIDForSession is required by steering.SessionGateway
// (CW-20260519-0065); the federation E2E does not exercise the reminder
// registry path, so we return ok=false uniformly — the bridge then
// skips reminder bookkeeping for these test injections.
func (g *fakeGateway) TaskIDForSession(_ string) (string, bool) {
	return "", false
}

func (g *fakeGateway) SteerTurn(_ context.Context, sessionID, text string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.turns = append(g.turns, injectedTurn{sessionID: sessionID, text: text})
	return nil
}

func (g *fakeGateway) deliveredTurns() []injectedTurn {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]injectedTurn(nil), g.turns...)
}

// --- GUI HTTP helpers ---------------------------------------------------------

// guiSend POSTs an envelope to /api/v1/messages and returns the created
// envelope; it fails the test on any non-201 response.
func guiSend(t *testing.T, base string, body map[string]any) gomsg.Envelope {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	respBody := guiDo(t, http.MethodPost, base+"/api/v1/messages", raw, http.StatusCreated)

	var env gomsg.Envelope
	require.NoError(t, json.Unmarshal(respBody, &env))
	return env
}

// guiGetMessage GETs /api/v1/messages/{id}.
func guiGetMessage(t *testing.T, base, id string) gomsg.Envelope {
	t.Helper()
	respBody := guiDo(t, http.MethodGet, base+"/api/v1/messages/"+url.PathEscape(id), nil, http.StatusOK)

	var env gomsg.Envelope
	require.NoError(t, json.Unmarshal(respBody, &env))
	return env
}

// guiInbox GETs /api/v1/messages/inbox?to=<urn> and returns the drained list.
func guiInbox(t *testing.T, base string, to gomsg.Address) []gomsg.Envelope {
	t.Helper()
	q := url.Values{"to": {to.URN()}}.Encode()
	respBody := guiDo(t, http.MethodGet, base+"/api/v1/messages/inbox?"+q, nil, http.StatusOK)

	var out struct {
		Messages []gomsg.Envelope `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(respBody, &out))
	return out.Messages
}

// guiDo performs one /api/v1/* request, asserts the status, and returns the
// fully-buffered response body. The body is read once so it is available both
// for the status-mismatch message and for the caller's decode.
func guiDo(t *testing.T, method, url string, reqBody []byte, wantStatus int) []byte {
	t.Helper()
	var body io.Reader
	if reqBody != nil {
		body = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequest(method, url, body)
	require.NoError(t, err)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	require.NoError(t, err)
	require.Equal(t, wantStatus, resp.StatusCode, "%s %s: %s", method, url, respBody)
	return respBody
}

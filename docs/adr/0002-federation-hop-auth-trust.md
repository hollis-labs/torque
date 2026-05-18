# ADR-0002 — Federation Hop: Auth & Trust Model (mTLS + Authority Allowlist)

**Status:** Accepted
**Date:** 2026-05-18
**Supersedes:** —
**Amends:** ADR-0001 §4 (the foreign-route registry now carries credentials)
**Plan:** CW-20260518-0038 (Torque Messaging — federated agent/user communication)
**Task:** CW-20260518-0040 (M2 — cross-host federation auth/trust design)
**Source design:** `agent-os/docs/torque-messaging-design.md`

---

## Context

ADR-0001 locked the federation mechanism: an **authority-routing `Store`
decorator** that delegates each `go-messaging` `Store` call by the `authority`
segment of the governing `Address`. A local authority is served by Torque's
SQLite `Store`; a **foreign authority** is served by an HTTP-backed `Store`
that talks to a peer install. ADR-0001 §4 explicitly deferred one thing:

> **Cross-authority auth is OUT OF SCOPE here.** […] the concrete auth
> mechanism (mTLS / signed envelopes / per-authority tokens) is specified by
> task **M2 (`CW-20260518-0040`)** before the Federation phase (ph-4) begins.
> Until M2 lands, the foreign-route registry carries no credentials and no
> install ships foreign routes.

This ADR is M2. It specifies the auth, authorization, and trust-boundary model
for the cross-host hop so that phase ph-4 (Federation) has an implementable
target.

### What the hop actually is

The foreign-route HTTP `Store` is an HTTP **client**; the peer install runs the
HTTP **server**. The transport template is `go-agentmux-client`'s `httpStore`,
which speaks the existing Torque `/api/v1/messages/*` route surface. That
surface today has **no authentication** — it is loopback / same-host only, by
the same assumption that gates the admin endpoints (`internal/httpserver/admin.go`:
"pre-launch, the frontend has no auth layer"). `go-messaging` itself states
auth is out of scope, and `go-agentmux-client` sends no credentials.

So the federation hop crosses a real trust boundary that **nothing in the
current stack defends**. Three things are missing:

1. **Identity** — the server must know *which peer* is connecting.
2. **Authorization** — given a peer, *which authorities* may it act for, and
   *which operations* may it perform.
3. **Trust boundary** — an envelope's `From.Authority` is a claim. A peer must
   not be able to forge mail that appears to originate from an authority it
   does not own (impersonation), nor deliver mail to authorities the server
   does not home (relay abuse), nor drain another entity's mailbox.

### Topology assumption (locked by this ADR)

Federation v1 is a **point-to-point mesh of registered, mutually-vetted
peers**. Every route is explicitly configured on both ends before any traffic
flows (ADR-0001: "a standalone Torque install simply registers no foreign
routes"). There is **no open registration, no discovery, and no third-party
relay** — a peer never forwards another peer's mail. Each install is a
*delivery leaf* for its own authorities. This assumption is load-bearing for
the decision below; §"Consequences" records what changes if it is relaxed.

---

## Decision

### Summary

**The federation hop is secured by mutual TLS (mTLS) for transport and peer
identity, plus an authority-allowlist authorization layer keyed on the pinned
peer certificate.** Signed envelopes and per-authority bearer tokens are
evaluated and rejected for v1; signed envelopes are reserved as a documented
forward-compatible extension for when the point-to-point assumption is relaxed.

### 1. Evaluation of the three candidates

| Dimension | mTLS | Signed envelopes | Per-authority tokens |
|---|---|---|---|
| **What it authenticates** | The *connection* — mutually, both directions | A single *envelope* (`Send` only) | The *connection* — caller only |
| **Covers reads/lifecycle** (`Get`/`Thread`/`Consume`/`Cancel`) | Yes — every call on the connection | **No** — signatures only bind `Send` payloads; reads carry no envelope to sign | Yes |
| **Encrypts the channel** | Yes (TLS) | No — needs TLS *anyway* | No — needs TLS *anyway* |
| **Server authenticated to client** | Yes (mutual) | No | No |
| **Replay resistance** | TLS session integrity; no app-layer replay window | Needs explicit nonce/timestamp + dedup store | Token is a static bearer — replayable if it leaks |
| **Secret-at-rest exposure** | Private key, never transmitted | Signing key, never transmitted | Bearer token *is* transmitted every request |
| **Rotation story** | Standard X.509 rotation; overlap via multi-pin | Key rotation + `kid` distribution | Token rotation + coordinated cutover |
| **New crypto / canonicalization risk** | None — Go stdlib `crypto/tls` | **High** — JSON canonicalization of `Envelope` is a footgun (map ordering, `Metadata`, optional fields) | None |
| **Survives a multi-hop relay** | No (hop-by-hop) | **Yes** (end-to-end) | No (hop-by-hop) |
| **Fit to current code** | `httpStore` already pluggable via `WithHTTPClient`; server adds a `tls.Config` | New signing/verify layer in the decorator or `go-messaging` | New token store + middleware |

**Per-authority tokens — rejected.** A bearer token is a shared secret sent on
every request. It must be stored in cleartext-equivalent form on the client,
is replayable if it leaks (logs, proxies, crash dumps), and still needs TLS
underneath for confidentiality — so it buys nothing mTLS does not, while adding
a secret-distribution and rotation burden. It also does not authenticate the
*server* to the client. mTLS dominates it on every axis that matters here.

**Signed envelopes — rejected for v1, reserved for later.** Per-envelope
signatures are the *correct* mechanism for an **untrusted-intermediary /
multi-hop** topology: they bind origin authenticity end-to-end and survive
relays. But that is not the v1 topology (point-to-point, mutually-vetted
peers). Against the actual v1 problem they are a poor fit: they secure only
`Send` (the `Store` reads and lifecycle calls — `Get`, `Thread`, `Consume`,
`Cancel` — carry no signable envelope and would be left unauthenticated), they
do not encrypt the channel or authenticate the server (TLS is still required
underneath), and they introduce a canonical-serialization requirement over the
`go-messaging` `Envelope` that is a well-known source of subtle, exploitable
bugs. They solve a problem v1 does not have at the cost of real complexity.
They are **reserved** as an additive future layer (§6).

**mTLS — selected.** It authenticates the connection mutually, encrypts it,
covers *every* `Store` operation uniformly, needs no bespoke cryptography (Go
stdlib), introduces no canonicalization surface, and slots into the existing
code with minimal new surface: the client side is a `tls.Config` on the
`http.Client` already injectable via `httpStore`'s `WithHTTPClient`; the server
side is a `tls.Config` on a listener. Its one weakness — it is hop-by-hop, not
end-to-end — is irrelevant under the locked point-to-point topology.

### 2. Decision — mTLS transport + authority-allowlist authorization

The federation hop is secured by **two layers**:

- **Layer 1 — mTLS (identity + transport).** The hop runs over TLS with
  `ClientAuth: RequireAndVerifyClientCert`. Both peers present X.509
  certificates; each is pinned by the other. The verified client certificate
  *is* the peer's identity.
- **Layer 2 — authority allowlist (authorization + trust boundary).** Each
  install holds a **peer registry** mapping a pinned peer certificate to the
  set of `authority` values that peer is permitted to act for. Every federated
  request is checked against this allowlist before any `Store` call runs.

Neither layer alone is sufficient: Layer 1 says *who* is calling; Layer 2 says
*what they may do*. The trust boundary (§4) lives entirely in Layer 2.

### 3. Peer identity — pinned X.509 client certificates

- Identity material is an **X.509 certificate** per peer install. A peer
  presents it as a TLS **client** certificate when dialing out and as the TLS
  **server** certificate when accepting; the same certificate serves both
  roles (it is the install's federation identity).
- Identity is established by **direct leaf-certificate pinning**, not by a CA
  trust chain. Each registry entry pins the peer's certificate by its
  **SHA-256 fingerprint** (of the DER-encoded leaf). A presented certificate is
  accepted iff its fingerprint matches a pinned value for that registry entry.
  - Rationale: pinning needs **no certificate authority to run, no CRL/OCSP
    infrastructure, and no chain-validation policy**. For a small, explicitly
    enumerated peer mesh this is simpler and *stricter* than a private CA — a
    pinned cert cannot be impersonated by anything else the CA also signed.
  - Certificates are **self-signed**; expiry is still honored (`NotAfter`
    rejected), so a hung-forever cert is not a thing. Hostname (SAN)
    verification against the dial target is also enforced for the server cert.
- **Rotation.** A registry entry holds a **list** of pinned fingerprints, not
  one. To rotate, an operator adds the new fingerprint to both ends, deploys
  the new cert, then removes the old fingerprint. This gives a zero-downtime
  overlap window without a CA.
- A future deployment that prefers a private federation CA MAY be added as an
  *alternative* trust mode (`ClientCAs` + chain verification) without changing
  the authorization model in §4 — the cert→peer mapping is the only seam. Not
  in v1 scope.

### 4. Authorization & the trust boundary

ADR-0001 §3 makes `authority` the single routing seam. This ADR makes it the
single **authorization** seam too.

**Definitions.**
- `localAuthorities(S)` — the authorities install `S` homes (ADR-0001 §3: one
  per install by default; a set in principle).
- `peerAuthorities(S, P)` — the authorities install `S`'s peer registry
  permits peer `P` to act for. This is the set `P` is *authoritative for* (its
  home authorities), as declared on `S`. A peer with an empty set, or no
  registry entry, is unauthorized for all federated calls.
- *Governing authority* of a call — exactly as ADR-0001 §4 defines it for
  routing: the `To` authority for `Send`; the owning authority of the target
  envelope for `Get` / `Thread` / `Consume` / `Cancel`.

**Routing invariant.** A correct routing decorator on peer `A` sends a call to
peer `B` *only because the governing authority is foreign to `A`* — i.e. homed
by `B`. Therefore every federated call `B` receives MUST have a governing
authority in `localAuthorities(B)`. `B` re-checks this on arrival; a federated
call whose governing authority `B` does not home is rejected as unroutable
(`ErrNotFound`-class). This defends against a misconfigured or malicious peer
pushing `B` mail `B` is not the home for — i.e. attempting to use `B` as a
relay.

**Trust-boundary check (the core rule).** An envelope's `From.Authority` is a
claim made by the calling peer. The rule:

> For a `Send`, the envelope's **`From.Authority` MUST be in
> `peerAuthorities(S, P)`** — the calling peer may only originate mail for
> authorities it is registered as authoritative for. For `Cancel` / `Consume`,
> the originating / recipient authority of the target envelope MUST likewise be
> in `peerAuthorities(S, P)`. For `Get` / `Thread`, the peer may read an
> envelope only if **at least one party** (`From` or `To` authority) is in
> `peerAuthorities(S, P)` — a peer may inspect threads it is a party to, and
> no others.

This makes impersonation structurally impossible: peer `A`, registered as
authoritative for authority `a`, cannot send an envelope claiming
`From: msg://…/b/…` because `b ∉ peerAuthorities(S, A)`. The server rejects it
before the `Store` is touched.

**`Inbox` / `Subscribe` are never federated — by construction.** An install
drains an inbox only for *its own* recipient address, whose authority is by
definition local to that install; the routing decorator therefore resolves
`Inbox` / `Subscribe` to the local `Store` and never dispatches them over the
hop. The federation HTTP surface deliberately **does not expose**
`Inbox` / `Subscribe`. A peer can never drain another install's mailboxes.
Cross-host inbox *pull* is out of scope for v1; the reply path does not need it
(see below).

**Federation v1 = push-delivery + reply correlation.** The hop carries:
`Send` (deliver mail to a peer), and `Get` / `Thread` / `Consume` / `Cancel`
(reply correlation and lifecycle for the sender). A reply travels as an
ordinary `Send` in the opposite direction — `B`'s agent replies with an
envelope `From: b → To: a`, which `B`'s own decorator routes to `A` as a
`Send`. No cross-host `Inbox` is required for the round trip.

**Authorization is enforced server-side, fail-closed.** Every federated request
runs the routing-invariant check and the trust-boundary check *before* the
`Store` call. Any failure → reject, no `Store` mutation, audit-logged (§5). The
client-side decorator performs no authorization — it is not the trust anchor;
the receiving install is.

### 5. The federation surface, wire mapping, and errors

**Dedicated, separately-listened surface.** The federation routes are served
**only** on a dedicated TLS listener with mTLS enforced — they are NOT added to
the existing plaintext `/api/v1/*` listener that serves the GUI. This keeps the
un-authenticated local surface and the authenticated cross-host surface
physically separated (no middleware-ordering mistake can expose one as the
other), and lets an operator bind them to different interfaces/ports. The
federation listener is started **only when federation is configured** (§7).

- Route group: `POST /federation/v1/messages` (Send),
  `GET /federation/v1/messages/{id}` (Get),
  `GET /federation/v1/messages/thread/{thread_id}` (Thread),
  `POST /federation/v1/messages/{id}/consume` (Consume),
  `POST /federation/v1/messages/{id}/cancel` (Cancel).
  Request/response bodies are the same JSON shapes the existing
  `/api/v1/messages/*` handlers use, so the `httpStore` template is reused
  almost verbatim — only the base path and the `http.Client`'s `tls.Config`
  differ.
- `Inbox` and `Subscribe` routes are intentionally absent (§4).
- Payload size: the existing `broker.MaxPayloadBytes` (256 KiB) cap applies
  unchanged; the federation handler enforces it on `Send` like the local path.

**Middleware order on the federation listener:** (1) TLS handshake +
`RequireAndVerifyClientCert`; (2) pin-match the presented client cert to a peer
registry entry → resolves peer `P` (no match → `403`, connection-level reject);
(3) per-request authorization (routing invariant + trust boundary, §4); (4)
handler → `Store` call.

**Error mapping.** Federation failures map onto `go-messaging`'s existing
sentinel errors so callers see clean, already-handled failures (ADR-0001's "no
silent drop" principle):

| Condition | HTTP | `httpStore` maps to |
|---|---|---|
| Client cert not pinned / handshake fails | (TLS reject) / `403` | `ErrStoreUnavailable` |
| Authorization failure (trust boundary) | `403` | `ErrStoreUnavailable` *(not leaked as NotFound)* |
| Governing authority not homed by server | `404` | `ErrNotFound` |
| Target envelope absent | `404` | `ErrNotFound` |
| Payload over `MaxPayloadBytes` | `413` | a size error, as the local path |
| Peer unreachable / TLS dial fails | — | `ErrStoreUnavailable` |

A foreign authority with **no registered route** remains a client-side hard
error (ADR-0001 §4) — it never reaches the wire.

**Audit logging.** Every federated request logs, at minimum: peer identity
(cert fingerprint + registry label), operation, governing authority, envelope
`From`/`To`, decision (allow/deny + reason). This is the forensic record for
the trust boundary and is required, not optional.

### 6. Forward-compat — reserved namespace for signed envelopes

Signed envelopes (§1) are the right tool **if** the point-to-point assumption
is ever relaxed (open peering, or relays that must not be trusted to vouch for
origin). To keep that future additive and non-breaking:

- The `Envelope.Metadata` map keys with the prefix **`fed.`** are **reserved**
  by this ADR for federation use and MUST NOT be used by application code.
- Specifically reserved for a future signed-envelope ADR: `fed.sig` (detached
  signature, base64), `fed.sig.alg` (algorithm), `fed.sig.kid` (signing key
  id), `fed.sig.ts` (signing timestamp), `fed.nonce` (replay nonce).
- v1 sets none of these. A v1 peer ignores them if present. Introducing
  signed envelopes later is then a *new ADR* that starts populating reserved
  keys — no schema change, no `Kind` change, no break to the locked contracts.

This ADR does **not** specify a signing scheme; it only fences the namespace so
the option stays open.

### 7. Configuration & the standalone guarantee

The auth model is configured through a **federation config block** (a
structured file section — peer lists and certificate paths do not fit the
env-var convention of `internal/config`). The implementation task fixes the
exact file/format; this ADR fixes the *content*:

- **Identity:** paths to this install's federation certificate + private key.
- **Listener:** the bind address/port for the dedicated federation TLS
  listener.
- **Peer registry** (inbound authorization): a list of peers, each with a
  human label, one-or-more pinned cert SHA-256 fingerprints, and the
  `authorities` set that peer is authoritative for.
- **Foreign-route registry** (outbound — extends ADR-0001 §4): each foreign
  `authority` → { peer endpoint URL, the pinned server-cert fingerprint(s) for
  that endpoint }. This is the change ADR-0001 §4 anticipated: "Until M2 lands,
  the foreign-route registry carries no credentials." It now carries the
  server-pin; the client TLS identity is the shared install identity above.

**Standalone guarantee (preserved, ADR-0001).** When no federation block is
configured: no federation listener is started, the peer registry and
foreign-route registry are empty, the routing decorator resolves every
authority as local, and Torque behaves exactly as today with **zero extra
configuration and zero new attack surface**. Federation — and therefore all of
this ADR — is purely additive and inert until an operator opts in on both ends.

### 8. Implementation checklist (feeds the ph-4 federation-auth task)

1. **Federation config** — parse the block in §7; validate cert/key load,
   fingerprint format, and that every foreign route names a reachable-shaped
   endpoint. Absent block → federation fully disabled.
2. **Peer registry** — in-memory structure: `fingerprint → Peer{label,
   authorities}`; constant-time-ish lookup by presented-cert fingerprint.
3. **Federation TLS listener** — a separate `http.Server` with
   `tls.Config{ClientAuth: RequireAndVerifyClientCert, VerifyPeerCertificate:
   <pin-match>}`; mount the §5 route group only.
4. **Pin-match middleware** — resolve the connection's verified client cert to
   a `Peer`; reject unpinned with `403`.
5. **Authorization middleware** — routing-invariant check + trust-boundary
   check (§4) per request; fail-closed; audit-log every decision.
6. **Federation handlers** — thin wrappers over the existing messaging `Store`
   (reuse the `/api/v1/messages/*` JSON shapes); enforce `MaxPayloadBytes` on
   `Send`.
7. **Federation `httpStore`** — generalize `go-agentmux-client`'s `httpStore`:
   base path `/federation/v1/messages`, `http.Client` with a client-cert
   `tls.Config` + server-cert pinning via `VerifyPeerCertificate`; expose only
   `Send`/`Get`/`Thread`/`Consume`/`Cancel`; return `ErrStoreUnavailable` for
   any disabled/`Inbox`/`Subscribe` call.
8. **Wire into the routing decorator** — the foreign-authority branch (ADR-0001
   §4) constructs the federation `httpStore` from the foreign-route registry
   entry.
9. **Error mapping** — implement the §5 table; verify against
   `messagingtest.RunContract` expectations for the operations exposed.
10. **Tests** — pinned-peer happy path; unpinned cert rejected; expired cert
    rejected; `From`-authority forgery rejected (trust boundary); relay attempt
    (governing authority not homed) rejected; cert-rotation overlap; standalone
    install starts no listener.

---

## Consequences

**Positive**

- One mechanism — mTLS — delivers peer identity, mutual authentication, and
  channel encryption for *every* federated `Store` operation, with no bespoke
  cryptography and no JSON-canonicalization surface.
- The trust boundary is a single, auditable rule (`From.Authority ∈
  peerAuthorities`) enforced fail-closed on the receiving install.
  Impersonation and relay abuse are structurally impossible, not merely
  detected.
- Cert pinning means **no CA, no CRL/OCSP, no chain-policy** to operate — the
  peer mesh is small and explicit, and pinning is stricter than a CA anyway.
- The federation surface is physically separated from the existing
  un-authenticated local surface — no middleware-ordering bug can cross them.
- Standalone Torque is completely unaffected: no config, no listener, no
  surface. Federation is additive and inert until opted in.
- The implementation reuses the proven `httpStore` template; the new surface is
  small (one listener, two middlewares, five thin handlers).

**Negative / accepted tradeoffs**

- mTLS is **hop-by-hop**, not end-to-end. Under the locked point-to-point
  topology this is fine; the moment a relay or untrusted intermediary is
  introduced, origin authenticity would need signed envelopes (§6). Accepted:
  v1 has no relays, and §6 keeps the upgrade path additive.
- Certificate pinning trades CA-style automatic trust for **manual rotation
  coordination** (add-new-pin / deploy / drop-old-pin on both ends). Accepted:
  the peer set is small and explicitly managed; the multi-pin overlap window
  makes rotation zero-downtime.
- `Inbox` / `Subscribe` are not federated, so **cross-host inbox pull** is
  unsupported in v1. Accepted: the push-delivery + reply model covers the
  round trip without it; revisiting it is a future ADR.
- Operators must provision and distribute a key pair and exchange fingerprints
  per peer relationship. Accepted: this is the irreducible cost of a real trust
  boundary; there is no zero-config secure federation.

**Follow-ups / delegated decisions**

- **Phase ph-4** — implement this ADR per §8; this unblocks the Federation
  phase that ADR-0001 §4 gated on M2.
- **Future ADR (only if topology relaxes)** — specify the signed-envelope
  scheme over the `fed.*` reserved namespace (§6): algorithm, canonical
  `Envelope` serialization, key distribution, replay-nonce dedup window.
- **Optional** — a private-federation-CA trust mode as an alternative to
  pinning (§3); does not affect the §4 authorization model.

---

## References

- ADR-0001 — Torque Messaging design lock (steering + federation contracts);
  §3 addressing, §4 authority-routing decorator (amended here re: registry
  credentials).
- Design study: `agent-os/docs/torque-messaging-design.md`
- Plan: CW-20260518-0038 — phase ph-4 (Federation).
- Library: `github.com/hollis-labs/go-messaging` v0.2.0 — `store.go` (`Store`
  contract), `messaging.go` (`Envelope`, `Address`, `Metadata`),
  `messagingtest/` (contract suite).
- Federation transport template: `go-agentmux-client` `httpStore`
  (`messaging_store.go`) — note `WithHTTPClient` makes the `tls.Config`
  injectable.
- Prior-art auth gate (token-on-localhost pattern this ADR moves beyond):
  `internal/httpserver/admin.go` (`adminGate`, `TORQUE_ADMIN_TOKEN`).
- Torque: `internal/messaging/sqlstore.go`, `internal/broker/broker.go`
  (`MaxPayloadBytes`), `internal/httpserver/server.go` (route registration),
  `internal/config/config.go`.

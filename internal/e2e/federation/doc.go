// Package federation_e2e is the end-to-end test surface for Torque's
// messaging federation (plan CW-20260518-0038, ph-6 — CW-20260518-0052).
//
// Where the per-package tests prove one seam in isolation —
// internal/messaging/router_test.go the routing decorator,
// internal/federation/server_test.go a single mTLS hop,
// internal/runtime/steering/bridge_test.go the turn-injection bridge — this
// package wires the WHOLE stack together and drives it as production would:
//
//	local SQLite Store
//	  → broker (typed envelope helpers)
//	    → authority-routing Router (internal vs. external = one routing question)
//	      → federation mTLS Server on each peer (the cross-host hop)
//	        → peer's local SQLite Store
//
// The three-app topology. The Torque ⇄ Nanite ⇄ Tether federation the task
// names is modelled as three federated installs, one per app. This is faithful
// rather than a shortcut: federation is delivered by `Authority`-based routing
// over the portfolio-shared go-messaging contract (ADR-0001 §4), and "internal
// vs. external is only addressing / routing" (torque-messaging-design.md). Each
// app homes its own authority and implements its own Store against the same
// contract; a three-node mesh built from Torque's Store/Server/Router therefore
// exercises the exact wire protocol and trust boundary every app's federation
// hop crosses. The authorities are literally named "torque", "nanite" and
// "tether" so the routing assertions read as the cross-app story they model.
//
// What the suite covers:
//
//   - standalone install — no foreign routes, every authority resolves local
//     (the "works without Tether" guarantee);
//   - internal routing — a local-authority envelope never leaves the install;
//   - external routing — a foreign-authority envelope crosses the mTLS hop and
//     lands in the peer's Store, and ONLY there;
//   - agent-to-agent — a request/response round trip across the hop in both
//     directions, with a cross-Store thread view;
//   - user-to-agent — the steering bridge injecting a user notice into a live
//     agent session, and leaving it durable when no session is live;
//   - the full mesh — every ordered app pair routes;
//   - the cross-app trust boundary — an install cannot originate mail for an
//     authority a peer has not registered it for (impersonation → 403);
//   - the GUI surface — the /api/v1/messages HTTP API the messaging GUI client
//     (apps/gui/src/lib/messaging.ts) consumes, wired over a federation Router
//     so a GUI Send/Get crosses the hop transparently.
//
// Not covered here: the React messaging UI itself (inbox/thread/compose) has
// its own vitest suite — apps/gui/src/lib/messaging.test.ts and
// apps/gui/src/lib/api.test.ts. This package verifies the Go-side HTTP API
// contract those clients depend on, end to end through federation.
package federation_e2e

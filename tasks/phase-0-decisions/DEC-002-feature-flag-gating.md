# DEC-002 — Feature-flag gating vs always-on for Project/Epic/Sprint tools

**Phase:** 0 — Decisions
**Status:** in-progress
**Depends on:** none
**Blocks:** ENT-PROJECT, ENT-EPIC, ENT-SPRINT
**Source:** ADR-0004 §6 (Open questions)

## Summary

Project/Epic/Sprint MCP tools are currently registered only when their
feature flag is enabled (`registerOptInTools`). ADR-0004's stated goal is
that "an agent using Torque purely as a task/project tracker (no execution)
should find the surface just as natural as an agent that dispatches work
through it" — which implies these tools should arguably be available in
pure-tracking mode regardless of execution-related feature flags. The ADR
flags this explicitly as undecided and notes it "may have implications
beyond MCP (GUI, HTTP)."

## Decision to make

1. **Keep current gating** — Project/Epic/Sprint tools stay behind
   `registerOptInTools`/their feature flag, unchanged. Phase 4 entity work
   lands inside the existing gate.
2. **Always-on for pure tracking** — register these tools unconditionally,
   decoupling "task/project data management" from "execution enabled."
   Requires checking whether the same flag also gates non-MCP surfaces (GUI
   routes, HTTP endpoints) that would need matching treatment, or whether
   the decoupling is MCP-only for now.

## Acceptance criteria

- [ ] Decision recorded with rationale.
- [ ] If always-on is chosen: confirm scope — MCP-only, or does GUI/HTTP
      also need the same decoupling in this pass? (If GUI/HTTP is a bigger
      lift, it's fine to scope this decision to MCP only and flag GUI/HTTP
      as a separate future task — just say so explicitly, don't leave it
      ambiguous.)
- [ ] ENT-PROJECT, ENT-EPIC, ENT-SPRINT unblocked with a clear registration
      instruction.

## Out of scope

Any GUI/HTTP registration changes beyond what the decision explicitly opts
into — this is an MCP-scoped ADR (ADR-0004 §title).

## Outcome

**PROPOSED — pending user sign-off**

### Decision: Option 1 — keep current gating

Project/Epic/Sprint MCP tools stay registered only when their
`features.{projects,epics,sprints}` flag is enabled, via
`registerOptInTools()` in `internal/mcpadapter/adapter.go:228`, unchanged
from today's mechanism.

### What the flag actually gates today (investigated, not assumed)

`internal/service/feature.go` defines exactly four known flags:
`sprints`, `projects`, `epics`, `collections` — nothing else. There is no
separate "execution enabled" flag anywhere in the codebase. So the premise
in ADR-0004 §6 ("execution-related feature flags") doesn't hold up:
these flags don't gate execution at all, they gate whether an install
opts into the Project→Epic→Sprint hierarchy (and, out of this ADR's
scope, Collections) as optional data-model complexity. All four default
to **off** (`FeatureService.IsEnabled` returns false when the settings
key is unset) — a fresh install starts with zero Project/Epic/Sprint.

The gate is **not MCP-only** — it's the same flag enforced consistently
across all three surfaces, at different layers:

- **Service layer (source of truth):** `ProjectService`/`EpicService`/
  `SprintService` (and `CollectionService`) each call
  `s.feature.Require("<flag>")` at the top of every method
  (`internal/service/project.go`, `epic.go`, `sprint.go`,
  `collection.go`, plus `issue.go:128` and `task.go:223-250` for
  sprint/project/epic association on Task/Issue). This is authoritative
  and applies to every caller — HTTP, GUI-driven HTTP calls, and MCP
  handlers alike.
- **HTTP:** routes are registered unconditionally
  (`internal/httpserver/projects.go` etc.), but when the flag is off the
  service call returns `*service.FeatureDisabledError`, which the
  handlers map to `404` (see `listProjects`/`getProject` in
  `internal/httpserver/projects.go:67-90`).
- **GUI:** `apps/gui/src/pages/BoardPage.tsx:495-510` fetches
  `/api/v1/features` and conditionally renders the Project/Sprint/Epic
  board sections only when `flags.projects`/`flags.sprints`/
  `flags.epics` are true.
- **MCP:** the only surface with an additional *registration-time* gate —
  when the flag is off, the tools are never added to the tool list at
  all (`registerOptInTools`), on top of the same service-layer
  enforcement the other two surfaces rely on.

So all three surfaces already agree in spirit: none of them let you use
Project/Epic/Sprint when the flag is off. MCP is just stricter about
*hiding* the capability rather than exposing-then-rejecting it.

### Rationale

1. Since these are opt-in *data-model-complexity* flags, not execution
   gates, decoupling MCP registration from them doesn't serve the ADR's
   stated goal any better — an agent gets full ergonomics parity for
   Project/Epic/Sprint (this ADR's pagination/sort/bulk/archive work)
   the moment an operator opts in. The "just as natural" goal is about
   surface *quality* once enabled, not universal tool presence
   regardless of whether the operator wants the entities at all.
2. Making MCP always-on while HTTP/GUI keep gating (service-layer
   enforcement doesn't change either way) would mean every default
   install's agent sees ~15-20+ tool schemas across three entities
   (more once this ADR's create/list/bulk expansion lands) that always
   fail with a domain error unless the operator has opted in — real
   tool-selection noise for the common flat-task-list case, which cuts
   against agent ergonomics rather than helping it.
3. The error path for "tool present but flag off" is legitimate and
   already tested (`FeatureDisabledError` maps cleanly to the `domain`
   error taxonomy, `internal/mcpadapter/errors.go:206`,
   `errors_test.go:86`) — so nothing here is technically broken either
   way. This was a quality-of-default-surface judgment call, not a
   technical constraint.

### Scope confirmation (per acceptance criteria)

Always-on was **not** chosen, so the MCP-only-vs-GUI/HTTP fork doesn't
apply. For completeness: had always-on been chosen, it would need to be
scoped MCP-only — HTTP already always-registers routes with dynamic
per-call gating (the pattern MCP would be copying), and GUI's conditional
board rendering is a distinct, deliberate human-UX choice (hide unused
nav for a human) orthogonal to agent tool-discovery ergonomics; no GUI/
HTTP code changes would be implied by an MCP-only always-on change.

### Registration instruction for ENT-PROJECT/ENT-EPIC/ENT-SPRINT

Implement the ADR-0004 §5 target tool inventories (expanded
`create`/`get`/`list`/`update`/`delete`/`archive`/`unarchive`/
`bulk_update`, etc.) inside the existing `registerProjectTools()`,
`registerEpicTools()`, `registerSprintTools()` functions in
`internal/mcpadapter/{project,epic,sprint}_tools.go`, called
conditionally from `registerOptInTools()` in
`internal/mcpadapter/adapter.go:228-241` exactly as today. No new gating
mechanism needs to be designed or built — this ADR's ergonomics fixes are
orthogonal to and fully compatible with the existing flag structure.

### Flags for the project owner (found during investigation, not decided here)

- **MCP tool registration is startup-only, not dynamic.**
  `mcpadapter.New()` calls `registerOptInTools()` exactly once at
  construction (`internal/mcpadapter/adapter.go:70-79`), invoked from the
  `torque mcp` stdio subcommand (`cmd/torque/mcp.go:115`) and from
  per-task loopback boot (`internal/runtime/bootstrap/loopback.go:127`).
  `torque_settings_save` documents that it can flip
  `features.projects`/`epics`/`sprints` (`internal/mcpadapter/
  settings_tools.go:20`, and its own docstring already warns "Feature
  flags may require a restart to register new tools"), and the
  service-layer check picks up the new value immediately — but the
  *current* MCP session's tool list won't include the newly-enabled
  tools until the next `torque mcp` process starts. HTTP and GUI don't
  have this lag (service layer is checked live; GUI re-fetches
  `/api/v1/features`). This is a real self-service friction gap for an
  agent trying to opt itself into pure-tracking mode mid-session, but
  it's orthogonal to whether the gate should exist — flagging as a
  candidate for its own follow-up task (e.g. dynamic tool
  re-registration) rather than folding it into this decision.
- **If the actual intent is "pure-tracking installs get Project/Epic/
  Sprint out of the box, no setup step,"** the correct lever is flipping
  the *default value* of `features.projects`/`epics`/`sprints` to true
  (opt-out instead of opt-in) — not decoupling MCP registration from
  enforcement. That's a deliberate product decision that would
  intentionally also change GUI (board sections visible by default) and
  HTTP (200s instead of 404s on fresh installs) — a bigger, cross-surface
  call explicitly out of scope for this MCP-scoped decision. Worth its
  own task/ADR if that's the desired direction; this decision does not
  take a position on it.

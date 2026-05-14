---
task: CW-20260417-0079
status: research memo — no implementation yet
owner: torque runtime
date: 2026-04-17
source: Fragments Engine (`~/Projects-apps/fragments-engine/engine/docs/`)
---

# Fragments Engine Boundary Rules — Adopt/Skip Memo

## Context

Fragments Engine (FE) is the parent of the Volon/Vanta/Cerberus/Hadron
portfolio Torque will eventually plug into. FE codifies four families
of **boundary rules** covering permission, isolation, and trust:

1. Task-lifecycle execution boundaries (`on_*` hook policy)
2. Capability-based role permissions (role-gated HTTP verbs)
3. Storage tier boundaries (DB authoritative, filesystem is a read-only
   cache, blocked writes via hook)
4. Persistence adapter boundary (dialect containment)

This memo summarizes each, compares against Torque's current state,
and recommends adopt/skip per rule. Authoritative FE sources are cited
inline; Torque call-sites are cited with file:line so follow-ups can
pick up directly.

## 1. Task-Lifecycle Execution Boundaries

**FE rule** (`fragments-engine/engine/artifacts/plan/control-plane-boundaries.md`):

- `on_complete`: `review` (default, pause for human) | `continue`
  (auto-close) | `pause` (halt scheduler).
- `on_block`: `pause` (default) | `escalate` (retry chain) | `retry`
  (re-queue up to `retry_budget`).
- `on_escalation`: `notify` (default) | `handoff` (reassign to senior
  profile) | `halt` (stop scheduler entirely).
- **Override hierarchy:** task-type override → agent-profile override →
  project default.

**Torque today** (`internal/service/task.go:36`,
`internal/runtime/scheduler/lifecycle.go:97`):

- `OnDone` (review/close/notify), `OnFail` (retry/block/escalate/
  notify), `OnReview` (pause), `OnDoneMerge` (none/auto/pr/auto-resolve),
  escalation chain with profile swap (`senior-agent`, `council`,
  `human`) already wired.
- **Missing:** project-level and agent-profile-level defaults. Hooks
  are task-only. `pause` as an `on_done` terminal is not modeled.

**Recommendation: ADOPT — cherry-pick two specifics.**

- Add agent-profile and project-level defaults to the `on_*` resolver
  (task override → profile → project). This is a ~half-day change in
  `lifecycle.go` and the profile loader; it turns repeated task-level
  config into reusable policy.
- Add `pause` as an `on_done` option (halt scheduler, not just the
  task). Useful for "stop the world when X succeeds" gates.

Skip renaming our terms to match FE's (`on_complete`/`on_block`) —
`on_done`/`on_fail` are already through the schema, MCP tools, and
GUI.

## 2. Capability-Based Role Permissions

**FE rule** (`fragments-engine/engine/docs/capability-model.md`):

- `X-Volon-Role` header and `VOLON_ROLE` env var select a role per
  request/process.
- Role → verb map: `orchestrator` full, `architect` no DELETE, `worker`
  / `reviewer` / unknown GET-only. Middleware enforces 403 on violation.
- `VOLON_AGENT_DEPTH` env blocks sub-agents (depth ≥1) from writing
  bootstrap/cache tiers.
- Future: short-lived bearer tokens with role claims.

**Torque today**:

- `Task.Permissions map[string]any` (`internal/service/task.go:48`) is
  passed to executors as opaque hints (e.g., network=deny, fs=readonly)
  — **no API-level enforcement**.
- `Task.Trust` (trusted/normal/untrusted) auto-resolved from
  `source_type`; used only in validation, not in gating.
- No role concept. No depth tracking. No middleware.

**Recommendation: SKIP for now. Revisit when Torque becomes
multi-tenant or embeds into a portfolio API surface.**

Torque's current threat model is single-operator local-first:
the API binds to localhost, the scheduler runs as the user, and
`executor=cli` already delegates sandboxing to the harness (Claude
Code, Codex, etc.). A role model is premium complexity we don't need
yet. What *is* worth keeping in mind:

- When Torque grows a shared remote mode (e.g., hosted GUI, shared
  scheduler), re-open this and adopt the header + middleware pattern
  from FE verbatim — it's small (one middleware, one env var) and
  battle-tested.
- The existing `Task.Trust` field is the right seed. Keep it; don't
  remove it.

## 3. Storage Tier Boundaries

**FE rule** (`fragments-engine/engine/docs/17_namespace-tiers.md`):

- Authoritative tier = DB (writes only via API).
- Cache tier (`.agentrc/tasks/`, `backlog/`) = read-only export;
  filesystem hook `exit 2`s on direct writes.
- State/logs tier = system-owned; blocked to prevent races.
- PCC/global and bootstrap tiers = high-value context, writable only
  via controlled refresh flows.
- Promotion paths: draft → docs → task (API create); backlog → task;
  session → task.

**Torque today**: none of this is present. Task records, sprints,
and backlog items live in SQLite (authoritative) and are read via MCP
and the GUI. There is no filesystem cache or hook-blocked directory.

**Recommendation: SKIP — not applicable to Torque's model.**

FE's tier story exists because it layers agentrc/PCC filesystem
projections on top of its DB. Torque is DB-plus-GUI; it has no
parallel filesystem shadow. The DB is already the single source of
truth by construction (principle #5 of
`docs/architecture/design-philosophy.md:33`).

**One rule worth copying in spirit, not code:** document explicitly
that the only write path to Torque state is the API (MCP + HTTP).
No direct SQLite edits. Add this as a line in `design-philosophy.md`
or `task-model-v0.1.md` so it's unambiguous for contributors.

## 4. Persistence Adapter Boundary

**FE rule** (`fragments-engine/engine/docs/persistence-adapter-boundary.md`):

- Runtime code must not embed engine-specific (SQL dialect, driver)
  logic. All of it lives behind a persistence adapter (`sqlstore.Store`).
- Active surfaces (code, docs, boot prompts, PCC) must reflect
  current architecture — no stale dialect references.
- Historical artifacts (old task records, logs) are preserved as-is.

**Torque today**
(`internal/persistence/sqlstore/store.go:13`,
`internal/persistence/sqlstore/dialect.go:5`):

- `Dialect` interface with `Placeholder()`, `AutoIncrement()`,
  `TimestampDefault()`, `JSONType()`, `Upsert()`.
- Two implementations: sqlite3 and postgres. Runtime code uses
  placeholders; no dialect-specific SQL leaks out of `sqlstore`.

**Recommendation: ALREADY ADOPTED. Keep it clean.**

The only delta worth noting: add a one-line invariant in
`internal/persistence/sqlstore/README.md` (if one exists, else in
`docs/architecture/task-model-v0.1.md`) that says **"no dialect
SQL outside of `internal/persistence/sqlstore/`"** so future
contributors don't accidentally regress the boundary.

## Summary Table

| Rule | FE | Torque today | Verdict |
|---|---|---|---|
| Task-lifecycle hooks | full 3-axis policy w/ profile+project defaults | task-only, no inheritance | **Adopt inheritance + `pause` on_done** |
| Role-gated API | header+env+middleware+depth | opaque permissions map, Trust field | **Skip; revisit when multi-tenant** |
| Storage tiers w/ hook block | DB auth + cache + PCC + bootstrap | DB-only, no FS cache | **Skip; add API-only invariant note** |
| Persistence adapter | `sqlstore.Store` abstraction | same, two dialects | **Already adopted** |

## Follow-ups (if accepted)

1. **CW task — runtime:** add agent-profile and project-level
   resolution for `on_done` / `on_fail` / `on_review`, plus `pause`
   as an `on_done` terminal. Surface area:
   `internal/runtime/scheduler/lifecycle.go`, profile loader, schema
   migration for `project.defaults`.
2. **CW task — docs:** one-line invariants in
   `docs/architecture/design-philosophy.md` (API-only writes) and
   `docs/architecture/task-model-v0.1.md` (no dialect SQL outside
   sqlstore).
3. **Parking lot:** FE role model (pattern 2) — revisit when a shared
   remote surface exists.

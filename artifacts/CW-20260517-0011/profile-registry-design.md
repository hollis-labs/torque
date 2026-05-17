# Profile Registry Design — EDGE 1 of CW-20260517-0011

**Status:** needs-discussion
**Author:** config-layer agent (EDGES 1 & 3)
**Date:** 2026-05-17
**Scope of this doc:** delineate the three "profile" registries, state which
API consumes which, and recommend a convention. The config-layer code
changes that accompany this doc (error-returning resolver, alias support)
are described in `report-profiles.md`.

---

## 1. The problem

A dispatch needs an *agent identity* — "what executor, what provider, what
model, what runtime, what budget". Today **three unrelated registries** all
present themselves as "the profile", and nothing at the call site tells you
which one a given API wants. Picking the wrong one fails late and
unhelpfully (pre-EDGE-1: a misleading `adapter not registered for provider:
profile has empty provider`).

This is a naming collision, not a data-model bug. Each registry is
internally coherent; the harm is that they share the word "profile" and
share a problem-shaped surface ("an agent identity you name").

---

## 2. The three registries

### Registry A — `agent_profiles` (Torque's `profiles.yaml`)

| | |
|---|---|
| **Lives in** | `profiles.yaml` at the Torque working dir, or `$TORQUE_PROFILES_PATH` |
| **Key shape** | flat slug — `default`, `codex-long`, `opencode-default` (post-EDGE-3); was `torque-backend`, `nanite-frontend` |
| **Loaded by** | `config.LoadProfiles` / `config.LoadProfilesFile` (`internal/config/profiles.go`) |
| **Go type** | `config.ProfileMap` = `map[string]config.AgentProfile` |
| **Contents** | executor (`cli`/`api`), provider, model, command, args, runtime_kind, timeouts, env strip prefixes, optional `launch_profile` ref |
| **Consumed by** | `torque_session_launch`'s `agent_profile` arg; the scheduler/executor dispatch path (`config.GetProfileOrDefault` in `executor.go`, `boot.go`, `planner.go`, `orchestrator.go`, `plugins/executor-api`) |
| **Authority** | Torque owns it. Hand-maintained YAML. Substrate also ships `builtinProfiles` (reviewer-end-agent, planner, orchestrator) as a fresh-install fallback. |

This is the **only** registry `torque_session_launch` accepts. It is the
canonical "Torque agent identity".

### Registry B — Tether catalog **boot-profiles**

| | |
|---|---|
| **Lives in** | the Tether catalog (external — Tether daemon / catalog repo) |
| **Key shape** | **dotted** id — `nanite.backend.main` |
| **Loaded by** | Tether tooling, not Torque's `internal/config` |
| **Consumed by** | Tether's own boot path; surfaced in Torque only indirectly via `launch_profile` references inside an `agent_profiles` entry |
| **Authority** | Tether owns it. |

A boot-profile id is **NOT accepted** by `torque_session_launch`'s
`agent_profile` arg. Passing `nanite.backend.main` there is the single most
common EDGE-1 mistake. The dotted vs. flat key shape is the only ambient
signal that distinguishes B from A — and nothing enforces or documents it.

### Registry C — Tether catalog **agents** + **launches**

| | |
|---|---|
| **Lives in** | the Tether catalog |
| **Key shape** | catalog-defined names (agent names, launch ids) |
| **Loaded by** | Tether tooling |
| **Consumed by** | Tether's catalog operations |
| **Authority** | Tether owns it. |

Also not accepted by `torque_session_launch`. Conceptually adjacent to B —
"things the Tether catalog calls agent-shaped" — but a separate list with a
separate purpose, hence counted separately.

### One legitimate bridge

`config.AgentProfile.LaunchProfile` (`launch_profile:` in `profiles.yaml`)
is the **only** sanctioned crossing point: an entry in Registry A may
*reference* a go-agent-launch launch profile by path or `path#id`.
Resolution is standalone (no Tether daemon required — see
`docs/launch-profiles.md`). This is fine because it is an explicit field
with its own name, not an overload of "profile".

---

## 3. Why this is confusing — root causes

1. **Shared noun.** All three are spoken of as "the profile". The word
   carries no registry information.
2. **No call-site disambiguation.** `agent_profile` (the
   `torque_session_launch` arg) does not say "this is a Registry A key".
3. **Silent wrong-registry failure (pre-EDGE-1).** A Registry B id passed
   where Registry A was wanted resolved to a zero-value `AgentProfile{}` and
   blew up three layers down as an empty-provider adapter error.
4. **Key shape is an unreliable tell.** A is flat, B is dotted — but that is
   convention, not contract, and is documented nowhere.

---

## 4. Recommendation

**Do not unify the three registries.** They have different owners (Torque
vs. Tether), different lifecycles, and different storage. A forced merge
would couple Torque's config loader to the Tether catalog and is far out of
proportion to a naming problem. Instead, **make the delineation explicit and
enforced**. Three concrete moves, in priority order:

### R1 — Rename the arg/concept away from the bare word "profile" (discuss)

`torque_session_launch`'s `agent_profile` arg should be understood — and
ideally documented in its tool schema description — as **"an
`agent_profiles` key from `profiles.yaml`"**. The bare word "profile" in
docs, tool descriptions, and error text should always be qualified:
`agent_profiles` entry / boot-profile / catalog agent. Never unqualified.

> **Decision needed:** rename the MCP arg `agent_profile` →
> `torque_agent_profile` or document-only? Renaming is the honest fix but
> touches `internal/mcpadapter` (out of this agent's scope). Recommendation:
> document-only now, schedule the rename behind a deprecation alias.

### R2 — Fail fast, name the registry, list valid values (DONE, config layer)

Resolution misses must error immediately with: (a) which registry was
consulted, (b) the valid values in it, (c) an explicit "this does NOT accept
Tether catalog boot-profile ids / catalog agents" note. Implemented this
ticket as `config.ResolveProfile` + `config.UnknownProfileError` (see
`report-profiles.md`). The operator-facing dispatch path
(`internal/mcpadapter`, `internal/service`, executor precheck) should be
migrated from `GetProfileOrDefault` to `ResolveProfile` — that wiring is
out of this agent's file scope and is filed as a follow-up.

### R3 — Adopt and enforce a key-shape convention

Make the flat-vs-dotted distinction a *contract*, not folklore:

- **Registry A (`agent_profiles`)** — flat, lowercase, hyphenated,
  **provider-led** slugs: `<provider>[-<model-or-shape>]`. No dots. EDGE 3
  of this ticket already moves `profiles.yaml` onto provider-led names
  (`codex-long`, `opencode-default`).
- **Registry B (boot-profiles)** — dotted ids (`nanite.backend.main`). This
  is already Tether's convention; Torque should treat **a dotted
  `agent_profile` value as a probable Registry B mistake** and say so in the
  error message. (`ResolveProfile`'s error already names Tether boot-profile
  ids as a thing it does *not* accept.)

A future hardening step: when `ResolveProfile` misses *and* the name
contains a `.`, add a targeted hint — "names with dots look like Tether
catalog boot-profile ids, which this arg does not accept". Filed as a
follow-up (small, in `internal/config`, but deferred to keep this change
reviewable).

---

## 5. Summary table — which API consumes which registry

| API / call site | Registry it consumes | Accepts the others? |
|---|---|---|
| `torque_session_launch` `agent_profile` arg | **A** — `agent_profiles` in `profiles.yaml` | No |
| Scheduler / executor dispatch (`GetProfileOrDefault`) | **A** (+ substrate `builtinProfiles`) | No |
| `config.AgentProfile.LaunchProfile` field | references a go-agent-launch launch profile (path / `path#id`) | n/a — explicit field, not the `agent_profile` arg |
| Tether boot path | **B** — boot-profiles | n/a (Tether-side) |
| Tether catalog ops | **C** — catalog agents / launches | n/a (Tether-side) |

**One-line mental model:** *Registry A is the only one Torque's
`agent_profile` arg accepts. B and C belong to Tether and reach Torque only
through an explicit `launch_profile:` reference inside an A entry.*

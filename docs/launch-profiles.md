# Launch Profiles

## TL;DR

`launch_profile` is Torque's first-class launch-family selector. It supersedes
the legacy `agent_profile` field as the user-facing entry point on
tasks, templates, and direct session-launch requests. The legacy field
remains accepted for migration; a launch resolver maps it onto built-in
launch profiles transparently.

## Object model

Three Go types in `internal/launchprofile`:

- **`LaunchProfile`** — the user-facing launch family. Carries
  `id`, `display_name`, `role`, `agent_profile` (the underlying registry
  lookup key), free-form `annotations`. Example IDs: `orchestrator.default`,
  `planner.default`, `worker.implementer`, `reviewer.code`, `default`.
- **`CompiledLaunchProfile`** — the resolved, defaulted form. Wraps a
  `LaunchProfile` plus the underlying `config.AgentProfile` resolved from
  `profiles.yaml`, plus a `Provenance` tag (`explicit` / `legacy_map` /
  `legacy_passthrough` / `default`).
- **`TaskLaunchOverlay`** — the per-task dynamic values that the stable
  launch family does not own: workdir, project ID, planted task-bundle
  native files, MCP loopback URL, the composed system prompt + kickoff
  markdown, and the agentkit runtime/provider IDs (pre-mapped by the
  caller).

## Assembly seam

`launchprofile.BuildLaunchPlan(compiled, overlay) → agentlaunch.LaunchPlan`
is the single translation point between Torque's launch-profile model and
the shared `agentlaunch` contract. Both code paths that spawn agents —
the scheduler/executor task-dispatch path and direct HTTP session launches
— funnel through `agent.Boot`, which calls this seam exactly once per
spawn.

```
boot.go
 ├── launchprofile.Resolve(req)            → CompiledLaunchProfile
 ├── build TaskLaunchOverlay               (runtime-kind mapped here)
 └── launchprofile.BuildLaunchPlan(...)    → agentlaunch.LaunchPlan
                                              │
                                              └── launcher.Compile
                                                  → launcher.Prepare
                                                  → providerplant.Plant
```

## Compatibility strategy

Precedence at the resolver:

1. Explicit `launch_profile` → looked up in the builtin catalog. Hits
   become `provenance=explicit`. Misses synthesize a pass-through
   `LaunchProfile` that uses the supplied name as the underlying
   `agent_profile` lookup key — operators can reference any
   `profiles.yaml` entry directly through `launch_profile` without
   pre-registering a builtin.
2. Empty `launch_profile` + non-empty `agent_profile` →
    - if `agent_profile` matches `legacyAgentProfileMap`
      (`orchestrator`, `planner`, `reviewer-end-agent`, `default`),
      maps onto the matching builtin (`provenance=legacy_map`).
    - otherwise, synthesizes a pass-through profile keyed by the legacy
      name (`provenance=legacy_passthrough`). This preserves operator-
      configured custom names like `codex-gpt5-long` without breaking
      changes.
3. Both empty → `default` builtin (`provenance=default`).

No data backfill is required. Existing rows continue to boot through the
legacy-compat path. New writes can opt into `launch_profile` at any time
on a per-row basis.

## Persistence

Migration `026_launch_profile.sql` adds a `launch_profile TEXT` column to
each of:

- `tasks` (`NOT NULL DEFAULT ''`)
- `task_templates` (nullable)
- `sessions` (`NOT NULL DEFAULT ''`)

Domain structs (`sqlstore.TaskRecord`, `TemplateRecord`, `SessionRecord`)
and `service.TaskCreateInput` / `TemplateCreateInput` / `TaskUpdate`
gain a `LaunchProfile` field.

## API surface

| Surface | Field | Notes |
|---|---|---|
| `POST /api/v1/tasks` | `launch_profile` (string, optional) | New. Legacy `agent_profile` still accepted. |
| `PATCH /api/v1/tasks/:id` | `launch_profile` (pointer) | New. |
| `GET /api/v1/tasks/:id` | `launch_profile` (string) | New. Always emitted; empty when unset. |
| `POST /api/v1/templates` | `launch_profile` (string) | New. |
| `PATCH /api/v1/templates/:id` | `launch_profile` (pointer) | New. |
| `POST /api/v1/sessions/launch` | `launch_profile` (string) | New. Validation of legacy `agent_profile` only fires when `launch_profile` is empty. |
| `POST /api/v1/sessions/:id/resume` | `launch_profile` (string) | New. |
| MCP `torque_session_create` | `launch_profile` (string) | `agent_profile` is no longer `Required` — either field satisfies. |
| MCP `torque_session_launch` | `launch_profile` (string) | Same. |
| MCP `torque_session_resume` | `launch_profile` (string) | New. |
| MCP `torque_task_create` | `launch_profile` (string) | New. |
| MCP `torque_task_update` | `launch_profile` (string) | New. |
| MCP `torque_template_create` | `launch_profile` (string) | New. |
| MCP `torque_template_update` | `launch_profile` (string) | New. |

The HTTP and MCP create paths require **at least one** of `launch_profile`
or `agent_profile`. `agent.Options.Validate` enforces this; the error
message names both fields.

## Builtin catalog (Phase 1)

| ID | Role | Underlying agent_profile |
|---|---|---|
| `orchestrator.default` | `orchestrator` | `orchestrator` |
| `planner.default` | `planner` | `planner` |
| `worker.implementer` | `executor` | `default` |
| `reviewer.code` | `reviewer-end-agent` | `reviewer-end-agent` |
| `default` | `executor` | `default` |

Operators register matching keys in `profiles.yaml` to override the
provider/model/runtime details. When a key is missing,
`config.GetProfileOrDefault` falls back to its own substrate-builtin set
(orchestrator/planner/reviewer-end-agent) and ultimately to the zero-value
profile.

## Telemetry

`agent.Boot` stamps two OTel span attributes on `torque.agent.boot`:

- `torque.agent.profile` — the resolved underlying agent_profile name.
- `torque.launch_profile` — the resolved launch family ID.

Plan-level annotations on the persisted `agentlaunch.LaunchPlan` carry
both `torque.agent_profile` and `torque.launch_profile` so forensic
tooling can group sessions either way.

## Tests

- `internal/launchprofile/resolver_test.go` — precedence rules, builtin
  catalog presence, legacy-map coverage, defensive-copy invariants.
- `internal/launchprofile/assemble_test.go` — `BuildLaunchPlan` assembles
  the expected `agentlaunch.LaunchPlan` shape (identity, annotations,
  unscoped-project fallback, permission threading).
- `internal/e2e/agent_boot/launch_profile_test.go` — four end-to-end
  paths through the real `agent.Boot` + fake runtime:
    1. `launch_profile`-only boot resolves and persists both fields.
    2. Explicit `launch_profile` wins over legacy `agent_profile`.
    3. Legacy `agent_profile`-only requests still boot (compat).
    4. Task bundle planting (`tasks/<id>/task.md`) survives the refactor.

## Deferred follow-ups

- **GUI work.** The React `apps/gui/` types and form components still
  carry only `agent_profile`. A second pass will add a launch-profile
  selector (dropdown of builtin IDs + free-text fallback), surface it on
  task / template / session create/edit/detail screens, and update
  display fixtures. Backend wire-up is ready.
- **Builtin catalog growth.** Phase 1 ships the five organic families.
  Future revisions can carry richer slot declarations (planted knowledge,
  procedures, tool packs) directly on `LaunchProfile` without re-shaping
  the seam.
- **External catalog / federation.** Out of scope for this pass. The
  package surface is designed so a file-backed or remote registry can
  slot in behind `LookupBuiltin` later.
- **`agent_profile` retirement.** Out of scope. The column stays for the
  full migration window. A future pass can backfill `launch_profile` from
  the legacy column and switch services to require the new field.

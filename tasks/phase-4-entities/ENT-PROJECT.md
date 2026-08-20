# ENT-PROJECT — Project: get/update (new), create fields, list wiring, archive

**Phase:** 4 — Per-entity rollout
**Status:** todo
**Depends on:** FIX-004, PRIM-001, PRIM-002, PRIM-004, DEC-002
**Blocks:** SWEEP-001
**Source:** ADR-0004 §5 (Project); audit "Per-Entity Findings → Project
[high][high]", "Executive Summary #8", "Proposed Direction E"

## Summary

Project is "underbuilt relative to its sibling entities" — per the audit,
likely "the highest-priority item in this whole list": no
`torque_project_get`, no `torque_project_update` exist at all, despite both
`ProjectService.Get`/`.Update` already being implemented and fully
functional at the service layer.

## Scope

**New tools (the headline gap)**:
- `torque_project_get` — fetch a single project's full record. Does not
  exist today at all.
- `torque_project_update` — edit any field (name, description, repo_path,
  agent_path, path arrays, permissions, rules, icon, status) without
  delete+recreate. Does not exist today at all. True partial-patch
  semantics (presence-in-payload), matching the design principle used
  system-wide.

**`create` field expansion**: `agent_path`, `icon`, and all path/permission
arrays are already settable in `ProjectCreateInput` but absent from
`torque_project_create`'s MCP schema (`project_tools.go:12-21`) — expose
them.

**`list` wiring**: `torque_project_list` currently hardcodes
`Project.List("")` — the `Status` field `ProjectFilter` already supports is
unused (`project_tools.go:56`). Wire it. Apply PRIM-001 (pagination — no
limit param today) and PRIM-002 (sort — apply the FIX-004 docstring fix if
it hasn't landed yet for this entity, don't duplicate).

**Archive/unarchive**: apply PRIM-004's generic archive primitive here —
`archived_at` + `archive`/`unarchive` tools. Previously moot since `Update`
(which could flip `status`) wasn't reachable via MCP at all; now that
`update` exists (this task), archive as an orthogonal concept becomes
meaningful.

**Registration**: apply whatever DEC-002 decided about feature-flag gating
vs always-on for this entity's tools.

## Acceptance criteria

- [ ] `torque_project_get` exists, returns the full project record by id.
- [ ] `torque_project_update` exists, true partial patch, covers every
      field listed above.
- [ ] `torque_project_create` accepts `agent_path`, `icon`, and the
      path/permission arrays.
- [ ] `torque_project_list` actually filters by `status` when passed, and
      supports pagination + sort per PRIM-001/PRIM-002.
- [ ] `archive`/`unarchive` tools exist and work per PRIM-004's pattern.
- [ ] Tool registration matches DEC-002's decision.

## Out of scope

- Project's artifact-scoped CRUD surface (`ProjectService.{List,Create,
  Get,Update,Delete}Artifact`, 5 methods, zero MCP exposure per the audit)
  — this is Artifact-entity-shaped work, and Artifact is explicitly out of
  ADR-0004's scope. Do not expose these here even though they live on
  `ProjectService`; they belong to Artifact's own future pass.

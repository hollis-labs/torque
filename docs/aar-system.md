# After-Action Report (AAR) system

Every Torque-orchestrated agent run is expected to file a structured
After-Action Report before signaling completion. The AAR captures process /
DX feedback the operator cannot infer from commits, comments, or error logs
— what was clunky, what could be automated, what manual step the system
should have done, sharp edges, and concrete suggestions for the next run.

This page is the contract: schema, submission protocol, storage convention,
and the CLI surface for querying / exporting aggregated AARs.

> Authoritative ticket: `CW-20260519-0088`. The 2026-05-17/18 controlled test
> runs showed every run surfaced invaluable feedback ad hoc in scattered
> task comments; this system captures it in a uniform queryable shape.

## Schema

An AAR is stored as a typed artifact attached to a task + run:

| Field          | Source                       |
|----------------|------------------------------|
| `Type`         | hardcoded `"aar"` (`aar.ArtifactType`) |
| `TaskID`       | substrate (loopback's bound task) |
| `RunID`        | substrate (most-recent run lookup) |
| `Content`      | rendered markdown (see below) |
| `Metadata`     | JSON blob with `schema`, `outcome`, identity, `reflection_populated`, `errors_count`, timestamps |

The markdown body is rendered by `internal/aar.Render`. Layout:

```
---
schema: aar/v1
outcome: success
task_id: CW-20260519-0088
run_id: 42
agent_profile: implementer-long
project_id: PRJ-20260416-0001
sprint_id: SP-20260519-0002
title: Build the AAR system
started_at: 2026-05-20T20:00:00Z
ended_at: 2026-05-20T22:15:00Z
---

# After-Action Report

## Summary
<one paragraph>

## What was clunky, confusing, or surprising?
<reflection>

## What could be automated or converted to a harness step?
<reflection>

## What manual step should the system have done?
<reflection>

## Sharp edges hit and workarounds
<reflection>

## Suggestions for the next run
<reflection>

## Errors / issues encountered
- one bullet per error
```

Empty reflection sections render as `_(none)_` so the document shape stays
uniform — a reader can see at a glance which questions the agent had
nothing to say about. Every section heading is present in every AAR.

### Outcome values

```
success | partial | blocked | failed
```

Empty / unset defaults to `success` at write time. The outcome is the agent's
self-classification, distinct from the run record's `status` (which captures
harness-side truth).

## Submission protocol

The agent files its AAR via the `torque_aar_submit` MCP tool on the
loopback. It is a self-task tool: every worker session that gets a loopback
adapter also gets the AAR primitive. Call exactly once per run, AFTER the
verification comment and BEFORE `torque_task_review` / `torque_task_blocked`.

The agent supplies the structured reflection; the substrate fills the
run-identity fields:

```
torque_aar_submit(reflection_json=json.dumps({
  "summary": "Stood up the AAR system. PR opened.",
  "outcome": "success",
  "clunky": "docs/torque-agent-guide referenced in boot.md does not exist",
  "automatable": "AAR-template scaffolding for repeat tasks",
  "manual_should_be_auto": "Identity stamping (provider, session_id)",
  "sharp_edges": "Loopback adapter does not know TORQUE_WORK_ROOT",
  "suggestions": "Thread the work_root into NewLoopback so file paths can be authoritative",
  "errors": ["transient gh rate limit on first push"]
}))
```

The loopback handler:

1. Validates the payload shape + outcome.
2. Resolves identity fields from the task record + the most recently
   started run for that task (the worker's active run).
3. Renders the markdown body via `aar.Render`.
4. Persists as `ArtifactRecord{Type:"aar", TaskID, RunID, Content,
   Metadata}` and returns the created artifact record.

A vacuous AAR (every reflection string empty) is still a valid, queryable
signal — submit one rather than skip. The CLI aggregator separates "filed
with no friction" from "never filed."

## Storage convention

AARs live in the `artifacts` table with `type = "aar"`. They are linked by
`task_id` and `run_id`. There is no on-disk-file convention by default — the
markdown body is stored inline in the artifact's `content` column. The
`torque aar export` subcommand materializes a directory of markdown files
on demand when team workflows need them as files.

The metadata blob holds the structured fields downstream tooling filters
on without re-parsing the markdown:

```json
{
  "schema": "aar/v1",
  "outcome": "success",
  "agent_profile": "implementer-long",
  "project_id": "...",
  "sprint_id": "...",
  "epic_id": "",
  "started_at": "2026-05-20T20:00:00Z",
  "ended_at": "2026-05-20T22:15:00Z",
  "reflection_populated": 6,
  "errors_count": 1
}
```

## CLI

```
torque aar list                          # newest-first table
torque aar list --task CW-20260519-0088  # one task
torque aar list --outcome partial
torque aar list --since 7d               # absolute RFC3339 or duration (`24h`, `7d`)
torque aar list --json                   # JSON projection (no markdown body)
torque aar list --all                    # ignore the default 50-row limit

torque aar show <artifact-id>            # print one AAR body
torque aar export --dir ./aars --since 24h --outcome failed
```

`export` writes one `.md` file per matching AAR named
`<task_id>-run-<run_id>.md` (or `<task_id>-<artifact_id>.md` when no run was
attached). Existing files are overwritten — the DB row is authoritative.

The CLI reads the same SQLite store the daemon writes to (resolved via
`config.Load` / `appdb.Open`), so it works offline / in CI / during
migrations.

## What the substrate does, what the agent does

| Responsibility            | Owned by  |
|---------------------------|-----------|
| `schema` version stamp    | substrate |
| `task_id`, `run_id`       | substrate |
| `agent_profile`, project / sprint / epic IDs | substrate (from task record) |
| `started_at` / `ended_at` | substrate (from run record + submission timestamp) |
| `title`                   | substrate (from task record) |
| Reflection answers        | agent     |
| Outcome classification    | agent     |
| Markdown rendering        | substrate (deterministic from inputs) |
| Persistence / linking     | substrate |
| Submission timing         | agent (between verification comment and review signal) |

When the substrate can't resolve a field (e.g. `provider` from a session
that pre-dates the unified registry), the field is simply omitted from the
frontmatter rather than fabricated.

## Boot-template integration

The default worker boot template (`internal/runtime/scheduler/templates/
default-worker.md`) requires AAR submission as part of the completion
contract — alongside committing, pushing, and self-transitioning. A worker
that skips AAR submission still completes successfully (the AAR is
post-hoc reflection, not a gate on review), but missing AARs degrade the
team's aggregate signal and the reviewer end-agent may flag them.

## Versioning

The schema constant in `internal/aar.Schema` is `aar/v1`. Bump when adding
or removing reflection sections, or when changing frontmatter key meaning
(not when adding additive fields — parsers tolerate unknown keys).

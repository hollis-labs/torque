# Clockwork Manifold — Task Model MVP Design

**Date:** 2026-04-16
**Status:** Draft (pre-implementation)
**Authors:** Chrispian + Claude (planner, brainstorm session)
**Predecessor spec:** `2026-04-07-clockwork-manifold-design.md` (canonical identity + 35-field task record)

---

## 1. Purpose

Firm up the Clockwork backend and task data model to a dogfoodable MVP. Explicit goals:

- Model task-type diversity without branching the scheduler: agent-executed, external (human/other-system), wait-for-predicate, decision/checkpoint, parent/container.
- Make "who originated this task" a first-class, queryable facet so routing, auto-approve, and trust rules are config + data, not reasoning.
- Give humans and orchestrator agents a structured way into the loop mid-task via typed checkpoints.
- Introduce task templates so the 35-field record stops leaking into freeform MCP payloads.
- Preserve all existing primitives (FSM, scheduler, executors, deliverables gate, escalation engine, SSE events). This spec **adds**; it does not rewrite.

Non-goals:

- Frontend / GUI changes — out of scope.
- Fragments Engine migration or import tooling — separate track.
- Vanta Conduit context-packet integration — separate wiring task.
- `go-envelope` and `go-signals` shared libs — separate extractions (BLGs 030 and the existing go-extractions idea).
- Persistent orchestrator agent as a first-class entity — BLG 031.

## 2. Architecture overview

```
User / Agent / API / Webhook
    |
    v
clockwork_task_create         clockwork_task_create_from_template
    |                                    |
    +------------+-----------------------+
                 |
                 v
          Task (35 existing fields + 6 new facet fields)
                 |
       +---------+---------+
       |                   |
   auto_execute=true   auto_execute=false
       |                   |
       v                   v
   Scheduler          Manual surface
  (kind-aware          (external tasks,
   validation,           user-owned work,
   dispatch by           status updates via MCP)
   Executor)
       |
       v
   Executor runs --> CLOCKWORK_* signals --> Run events
       |                   ^
       v                   |
   Checkpoint? --yes--> emit {type, payload}  ---(wait)---  response via MCP
       |                                                       |
       +-------------<------resume with response-<-------------+
       |
       v
   Result + Deliverables gate --> Lifecycle rule --> status transition
```

Four additions vs. what ships today:

1. **Six facet columns on `tasks`** — `kind`, `source_type`, `source_ref`, `trust`, `checkpoint_mode`, `on_checkpoint_response`. Descriptive and filterable, not scheduler-branching.
2. **`task_templates` table** — first-class, versioned, CRUD via MCP, carries defaults for every task-shaping field.
3. **`checkpoints` table** — correlates emit/respond with opaque JSON payloads. Scheduler parks `checkpoint_mode=blocking` tasks until response; a timeout sweeper handles expiry and cancellation shares the same row-flip mechanism.
4. **Kind-aware validation** — service layer rejects malformed tasks per `kind`. Not in scheduler. Not in FSM. Pure input validation.

Unchanged: FSM, picker, worker pool, lifecycle manager, deliverables gate, escalation engine, cost tracker, SSE event bus, executor interface, MCP adapter structure, all 35 existing canonical fields, sentinel budgets, relational tags, typed deliverables.

## 3. Task object deltas

### 3.1 New columns (migration 007, strict — no back-compat)

```sql
ALTER TABLE tasks
  ADD COLUMN kind                    TEXT NOT NULL
    CHECK (kind IN ('agent','external','wait','decision','parent')),
  ADD COLUMN source_type             TEXT NOT NULL
    CHECK (source_type IN ('agent','user','api','system','webhook','import')),
  ADD COLUMN source_ref              TEXT,
  ADD COLUMN trust                   TEXT NOT NULL
    CHECK (trust IN ('trusted','normal','untrusted')),
  ADD COLUMN checkpoint_mode         TEXT NOT NULL
    CHECK (checkpoint_mode IN ('none','blocking','non_blocking')),
  ADD COLUMN on_checkpoint_response  TEXT NOT NULL
    CHECK (on_checkpoint_response IN ('resume','review','custom'));

CREATE INDEX idx_tasks_kind_status      ON tasks(kind, status);
CREATE INDEX idx_tasks_source           ON tasks(source_type, source_ref);
CREATE INDEX idx_tasks_checkpoint_mode  ON tasks(checkpoint_mode);
```

Strict NOT NULL without SQL defaults. Defaults live in the service layer (`TaskCreateInput` applies them before validation). Dev DBs rebuild; no legacy rows to migrate.

### 3.2 Kind interaction matrix

| `kind`     | `executor` required? | `auto_execute` default | Scheduler picks up?   | Predicate required? | Allowed `checkpoint_mode` |
|------------|----------------------|------------------------|-----------------------|---------------------|---------------------------|
| `agent`    | yes                  | true                   | yes                   | no                  | any                       |
| `external` | no (forbidden)       | false                  | no                    | no                  | `none` or `blocking`      |
| `wait`     | no                   | true                   | yes (predicate poll)  | yes (in metadata)   | any                       |
| `decision` | no                   | false                  | no (human-driven)     | no                  | `blocking` (required)     |
| `parent`   | no                   | true                   | yes (auto-rolls up)   | no                  | any                       |

### 3.3 `kind=wait` predicate shape

Stored in `metadata.wait` — flexible, no new column:

```json
{
  "wait": {
    "predicate_type": "task_done | url_reachable | file_exists | git_tag | vanta_doc | custom",
    "params": { },
    "poll_interval_seconds": 60,
    "max_duration_seconds": 3600
  }
}
```

MVP ships predicate evaluators for `task_done`, `url_reachable`, `file_exists`. Registry lives in `internal/runtime/waitpoll/`. Custom/plugin predicates deferred to BLG-034.

### 3.4 `kind=parent` rollup

Parent status is **derived** each scheduler tick from children referenced in `metadata.children` (array of task IDs):

- Any child `blocked` → parent `blocked`.
- All children `done` → parent transitions per its own `on_done` rule.
- Else → parent stays in current status.

No new join table; no back-pointer FK. Simple.

### 3.5 Validation rules (service layer)

`task_validation.go` grows a `validateTaskKind` pass in `Create` and `Update`:

- `kind=agent` + `executor=""` → 422 "executor required for kind=agent".
- `kind=external` + `executor != ""` → 422 "executor not allowed for kind=external".
- `kind=external` + `auto_execute=true` → 422 "external tasks cannot be auto-executed".
- `kind=wait` + no `metadata.wait.predicate_type` → 422.
- `kind=decision` + `checkpoint_mode != "blocking"` → 422.
- `kind=decision` + `auto_execute=true` → 422 "decision tasks are human-driven; auto_execute must be false".
- `kind=parent` + `metadata.children` empty or missing → warning only.

### 3.6 Trust defaulting

On create, if `trust` not explicitly set:

| `source_type`                  | Default trust |
|---------------------------------|---------------|
| `system`                        | `trusted`     |
| `user`, `agent`                 | `normal`      |
| `webhook`, `import`             | `untrusted`   |
| `api`                           | `normal`      |

Known-source registry (deferred to BLG-035) will later override these by `(source_type, source_ref)` pair.

## 4. Checkpoint mechanics

Opaque-payload design per Q4 Option C. `go-envelope` (BLG-030) provides schema vocabulary later.

### 4.1 New table (migration 008)

```sql
CREATE TABLE checkpoints (
  id                     INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id                TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id                 INTEGER REFERENCES runs(id) ON DELETE SET NULL,
  correlation_id         TEXT NOT NULL UNIQUE,
  type                   TEXT NOT NULL,
  payload_json           TEXT NOT NULL,
  response_json          TEXT,
  emitter_source_type    TEXT NOT NULL,
  emitter_source_ref     TEXT,
  responder_source_type  TEXT,
  responder_source_ref   TEXT,
  emitted_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  responded_at           TIMESTAMP,
  timeout_at             TIMESTAMP,
  status                 TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','responded','timed_out','canceled'))
);

CREATE INDEX idx_checkpoints_task_status  ON checkpoints(task_id, status);
CREATE INDEX idx_checkpoints_pending      ON checkpoints(status) WHERE status = 'pending';
CREATE INDEX idx_checkpoints_correlation  ON checkpoints(correlation_id);
```

`correlation_id` is a ULID generated on emit. `type` is a free string in MVP — registry lands with `go-envelope`.

### 4.2 Emit flow

Executor emits during a run:

```
CLOCKWORK_CHECKPOINT <correlation_id> <type> <base64(payload_json)>
```

Scheduler:

1. Parses the signal, inserts a row `status=pending`.
2. Broadcasts `checkpoint.emitted` via SSE.
3. If `task.checkpoint_mode == 'blocking'`:
   - Transitions task `doing → review` with `BlockedReason = "awaiting checkpoint <correlation_id>"`.
   - Releases the worker slot.
4. If `task.checkpoint_mode == 'non_blocking'`:
   - Executor continues. Run completes normally. Task does not pause.

### 4.3 Response path

`clockwork_task_checkpoint_respond` accepts `{correlation_id, response_json, responder_source_type, responder_source_ref}`:

1. Row updated: `response_json`, `responder_*`, `responded_at`, `status=responded`.
2. If task is parked in `review` on this checkpoint, lifecycle manager applies `on_checkpoint_response`:
   - `resume` → transition `review → todo`, re-queue. Next run sees response in `metadata.checkpoint_responses[correlation_id]`.
   - `review` → stay in `review`. Human manually transitions. Response attached.
   - `custom` → plugin filter `checkpoint.on_response` decides.
3. Broadcasts `checkpoint.responded`.

### 4.4 Timeout

Each checkpoint may set `timeout_at`. A sweeper piggybacks on the existing scheduler tick:

- Any `status=pending AND timeout_at < now()` → `status=timed_out`, emit `checkpoint.timed_out`.
- If task `checkpoint_mode=blocking`, transition `review → blocked` with `BlockedReason="checkpoint <id> timed out"`. Human intervention required.

MVP does not ship default-on-timeout responses. Plugin authors can listen for `checkpoint.timed_out` and call `respond` with a default. Deferred enhancement: BLG-033.

### 4.5 Cancel

Reuses the same row-flip mechanism as timeout/respond. `clockwork_task_checkpoint_cancel` accepts `{correlation_id, reason, canceler_source_type, canceler_source_ref}`:

```
status             = 'canceled'
responded_at       = now()
response_json      = {"canceled": true, "reason": "..."}
responder_source_* = canceler_*
```

Task stays in `review` with `BlockedReason = "checkpoint <correlation_id> canceled: <reason>"`. Human transitions from there. No new `on_checkpoint_cancel` field.

Anyone with MCP access can cancel in MVP (no ACL). Gating deferred to BLG-033 + BLG-035.

Error cases: cancel already-terminal checkpoint → 409; unknown correlation_id → 404.

### 4.6 Signals added to the `CLOCKWORK_*` vocabulary

```
CLOCKWORK_CHECKPOINT <correlation_id> <type> <base64(payload_json)>
CLOCKWORK_CHECKPOINT_AWAIT <correlation_id>
```

Parser is in-tree today at `internal/runtime/executor/signal.go`. Phase B (see §8.2) adds `CLOCKWORK_CHECKPOINT` and `CLOCKWORK_CHECKPOINT_AWAIT` to the existing parser. A future `go-signals` extraction (separate effort — see `agent-workspaces/knowledge/ideas/go-extractions.md`) will lift this package into a shared library; Clockwork will consume it post-extraction. The MVP does not depend on the extraction.

### 4.7 What MVP **does not** do

- No schema validation of payload or response JSON. Both opaque strings.
- No multi-responder aggregation. First response wins; second gets 409. (BLG-033.)
- No nested checkpoint dependencies. Flat model. (BLG-033.)
- No ACL on emit/respond/cancel. (BLG-033, BLG-035.)
- No built-in UI.

## 5. Task templates

First-class, versioned, instantiable shapes.

### 5.1 New table (migration 009)

```sql
CREATE TABLE task_templates (
  id                     TEXT NOT NULL,
  version                INTEGER NOT NULL DEFAULT 1,
  name                   TEXT NOT NULL,
  description            TEXT NOT NULL,
  kind                   TEXT NOT NULL,
  auto_execute           BOOLEAN NOT NULL DEFAULT true,
  executor               TEXT,
  agent_profile          TEXT,
  system_prompt          TEXT,
  tools                  TEXT,
  permissions            TEXT,
  environment            TEXT,
  cost_budget            REAL,
  max_retries            INTEGER DEFAULT 3,
  max_duration_ms        INTEGER,
  token_budget           INTEGER,
  on_done                TEXT NOT NULL DEFAULT 'review',
  on_fail                TEXT NOT NULL DEFAULT 'retry',
  on_review              TEXT NOT NULL DEFAULT 'pause',
  on_done_merge          TEXT NOT NULL DEFAULT 'none',
  escalation_chain       TEXT,
  quality_gates          TEXT,
  deliverables           TEXT,
  checkpoint_mode        TEXT NOT NULL DEFAULT 'none',
  on_checkpoint_response TEXT NOT NULL DEFAULT 'resume',
  metadata_template      TEXT,
  required_vars          TEXT,
  tags                   TEXT,
  is_archived            BOOLEAN NOT NULL DEFAULT false,
  created_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

  PRIMARY KEY (id, version)
);

CREATE INDEX idx_templates_kind ON task_templates(kind) WHERE is_archived = false;
```

JSON fields (`tools`, `permissions`, `environment`, `escalation_chain`, `quality_gates`, `deliverables`, `metadata_template`, `required_vars`, `tags`) are `TEXT` storing JSON.

### 5.2 Versioning semantics

- `clockwork_template_update` = new row with `version = max(version) + 1`. Old versions preserved.
- Tasks carry `metadata.template_ref = {id, version}` — not a FK. Templates can be archived; historical references survive.
- No mutable-in-place edits on a live version. Templates are immutable per version.
- `clockwork_template_archive` removes a template from the instantiation catalog; its rows remain for task-history lookup. This is the recommended lifecycle end-state.
- `clockwork_template_delete` (hard delete) is allowed **only when no tasks reference any version of the template** (i.e., no task row has `metadata.template_ref.id == template.id`). Otherwise returns 409 with a list of referencing task IDs. Prefer archive; delete is for mistakes caught before use.

### 5.3 Instantiation

`clockwork_task_create_from_template` accepts:

```
template_id         string   required
template_version    int      optional (default: latest non-archived)
title               string   required (never templated)
description         string   required (never templated unless template sets one with vars)
vars                map      required if template has required_vars
overrides           map      optional (any task field)
sprint_id / project_id / epic_id / tags   optional
```

Apply order:

1. Load template `(id, version)`.
2. Build `TaskCreateInput` from template fields.
3. Resolve `{{var}}` placeholders in templatable fields; fail with 422 if any unresolved placeholder remains.
4. Apply caller `overrides` (last wins).
5. Apply caller title/description/grouping/tags.
6. Stamp `metadata.template_ref = {id, version}`.
7. Run `validateTaskWrites` + `validateTaskKind`.
8. Insert and return.

### 5.4 Variable resolution

Flat `{{var}}` substitution in: `description`, `system_prompt`, `environment` values, string leaves of `metadata_template` (recursive). No expressions, no conditionals, no escapes. Unresolved `{{var}}` → 422.

Richer templating engines (Mustache, Go text/template, conditionals, defaults) deferred to BLG-032.

### 5.5 Example templates (illustrative, not a seed)

```yaml
# backend-fix
id: backend-fix
kind: agent
auto_execute: true
executor: cli
agent_profile: claude-code-sonnet
on_done: review
on_fail: retry
on_done_merge: pr
max_retries: 2
quality_gates: ["go vet ./...", "go test ./..."]
deliverables:
  - { type: diff, required: true }
  - { type: test-results, required: true }
  - { type: pr-link, required: false }
required_vars: [repo_path]
metadata_template:
  working_dir_template: "{{repo_path}}"
tags: [code, backend]
```

```yaml
# external-chore
id: external-chore
kind: external
auto_execute: false
on_done: close
deliverables:
  - { type: note, required: true, description: "How it was done" }
tags: [external, chore]
```

```yaml
# wait-for-git-tag
id: wait-for-git-tag
kind: wait
auto_execute: true
on_done: close
required_vars: [repo_path, tag]
metadata_template:
  wait:
    predicate_type: git_tag
    params: { repo: "{{repo_path}}", tag: "{{tag}}" }
    poll_interval_seconds: 60
    max_duration_seconds: 7200
tags: [wait, dependency]
```

```yaml
# decision-checkpoint
id: decision-checkpoint
kind: decision
auto_execute: false
checkpoint_mode: blocking
on_checkpoint_response: resume
deliverables:
  - { type: finding, required: true }
tags: [decision, human-in-loop]
```

```yaml
# sprint-split-parent
id: sprint-split-parent
kind: parent
auto_execute: true
on_done: close
deliverables:
  - { type: report, required: true, description: "Sub-session rollup" }
tags: [parent, meta]
```

These live as YAML in `docs/templates/` post-implementation for reference. Source of truth is the DB via MCP create calls.

### 5.6 What MVP **does not** do

- No inheritance (`extends: other-template`). Flat. (BLG-032.)
- No team-shared catalog. Per-instance only. (BLG-032.)
- No publish/export/import. (BLG-032.)
- No scheduled instantiation (cron → template → task). (BLG-032.)
- No template-driven FSM customization. Fixed FSM. (BLG-032 — likely won't-do.)

## 6. Template-driven task creation flow

### 6.1 Service-layer defaults

`TaskCreateInput.extractCreateFields` applies API-level defaults before validation:

```
kind                     = "agent"     (if empty)
source_type              = "user"      (if empty)
trust                    = resolveTrust(source_type, source_ref, settings)
checkpoint_mode          = "none"      (if empty)
on_checkpoint_response   = "resume"    (if empty)
```

All existing defaults (`Status=todo`, `OnDone=review`, `MaxRetries=3`, etc.) remain in `applyDefaults`.

### 6.2 Instantiation path

New `internal/service/template.go`:

```go
type TemplateService struct { ... }

func (s *TemplateService) Instantiate(
    ctx context.Context,
    req TemplateInstantiateInput,
) (*TaskRecord, error)
```

Steps as in §5.3. Field-mapping `applyTemplateToInput` lives in one function so future additions to either table touch one place.

### 6.3 Variable resolver

`internal/service/template_vars.go`:

```go
func ResolveVars(s string, vars map[string]string) (string, error)
```

Walks `s`, replaces `{{key}}` with `vars[key]`. Returns error on any remaining `{{...}}`.

Applied recursively to string leaves of `metadata_template`.

### 6.4 MCP adapter

`internal/mcpadapter/template_tools.go` registers the seven template tools; shape mirrors `task_tools.go`.

### 6.5 Test additions

- Store: CRUD, version monotonic increment, archive semantics, `GetTemplate(id, nil)` returns latest non-archived.
- Service: variable resolution across all field types, missing required vars → 422, override merge order, archived template refusal, kind validation propagates, tags applied.
- HTTP/MCP: round-trip create → get → update → instantiate → task `metadata.template_ref` correct.
- Integration: create template, instantiate, scheduler picks up, mock executor runs, deliverables gate + lifecycle honor template config.

Target: ~35 new tests across the template track alone; total new tests across all phases ~115.

## 7. MCP surface deltas

### 7.1 New tools (13)

Templates (7):
```
clockwork_template_create
clockwork_template_get
clockwork_template_update
clockwork_template_delete
clockwork_template_archive
clockwork_template_list
clockwork_task_create_from_template
```

Checkpoints (6):
```
clockwork_task_checkpoint_emit
clockwork_task_checkpoint_respond
clockwork_task_checkpoint_cancel
clockwork_task_checkpoint_list
clockwork_task_checkpoint_get
clockwork_task_checkpoints_pending
```

### 7.2 Modified tools

- `clockwork_task_create` and `clockwork_task_update` accept the six new facet fields. Defaults applied when omitted. No breaking change.
- `clockwork_task_list` gains filters: `kind`, `source_type`, `source_ref`, `trust`, `checkpoint_mode`.
- `clockwork_task_search` adds the same facets to searchable indexes.
- `clockwork_task_get` response includes facet fields, nested `checkpoints` summary (count + latest status), and `template_ref` if set.

### 7.3 Unchanged

`clockwork_task_transition`, `clockwork_task_bulk_transition`, `clockwork_task_delete`, `clockwork_run_*`, `clockwork_artifact_*`, `clockwork_comment_*`, `clockwork_scheduler_*`, `clockwork_health`, `clockwork_settings_*`, and all sprint/project/epic tools.

### 7.4 New lifecycle events

```
template.created
template.updated
template.archived
task.from_template
checkpoint.emitted
checkpoint.responded
checkpoint.canceled
checkpoint.timed_out
```

Land in the existing event emitter. SSE subscribers and plugin hooks automatically receive them.

## 8. Migration plan & sequencing

### 8.1 Migrations

```
007_task_facets.sql    -- ALTER tasks + indexes
008_checkpoints.sql    -- CREATE TABLE checkpoints + indexes
009_task_templates.sql -- CREATE TABLE task_templates + indexes
```

Order: 007 → 008 → 009. (Migration 006 is already taken by `006_run_events_nullable_run.sql`.)

### 8.2 Implementation phases

| Phase | Scope | Depends on | Est. (dev days) |
|-------|-------|-----------|-----------------|
| A | Migration 007 + facet columns + `validateTaskKind` + service defaults + trust resolver + MCP facet fields + tests | — | 1–2 |
| B | Migration 008 + `checkpoints` table + checkpoint service + scheduler signal handling + timeout sweep + 6 MCP tools + tests | A | 1–2 |
| C | `internal/runtime/waitpoll/` + predicate registry + three built-ins + scheduler dispatch for `kind=wait` + tests | A | 1 |
| D | `kind=parent` rollup on scheduler tick + tests | A | 0.5 |
| E | Migration 009 + template table + `TemplateService` + `ResolveVars` + 7 MCP tools + tests | A | 1.5 |
| F | Dogfood — create five example templates via MCP, commit YAML reference to `docs/templates/`, run an end-to-end template → scheduler → mock executor → deliverables gate pass | A–E | 0.5 |

Sequencing: A first. B, C, D, E can parallelize after A. F is the validation gate before declaring MVP.

Total rough sizing: **5–7 dev days**. Planning estimate only.

### 8.3 Success criteria for MVP

F passes. Specifically:

1. All five example templates create cleanly via MCP.
2. `clockwork_task_create_from_template backend-fix --vars={repo_path: /tmp/demo}` produces a task that the scheduler picks up and dispatches to the mock executor.
3. A `kind=decision` task with `checkpoint_mode=blocking` emitted via signal parks the task correctly; `clockwork_task_checkpoint_respond` resumes it; `clockwork_task_checkpoint_cancel` also closes it cleanly.
4. A `kind=wait` task with `predicate_type=task_done` blocks until the target task completes, then transitions to `done`.
5. A `kind=external` task can be created, status-transitioned manually via MCP, and honors its deliverables gate.
6. A `kind=parent` task with two children auto-transitions when both children reach `done`.
7. Full test suite green: existing 20 packages + new packages. `go vet` clean.

### 8.4 Out-of-scope / deferred

| Topic | Deferred to |
|-------|-------------|
| Source chain multi-hop provenance | BLG-029 |
| `go-envelope` shared schema lib | BLG-030 |
| Persistent orchestrator agent entity | BLG-031 |
| Template inheritance / catalog / scheduled instantiation / richer templating / custom FSM | BLG-032 |
| Multi-responder checkpoints / nesting / default-on-timeout / ACL | BLG-033 |
| Custom wait predicates as plugin hook | BLG-034 |
| Known-source trust registry | BLG-035 |
| Fragments Engine migration / import tooling | KB GAP — `knowledge/projects/clockwork-manifold.md` |
| Vanta Conduit context-packet integration | KB GAP + Nanite Phase 3 S3 |
| Context hot-swapping | KB GAP + Nanite Phase 3 S3 |
| Automated oversight / Special Agent patterns | subsumed by BLG-031 |
| Multi-project priority resolution | KB GAP |
| Frontend / GUI changes | explicitly out of scope |

## 9. Invariants preserved

All cross-portfolio `feedback_service_invariants` principles hold:

- **Append-only** — checkpoints append; templates new-version-on-edit; task audit trail unchanged.
- **Deterministic** — kind validation, trust defaulting, variable resolution, timeout sweeping are rule-driven. No reasoning.
- **Selectors-not-processors** — templates describe shape; scheduler reads shape; plugins compose via filters and hooks.
- **Namespace-owned** — `checkpoints` and `task_templates` are Clockwork's. Envelope schemas are `go-envelope`'s. Signal parsing is `go-signals`'s. No cross-namespace mutation.
- **Audit-canonical** — DB > files > ambient. Templates and checkpoints are DB-canonical. YAML in `docs/templates/` is documentation, not source of truth.

Additional Clockwork-specific principles preserved:

- **Task-only scope.** Everything added is a task-shaping facet or a task-lifecycle mechanism. No new domain entities beyond `task_templates` (templates are task factories) and `checkpoints` (task-scoped messages).
- **Agent-first, composition over coupling.** Facets surface to agents at create-time and query-time. Templates become discoverable shapes. Checkpoints become a first-class surface for orchestrator agents to observe and respond. None of these require orchestration coupling — composition happens through MCP tools + SSE events.
- **Tools provide functionality, not process.** Process stays in the harness (agentrc, boot profiles, templates). New MCP tools expose capabilities — they do not hard-code workflows.
- **Canonical objects.** Templates are DB rows; checkpoints are DB rows; facets are DB columns. YAML examples are docs.

## 10. References

- Canonical spec: `docs/superpowers/specs/2026-04-07-clockwork-manifold-design.md`
- Evaluation session: `agent-workspaces/planning/clockwork-executor-wiring/evaluation-session-2026-04-15.md`
- Project oracle: `agent-workspaces/knowledge/projects/clockwork-manifold.md`
- Session tracking: `agent-workspaces/execution/clockwork-manifold/planner/2026-04-16/`
- BLGs filed this session (in Engine, project `clockwork-manifold`): 029, 030, 031, 032, 033, 034, 035.

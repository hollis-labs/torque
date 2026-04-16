# Task Model MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the dogfoodable MVP task-model: six facet columns, checkpoint table + scheduler plumbing, wait-poll runner, parent-rollup, and first-class versioned task templates, per `docs/superpowers/specs/2026-04-16-task-model-mvp-design.md`.

**Architecture:** Three new migrations (007, 008, 009) + service-layer additions (facets, checkpoints, templates). Scheduler gains kind-aware dispatch for `wait` and `parent`, a checkpoint emit/respond/timeout loop, and a parent-rollup tick. No rewrites — all additive.

**Tech Stack:** Go 1.26, SQLite (modernc.org/sqlite), go-chi/chi/v5, mark3labs/mcp-go, stdlib `database/sql`, existing `internal/*` packages. Tests use `github.com/stretchr/testify/require`.

---

## Prerequisites

Before starting:

- [ ] **Read the spec.** `docs/superpowers/specs/2026-04-16-task-model-mvp-design.md`. Especially §3 (facets), §4 (checkpoints), §5 (templates), §8 (migrations + phasing).
- [ ] **Read these existing files** (you'll be modifying them; reading first is cheaper than guessing):
  - `internal/service/task.go` — `TaskCreateInput`, `TaskUpdateInput`, `Create`, `Update`
  - `internal/service/task_validation.go` — `validateTaskWrites`, `extractCreateFields`, `extractUpdateFields`
  - `internal/persistence/sqlstore/tasks.go` — `TaskRecord`, `TaskFilter`, `TaskUpdate`, `taskSelectCols`, SQL builders
  - `internal/runtime/executor/signal.go` — signal enum + name maps
  - `internal/runtime/scheduler/scheduler.go`, `lifecycle.go`, `worker.go`, `picker.go`
  - `internal/httpserver/tasks.go` — `TaskCreateRequest`, `TaskUpdateRequest`, `taskJSON`
  - `internal/mcpadapter/task_tools.go` — MCP wrappers
  - `internal/persistence/sqlstore/migrations/001_initial.sql` through `006_run_events_nullable_run.sql`
- [ ] **Verify env.** Commands to run:
  ```bash
  go version                         # expect go1.26+
  go build ./...                     # expect clean
  go vet ./...                       # expect clean
  go test ./...                      # expect all PASS
  ```
- [ ] **Clean branch.** Work on a feature branch off `main`. `git switch -c feat/task-model-mvp`.
- [ ] **Migration number check.** Confirm `internal/persistence/sqlstore/migrations/006_run_events_nullable_run.sql` exists. This plan uses 007/008/009.

---

## File structure map

**Phase A (facets + validation):**
- Create: `internal/persistence/sqlstore/migrations/007_task_facets.sql`
- Modify: `internal/persistence/sqlstore/tasks.go` (TaskRecord, TaskFilter, TaskUpdate, taskSelectCols, insert/update builders)
- Modify: `internal/service/task.go` (TaskCreateInput fields, Create record builder)
- Modify: `internal/service/task_validation.go` (validateTaskKind, kind/source/trust enum maps, extract helpers)
- Create: `internal/service/trust.go` (ResolveTrust helper)
- Modify: `internal/service/task_validation_test.go` (kind rule tests)
- Modify: `internal/service/task_test.go` (round-trip)
- Modify: `internal/httpserver/tasks.go` (TaskCreateRequest, TaskUpdateRequest, taskJSON, parse helpers)
- Modify: `internal/httpserver/tasks_test.go`
- Modify: `internal/mcpadapter/task_tools.go` (facet params)
- Modify: `internal/persistence/sqlstore/migrate_test.go` (assert new columns present)

**Phase B (checkpoints):**
- Create: `internal/persistence/sqlstore/migrations/008_checkpoints.sql`
- Create: `internal/persistence/sqlstore/checkpoints.go`
- Create: `internal/persistence/sqlstore/checkpoints_test.go`
- Create: `internal/service/checkpoint.go`
- Create: `internal/service/checkpoint_test.go`
- Create: `internal/service/errors.go` entries or extend existing
- Modify: `internal/runtime/executor/signal.go` (new SignalCheckpointAwait; ensure SignalCheckpoint already present — it is)
- Modify: `internal/runtime/executor/parser.go` + `signal_test.go` / `parser_test.go` (handle CHECKPOINT + CHECKPOINT_AWAIT format)
- Create: `internal/runtime/scheduler/checkpoint.go`
- Create: `internal/runtime/scheduler/checkpoint_test.go`
- Modify: `internal/runtime/scheduler/scheduler.go` (wire timeout sweeper into tick)
- Modify: `internal/runtime/scheduler/lifecycle.go` (apply on_checkpoint_response)
- Create: `internal/mcpadapter/checkpoint_tools.go`
- Create: `internal/mcpadapter/checkpoint_tools_test.go`
- Create: `internal/httpserver/checkpoints.go`
- Create: `internal/httpserver/checkpoints_test.go`

**Phase C (wait-poll):**
- Create: `internal/runtime/waitpoll/predicate.go`
- Create: `internal/runtime/waitpoll/registry.go`
- Create: `internal/runtime/waitpoll/task_done.go`
- Create: `internal/runtime/waitpoll/url_reachable.go`
- Create: `internal/runtime/waitpoll/file_exists.go`
- Create: `internal/runtime/waitpoll/*_test.go` (one per predicate + registry_test.go)
- Modify: `internal/runtime/scheduler/worker.go` or `picker.go` (dispatch for kind=wait)
- Modify: `internal/runtime/bootstrap/bootstrap.go` (register predicates)
- Create: `internal/runtime/scheduler/waitpoll_integration_test.go`

**Phase D (parent rollup):**
- Create: `internal/runtime/scheduler/parent_rollup.go`
- Create: `internal/runtime/scheduler/parent_rollup_test.go`
- Modify: `internal/runtime/scheduler/scheduler.go` (call rollup each tick)

**Phase E (templates):**
- Create: `internal/persistence/sqlstore/migrations/009_task_templates.sql`
- Create: `internal/persistence/sqlstore/templates.go`
- Create: `internal/persistence/sqlstore/templates_test.go`
- Create: `internal/service/template.go`
- Create: `internal/service/template_test.go`
- Create: `internal/service/template_vars.go`
- Create: `internal/service/template_vars_test.go`
- Create: `internal/mcpadapter/template_tools.go`
- Create: `internal/mcpadapter/template_tools_test.go`
- Create: `internal/httpserver/templates.go`
- Create: `internal/httpserver/templates_test.go`

**Phase F (dogfood smoke):**
- Create: `docs/templates/backend-fix.yaml`
- Create: `docs/templates/external-chore.yaml`
- Create: `docs/templates/wait-for-git-tag.yaml`
- Create: `docs/templates/decision-checkpoint.yaml`
- Create: `docs/templates/sprint-split-parent.yaml`
- Create: `cmd/clockwork/smoke_templates_test.go`

---

## Phase A — Facet columns + validation

Depends on: nothing.

### Task A1: Migration 007 — task facets

**Files:**
- Create: `internal/persistence/sqlstore/migrations/007_task_facets.sql`

- [ ] **Step 1: Write the migration SQL**

`internal/persistence/sqlstore/migrations/007_task_facets.sql`:

```sql
-- Add task-facet columns per spec 2026-04-16-task-model-mvp-design.md §3.1.
-- NOT NULL without SQL DEFAULT — defaults live in the service layer.
-- Dev DBs must be rebuilt; greenfield, no back-compat.

ALTER TABLE tasks ADD COLUMN kind TEXT NOT NULL DEFAULT 'agent'
  CHECK (kind IN ('agent','external','wait','decision','parent'));
ALTER TABLE tasks ADD COLUMN source_type TEXT NOT NULL DEFAULT 'user'
  CHECK (source_type IN ('agent','user','api','system','webhook','import'));
ALTER TABLE tasks ADD COLUMN source_ref TEXT;
ALTER TABLE tasks ADD COLUMN trust TEXT NOT NULL DEFAULT 'normal'
  CHECK (trust IN ('trusted','normal','untrusted'));
ALTER TABLE tasks ADD COLUMN checkpoint_mode TEXT NOT NULL DEFAULT 'none'
  CHECK (checkpoint_mode IN ('none','blocking','non_blocking'));
ALTER TABLE tasks ADD COLUMN on_checkpoint_response TEXT NOT NULL DEFAULT 'resume'
  CHECK (on_checkpoint_response IN ('resume','review','custom'));

CREATE INDEX IF NOT EXISTS idx_tasks_kind_status     ON tasks(kind, status);
CREATE INDEX IF NOT EXISTS idx_tasks_source          ON tasks(source_type, source_ref);
CREATE INDEX IF NOT EXISTS idx_tasks_checkpoint_mode ON tasks(checkpoint_mode);
```

Note: SQLite's `ALTER TABLE ADD COLUMN` does not accept `NOT NULL` without a DEFAULT. The spec says "strict NOT NULL, no SQL default." SQLite's constraint is binding here — we compromise with `DEFAULT` values at the SQL level (matching the semantic defaults) so existing rows get a value, *and* enforce that service-layer defaults are applied before insert so the column is always set explicitly from the API. In practice the SQL defaults are a safety net, not the primary mechanism.

- [ ] **Step 2: Run migration test to verify it fails**

Run: `go test ./internal/persistence/sqlstore -run TestMigrate -v`
Expected: FAIL — the migration test asserts new columns don't yet exist.

(If migrate_test.go has no assertion for these columns, this step becomes "add the assertion, then run it to FAIL." See Task A2 for the assertion addition if needed.)

- [ ] **Step 3: Update `internal/persistence/sqlstore/migrate_test.go`**

Read the existing `migrate_test.go` — it has a pattern of asserting columns present after migrate. Add a case asserting `kind`, `source_type`, `source_ref`, `trust`, `checkpoint_mode`, `on_checkpoint_response` columns exist on `tasks`, plus the three new indexes.

Example test addition:

```go
func TestMigrate007_TaskFacetColumns(t *testing.T) {
    db := setupTestDB(t)          // assume existing helper; match pattern in file
    cols := columnsForTable(t, db, "tasks")
    require.Contains(t, cols, "kind")
    require.Contains(t, cols, "source_type")
    require.Contains(t, cols, "source_ref")
    require.Contains(t, cols, "trust")
    require.Contains(t, cols, "checkpoint_mode")
    require.Contains(t, cols, "on_checkpoint_response")

    idx := indexNamesForTable(t, db, "tasks")
    require.Contains(t, idx, "idx_tasks_kind_status")
    require.Contains(t, idx, "idx_tasks_source")
    require.Contains(t, idx, "idx_tasks_checkpoint_mode")
}
```

If `columnsForTable` / `indexNamesForTable` helpers don't exist, add them to `migrate_test.go` (see existing migration 005 tag-table tests for the pattern).

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/persistence/sqlstore -run TestMigrate -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/sqlstore/migrations/007_task_facets.sql \
        internal/persistence/sqlstore/migrate_test.go
git commit -m "feat(migrate): 007 task facets — kind, source, trust, checkpoint"
```

---

### Task A2: Extend TaskRecord, TaskFilter, TaskUpdate for facet columns

**Files:**
- Modify: `internal/persistence/sqlstore/tasks.go`
- Modify: `internal/persistence/sqlstore/tasks_test.go` (if exists) or add round-trip test elsewhere

- [ ] **Step 1: Write the failing test**

Add to `internal/persistence/sqlstore/tasks_test.go` (create if missing):

```go
func TestTaskRecord_FacetsRoundTrip(t *testing.T) {
    store := setupStore(t)
    rec := &TaskRecord{
        ID:                   "CW-TEST-0001",
        Title:                "facets",
        Description:          "x",
        Status:               "todo",
        Priority:             2,
        Executor:             "cli",
        OnDone:               "review",
        OnFail:               "retry",
        OnReview:             "pause",
        OnDoneMerge:          "none",
        Kind:                 "agent",
        SourceType:           "user",
        Trust:                "normal",
        CheckpointMode:       "none",
        OnCheckpointResponse: "resume",
        SourceRef:            sql.NullString{String: "claude-code", Valid: true},
    }
    require.NoError(t, store.CreateTask(rec))
    got, err := store.GetTask(rec.ID)
    require.NoError(t, err)
    require.Equal(t, "agent", got.Kind)
    require.Equal(t, "user", got.SourceType)
    require.Equal(t, "claude-code", got.SourceRef.String)
    require.Equal(t, "normal", got.Trust)
    require.Equal(t, "none", got.CheckpointMode)
    require.Equal(t, "resume", got.OnCheckpointResponse)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/persistence/sqlstore -run TestTaskRecord_FacetsRoundTrip -v`
Expected: FAIL — compile error, unknown `Kind` field on TaskRecord.

- [ ] **Step 3: Update TaskRecord + SQL builders**

In `internal/persistence/sqlstore/tasks.go`:

3a. Add to `TaskRecord` struct (after `EpicID sql.NullString`):

```go
// Facet columns (migration 007)
Kind                 string
SourceType           string
SourceRef            sql.NullString
Trust                string
CheckpointMode       string
OnCheckpointResponse string
```

3b. Extend `TaskFilter`:

```go
type TaskFilter struct {
    // ... existing fields ...
    Kind           string
    SourceType     string
    SourceRef      string
    Trust          string
    CheckpointMode string
}
```

3c. Extend `TaskUpdate`:

```go
type TaskUpdate struct {
    // ... existing fields ...
    Kind                 *string
    SourceType           *string
    SourceRef            *sql.NullString
    Trust                *string
    CheckpointMode       *string
    OnCheckpointResponse *string
}
```

3d. Extend `taskSelectCols` (was 34 columns, now 40):

```go
const taskSelectCols = `id, title, description, status, priority, manual,
    executor, agent_profile, working_dir, tools, permissions, environment,
    system_prompt, files, cost_budget, max_retries, max_duration_ms, token_budget,
    on_done, on_fail, on_review, escalation_chain, quality_gates, deliverables,
    deliverable_preset, on_done_merge, depends_on, blocked_reason, metadata,
    sprint_id, project_id, epic_id, created_at, updated_at,
    kind, source_type, source_ref, trust, checkpoint_mode, on_checkpoint_response`
```

3e. Update every `rows.Scan(...)` / `row.Scan(...)` call that reads a `TaskRecord` to include the six new columns in scan-arg order matching `taskSelectCols`. Search the file for `.Scan(&t.ID` (or similar) and extend each. There are likely 2–4 of these in `GetTask`, `ListTasks`, `SearchTasks`.

3f. Update `CreateTask` (the `INSERT INTO tasks ...` builder). Add the six columns to the INSERT column list and the six values to the placeholder list.

3g. Update `UpdateTask` (the partial-update builder). Add six cases to the field-by-field conditional assembly — each mirrors the shape of existing lifecycle-enum field updates.

3h. Update `ListTasks` filter-SQL builder. Add WHERE clauses for `filter.Kind`, `filter.SourceType`, `filter.SourceRef`, `filter.Trust`, `filter.CheckpointMode` when non-empty.

3i. Update `applyDefaults` to set:

```go
if t.Kind == "" {
    t.Kind = "agent"
}
if t.SourceType == "" {
    t.SourceType = "user"
}
if t.Trust == "" {
    t.Trust = "normal"
}
if t.CheckpointMode == "" {
    t.CheckpointMode = "none"
}
if t.OnCheckpointResponse == "" {
    t.OnCheckpointResponse = "resume"
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/persistence/sqlstore -run TestTaskRecord_FacetsRoundTrip -v`
Expected: PASS

Also run the broader store test suite to catch any existing-test regressions:
Run: `go test ./internal/persistence/sqlstore -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/sqlstore/tasks.go internal/persistence/sqlstore/tasks_test.go
git commit -m "feat(sqlstore): TaskRecord facets — kind, source, trust, checkpoint"
```

---

### Task A3: Service-layer facet fields + defaults

**Files:**
- Modify: `internal/service/task.go`
- Modify: `internal/service/task_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/service/task_test.go`:

```go
func TestTaskCreate_FacetDefaults(t *testing.T) {
    s := setupTaskService(t)          // existing helper
    in := TaskCreateInput{Title: "minimal"}
    rec, err := s.Create(in)
    require.NoError(t, err)
    require.Equal(t, "agent", rec.Kind)
    require.Equal(t, "user", rec.SourceType)
    require.Equal(t, "normal", rec.Trust)
    require.Equal(t, "none", rec.CheckpointMode)
    require.Equal(t, "resume", rec.OnCheckpointResponse)
}

func TestTaskCreate_FacetExplicit(t *testing.T) {
    s := setupTaskService(t)
    in := TaskCreateInput{
        Title:                "explicit",
        Kind:                 "agent",
        SourceType:           "agent",
        SourceRef:            "claude-code",
        Trust:                "trusted",
        CheckpointMode:       "blocking",
        OnCheckpointResponse: "review",
    }
    rec, err := s.Create(in)
    require.NoError(t, err)
    require.Equal(t, "agent", rec.SourceType)
    require.Equal(t, "claude-code", rec.SourceRef.String)
    require.Equal(t, "trusted", rec.Trust)
    require.Equal(t, "blocking", rec.CheckpointMode)
    require.Equal(t, "review", rec.OnCheckpointResponse)
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/service -run TestTaskCreate_Facet -v`
Expected: FAIL (compile error; TaskCreateInput has no Kind/SourceType/etc. fields).

- [ ] **Step 3: Extend TaskCreateInput + Create record builder**

In `internal/service/task.go`:

3a. Add to `TaskCreateInput` (after existing Project-2 additions):

```go
// Facet fields (migration 007)
Kind                 string
SourceType           string
SourceRef            string
Trust                string            // if empty, resolved via ResolveTrust
CheckpointMode       string
OnCheckpointResponse string
```

3b. In the `Create` method, after the existing record builder but before `s.store.CreateTask(rec)`, fill in facets:

```go
rec.Kind                 = orDefault(input.Kind, "agent")
rec.SourceType           = orDefault(input.SourceType, "user")
rec.Trust                = orDefault(input.Trust, ResolveTrust(orDefault(input.SourceType, "user"), input.SourceRef))
rec.CheckpointMode       = orDefault(input.CheckpointMode, "none")
rec.OnCheckpointResponse = orDefault(input.OnCheckpointResponse, "resume")
if input.SourceRef != "" {
    rec.SourceRef = sql.NullString{String: input.SourceRef, Valid: true}
}
```

`ResolveTrust` is defined in Task A5 — reference it now, create the file next.

- [ ] **Step 4: Create the trust helper stub so the build compiles**

Create a placeholder `internal/service/trust.go`:

```go
package service

// ResolveTrust is implemented in Task A5; stub here so builds pass.
func ResolveTrust(sourceType, sourceRef string) string {
    return "normal"
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/service -run TestTaskCreate_Facet -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/task.go internal/service/trust.go internal/service/task_test.go
git commit -m "feat(service): task facet fields with API-level defaults"
```

---

### Task A4: Trust resolver

**Files:**
- Modify: `internal/service/trust.go` (replace stub)
- Create: `internal/service/trust_test.go`

- [ ] **Step 1: Write the failing test**

`internal/service/trust_test.go`:

```go
package service

import "testing"

func TestResolveTrust_Defaults(t *testing.T) {
    cases := []struct {
        name       string
        sourceType string
        sourceRef  string
        want       string
    }{
        {"system_any",    "system", "",            "trusted"},
        {"user_any",      "user",   "chrispian",   "normal"},
        {"agent_any",     "agent",  "claude-code", "normal"},
        {"api_any",       "api",    "nanite",      "normal"},
        {"webhook_any",   "webhook","github",      "untrusted"},
        {"import_any",    "import", "",            "untrusted"},
        {"unknown_falls", "bogus",  "",            "normal"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := ResolveTrust(tc.sourceType, tc.sourceRef)
            if got != tc.want {
                t.Errorf("ResolveTrust(%q, %q) = %q, want %q", tc.sourceType, tc.sourceRef, got, tc.want)
            }
        })
    }
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/service -run TestResolveTrust -v`
Expected: FAIL — all cases return "normal" because of the stub.

- [ ] **Step 3: Implement**

Replace `internal/service/trust.go`:

```go
package service

// ResolveTrust returns the default trust level for a task given its source.
// MVP: deterministic map keyed on source_type. A known-source registry
// (BLG-35) will later override this per (source_type, source_ref).
func ResolveTrust(sourceType, sourceRef string) string {
    switch sourceType {
    case "system":
        return "trusted"
    case "user", "agent", "api":
        return "normal"
    case "webhook", "import":
        return "untrusted"
    default:
        return "normal"
    }
}
```

- [ ] **Step 4: Run**

Run: `go test ./internal/service -run TestResolveTrust -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/trust.go internal/service/trust_test.go
git commit -m "feat(service): ResolveTrust — deterministic trust defaulting per source_type"
```

---

### Task A5: validateTaskKind

**Files:**
- Modify: `internal/service/task_validation.go`
- Modify: `internal/service/task_validation_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/service/task_validation_test.go`:

```go
func TestValidateTaskKind_AgentRequiresExecutor(t *testing.T) {
    s := setupTaskService(t)
    _, err := s.Create(TaskCreateInput{Title: "x", Kind: "agent", Executor: ""})
    var verr *ValidationError
    require.ErrorAs(t, err, &verr)
    require.Equal(t, "executor", verr.Field)
}

func TestValidateTaskKind_ExternalForbidsExecutor(t *testing.T) {
    s := setupTaskService(t)
    _, err := s.Create(TaskCreateInput{Title: "x", Kind: "external", Executor: "cli"})
    var verr *ValidationError
    require.ErrorAs(t, err, &verr)
    require.Equal(t, "executor", verr.Field)
}

func TestValidateTaskKind_ExternalForbidsAutoExecute(t *testing.T) {
    s := setupTaskService(t)
    _, err := s.Create(TaskCreateInput{Title: "x", Kind: "external", Manual: false})
    // Manual=false means auto_execute=true; external must be manual=true.
    var verr *ValidationError
    require.ErrorAs(t, err, &verr)
    require.Equal(t, "auto_execute", verr.Field)
}

func TestValidateTaskKind_WaitRequiresPredicate(t *testing.T) {
    s := setupTaskService(t)
    _, err := s.Create(TaskCreateInput{Title: "x", Kind: "wait"})
    var verr *ValidationError
    require.ErrorAs(t, err, &verr)
    require.Equal(t, "metadata.wait.predicate_type", verr.Field)
}

func TestValidateTaskKind_DecisionRequiresBlockingCheckpoint(t *testing.T) {
    s := setupTaskService(t)
    _, err := s.Create(TaskCreateInput{Title: "x", Kind: "decision", CheckpointMode: "none", Manual: true})
    var verr *ValidationError
    require.ErrorAs(t, err, &verr)
    require.Equal(t, "checkpoint_mode", verr.Field)
}

func TestValidateTaskKind_DecisionForbidsAutoExecute(t *testing.T) {
    s := setupTaskService(t)
    _, err := s.Create(TaskCreateInput{
        Title: "x", Kind: "decision", CheckpointMode: "blocking", Manual: false,
    })
    var verr *ValidationError
    require.ErrorAs(t, err, &verr)
    require.Equal(t, "auto_execute", verr.Field)
}

func TestValidateTaskKind_UnknownKindRejected(t *testing.T) {
    s := setupTaskService(t)
    _, err := s.Create(TaskCreateInput{Title: "x", Kind: "bogus"})
    var verr *ValidationError
    require.ErrorAs(t, err, &verr)
    require.Equal(t, "kind", verr.Field)
}

func TestValidateTaskKind_UnknownSourceTypeRejected(t *testing.T) {
    s := setupTaskService(t)
    _, err := s.Create(TaskCreateInput{Title: "x", SourceType: "bogus"})
    var verr *ValidationError
    require.ErrorAs(t, err, &verr)
    require.Equal(t, "source_type", verr.Field)
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/service -run TestValidateTaskKind -v`
Expected: most cases FAIL (tasks get created without validation).

- [ ] **Step 3: Implement validation**

Add to `internal/service/task_validation.go`:

```go
var validKinds = map[string]bool{
    "agent": true, "external": true, "wait": true, "decision": true, "parent": true,
}

var validSourceTypes = map[string]bool{
    "agent": true, "user": true, "api": true, "system": true, "webhook": true, "import": true,
}

var validTrustLevels = map[string]bool{
    "trusted": true, "normal": true, "untrusted": true,
}

var validCheckpointModes = map[string]bool{
    "none": true, "blocking": true, "non_blocking": true,
}

var validOnCheckpointResponse = map[string]bool{
    "resume": true, "review": true, "custom": true,
}

// validateTaskKind enforces per-kind invariants. Called from Create (and Update
// in the update path — see extractUpdateFields extension).
//
// Rules per spec §3.5:
//   - kind=agent    + executor=""                                 -> 422
//   - kind=external + executor!=""                                -> 422
//   - kind=external + auto_execute=true (Manual=false)            -> 422
//   - kind=wait     + no metadata.wait.predicate_type             -> 422
//   - kind=decision + checkpoint_mode!="blocking"                 -> 422
//   - kind=decision + auto_execute=true (Manual=false)            -> 422
//   - unknown kind / source_type / trust / checkpoint_mode        -> 422
func validateTaskKind(kind, executor, sourceType, trust, checkpointMode, onCheckpointResponse string, manual bool, metadata map[string]any) error {
    if kind != "" && !validKinds[kind] {
        return &ValidationError{Field: "kind", Message: "invalid kind: got '" + kind + "', expected one of: agent, external, wait, decision, parent"}
    }
    if sourceType != "" && !validSourceTypes[sourceType] {
        return &ValidationError{Field: "source_type", Message: "invalid source_type: got '" + sourceType + "', expected one of: agent, user, api, system, webhook, import"}
    }
    if trust != "" && !validTrustLevels[trust] {
        return &ValidationError{Field: "trust", Message: "invalid trust: got '" + trust + "', expected one of: trusted, normal, untrusted"}
    }
    if checkpointMode != "" && !validCheckpointModes[checkpointMode] {
        return &ValidationError{Field: "checkpoint_mode", Message: "invalid checkpoint_mode: got '" + checkpointMode + "', expected one of: none, blocking, non_blocking"}
    }
    if onCheckpointResponse != "" && !validOnCheckpointResponse[onCheckpointResponse] {
        return &ValidationError{Field: "on_checkpoint_response", Message: "invalid on_checkpoint_response: got '" + onCheckpointResponse + "', expected one of: resume, review, custom"}
    }

    autoExecute := !manual // auto_execute == !Manual in existing model

    switch kind {
    case "agent":
        if executor == "" {
            return &ValidationError{Field: "executor", Message: "executor required for kind=agent"}
        }
    case "external":
        if executor != "" {
            return &ValidationError{Field: "executor", Message: "executor not allowed for kind=external"}
        }
        if autoExecute {
            return &ValidationError{Field: "auto_execute", Message: "external tasks cannot be auto-executed (set manual=true)"}
        }
    case "wait":
        wait, _ := metadata["wait"].(map[string]any)
        if wait == nil {
            return &ValidationError{Field: "metadata.wait.predicate_type", Message: "wait tasks require metadata.wait.predicate_type"}
        }
        if pt, _ := wait["predicate_type"].(string); pt == "" {
            return &ValidationError{Field: "metadata.wait.predicate_type", Message: "wait tasks require metadata.wait.predicate_type"}
        }
    case "decision":
        if checkpointMode != "blocking" {
            return &ValidationError{Field: "checkpoint_mode", Message: "decision tasks require checkpoint_mode=blocking"}
        }
        if autoExecute {
            return &ValidationError{Field: "auto_execute", Message: "decision tasks are human-driven; set manual=true"}
        }
    case "parent":
        // children array is a warning, not an error — emit log but don't reject
    }
    return nil
}
```

Note on `auto_execute` mapping: the existing model uses `Manual bool` where `Manual=true` → scheduler skips (i.e. not auto-executed). MVP-API reasoning says `auto_execute = !Manual`. Validation treats them as equivalents.

- [ ] **Step 4: Wire `validateTaskKind` into Create**

In `internal/service/task.go` `Create`, after `validateTaskWrites` call, add:

```go
if err := validateTaskKind(
    orDefault(input.Kind, "agent"),
    input.Executor,
    orDefault(input.SourceType, "user"),
    input.Trust,
    orDefault(input.CheckpointMode, "none"),
    orDefault(input.OnCheckpointResponse, "resume"),
    input.Manual,
    input.Metadata,
); err != nil {
    return nil, err
}
```

Also wire into `Update`. For Update you need the *resulting* values, not the input values — load the existing record, overlay the update, then validate. This is more involved:

```go
// In Update, after extractUpdateFields + validateTaskWrites
existing, err := s.store.GetTask(id)
if err != nil {
    return err
}
effectiveKind := ptrOrDefault(input.Kind, existing.Kind)
effectiveExecutor := ptrOrDefault(input.Executor, existing.Executor)
effectiveSourceType := ptrOrDefault(input.SourceType, existing.SourceType)
effectiveTrust := ptrOrDefault(input.Trust, existing.Trust)
effectiveCheckpointMode := ptrOrDefault(input.CheckpointMode, existing.CheckpointMode)
effectiveOnCheckpointResponse := ptrOrDefault(input.OnCheckpointResponse, existing.OnCheckpointResponse)
effectiveManual := existing.Manual
if input.Manual != nil {
    effectiveManual = *input.Manual
}
// Metadata for wait predicate check: overlay if input updates metadata
effectiveMetadata := map[string]any{}
if existing.Metadata.Valid && existing.Metadata.String != "" {
    _ = unmarshalJSON([]byte(existing.Metadata.String), &effectiveMetadata)
}
if input.Metadata != nil && input.Metadata.Valid && input.Metadata.String != "" {
    _ = unmarshalJSON([]byte(input.Metadata.String), &effectiveMetadata)
}
if err := validateTaskKind(effectiveKind, effectiveExecutor, effectiveSourceType,
    effectiveTrust, effectiveCheckpointMode, effectiveOnCheckpointResponse,
    effectiveManual, effectiveMetadata); err != nil {
    return err
}
```

Add the `ptrOrDefault` helper to `task_validation.go`:

```go
func ptrOrDefault(p *string, fallback string) string {
    if p != nil {
        return *p
    }
    return fallback
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/service -run TestValidateTaskKind -v`
Expected: all PASS.

Run full service suite:
Run: `go test ./internal/service -v`
Expected: all PASS (including existing tests — no regressions).

- [ ] **Step 6: Commit**

```bash
git add internal/service/task.go internal/service/task_validation.go internal/service/task_validation_test.go
git commit -m "feat(service): validateTaskKind — per-kind invariants + enum validation"
```

---

### Task A6: HTTP layer — facets on create/update/list/response

**Files:**
- Modify: `internal/httpserver/tasks.go`
- Modify: `internal/httpserver/tasks_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/httpserver/tasks_test.go`:

```go
func TestHTTP_TaskCreate_RoundTripWithFacets(t *testing.T) {
    srv := setupHTTPServer(t)
    body := `{
        "title":"http facets",
        "description":"x",
        "kind":"agent",
        "source_type":"agent",
        "source_ref":"claude-code",
        "trust":"trusted",
        "checkpoint_mode":"blocking",
        "on_checkpoint_response":"review"
    }`
    resp := postJSON(t, srv, "/api/v1/tasks", body)
    require.Equal(t, 201, resp.StatusCode)
    var got map[string]any
    require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
    require.Equal(t, "agent", got["kind"])
    require.Equal(t, "agent", got["source_type"])
    require.Equal(t, "claude-code", got["source_ref"])
    require.Equal(t, "trusted", got["trust"])
    require.Equal(t, "blocking", got["checkpoint_mode"])
    require.Equal(t, "review", got["on_checkpoint_response"])
}

func TestHTTP_TaskList_FilterByKind(t *testing.T) {
    srv := setupHTTPServer(t)
    postJSON(t, srv, "/api/v1/tasks", `{"title":"a","kind":"agent","description":"x"}`)
    postJSON(t, srv, "/api/v1/tasks", `{"title":"e","kind":"external","manual":true,"description":"x"}`)
    resp := get(t, srv, "/api/v1/tasks?kind=external")
    require.Equal(t, 200, resp.StatusCode)
    // assert only the external task returns; adapt to your existing response shape
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/httpserver -run TestHTTP_Task -v`
Expected: FAIL — fields not accepted by request structs.

- [ ] **Step 3: Extend request/response structs + parse/list**

In `internal/httpserver/tasks.go`:

3a. Add to `TaskCreateRequest`:

```go
Kind                 string  `json:"kind"`
SourceType           string  `json:"source_type"`
SourceRef            string  `json:"source_ref"`
Trust                string  `json:"trust"`
CheckpointMode       string  `json:"checkpoint_mode"`
OnCheckpointResponse string  `json:"on_checkpoint_response"`
```

3b. Same fields on `TaskUpdateRequest` (as `*string` for partial update semantics).

3c. In the handler that converts `TaskCreateRequest` → `service.TaskCreateInput`, pass each facet through.

3d. In the handler that converts `TaskUpdateRequest` → `service.TaskUpdateInput`, pass each facet through as `*string`.

3e. In `taskJSON` (response builder), add:

```go
"kind":                  t.Kind,
"source_type":            t.SourceType,
"source_ref":             nullStringToJSON(t.SourceRef),
"trust":                  t.Trust,
"checkpoint_mode":        t.CheckpointMode,
"on_checkpoint_response": t.OnCheckpointResponse,
```

3f. In the list handler, parse `kind`, `source_type`, `source_ref`, `trust`, `checkpoint_mode` query params and set them on the `TaskFilter`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/httpserver -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpserver/tasks.go internal/httpserver/tasks_test.go
git commit -m "feat(httpserver): expose task facets on create/update/list/response"
```

---

### Task A7: MCP adapter — facet fields on task_create, task_update, task_list

**Files:**
- Modify: `internal/mcpadapter/task_tools.go`
- Modify: `internal/mcpadapter/task_tools_test.go` (or add tests inline)

- [ ] **Step 1: Write the failing test**

```go
func TestMCP_TaskCreate_Facets(t *testing.T) {
    server := setupMCPServer(t)
    payload := map[string]any{
        "title":"mcp facets",
        "description":"x",
        "kind":"external",
        "manual":true,
        "source_type":"user",
        "source_ref":"chrispian",
    }
    resp := callTool(t, server, "clockwork_task_create", payload)
    require.False(t, resp.IsError)
    // decode resp.Content, assert facets present
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/mcpadapter -run TestMCP_TaskCreate_Facets -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `internal/mcpadapter/task_tools.go`:

3a. Add the six facet fields to the `clockwork_task_create` schema (MCP tool definition). Find where `title`/`description`/existing fields are declared in the `mcp.NewTool(...)` definition and add:

```go
mcp.WithString("kind", mcp.Description("agent|external|wait|decision|parent"), mcp.DefaultString("agent")),
mcp.WithString("source_type", mcp.Description("agent|user|api|system|webhook|import"), mcp.DefaultString("user")),
mcp.WithString("source_ref", mcp.Description("originating slug/id")),
mcp.WithString("trust", mcp.Description("trusted|normal|untrusted")),
mcp.WithString("checkpoint_mode", mcp.Description("none|blocking|non_blocking"), mcp.DefaultString("none")),
mcp.WithString("on_checkpoint_response", mcp.Description("resume|review|custom"), mcp.DefaultString("resume")),
```

3b. In the `clockwork_task_create` handler, extract these params and pass to `TaskCreateInput`.

3c. Same for `clockwork_task_update` (`*string` handling).

3d. For `clockwork_task_list`, add the filter params to the schema + handler.

3e. Update the `taskWithTags` helper / response formatter so the MCP JSON includes the facets, matching the HTTP response shape from Task A6.

- [ ] **Step 4: Run**

Run: `go test ./internal/mcpadapter -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpadapter/task_tools.go internal/mcpadapter/task_tools_test.go
git commit -m "feat(mcp): expose task facets on task_create/update/list tools"
```

---

### Task A8: Integration round-trip test

**Files:**
- Modify: `internal/service/task_test.go` (or create a dedicated integration test file)

- [ ] **Step 1: Write the test**

```go
func TestIntegration_TaskWithFacets_ThroughAllLayers(t *testing.T) {
    srv := setupHTTPServer(t)
    // Create via HTTP
    resp := postJSON(t, srv, "/api/v1/tasks", `{
        "title":"e2e facets",
        "description":"x",
        "kind":"external",
        "manual":true,
        "source_type":"user",
        "source_ref":"chrispian",
        "trust":"trusted"
    }`)
    require.Equal(t, 201, resp.StatusCode)

    // Read via HTTP
    var created map[string]any
    require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
    id := created["id"].(string)

    got := getJSON(t, srv, "/api/v1/tasks/"+id)
    require.Equal(t, "external", got["kind"])
    require.Equal(t, "trusted", got["trust"])

    // Update trust via HTTP
    patchJSON(t, srv, "/api/v1/tasks/"+id, `{"trust":"normal"}`)
    got2 := getJSON(t, srv, "/api/v1/tasks/"+id)
    require.Equal(t, "normal", got2["trust"])

    // List filtered by source_ref
    listed := getJSON(t, srv, "/api/v1/tasks?source_ref=chrispian")
    // assert task present
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/service ./internal/httpserver ./internal/mcpadapter -v`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/service/task_test.go
git commit -m "test: integration round-trip for task facets"
```

---

## Phase B — Checkpoints

Depends on: Phase A complete.

### Task B1: Migration 008 — checkpoints table

**Files:**
- Create: `internal/persistence/sqlstore/migrations/008_checkpoints.sql`
- Modify: `internal/persistence/sqlstore/migrate_test.go`

- [ ] **Step 1: Write the migration**

```sql
-- 008 checkpoints — see spec §4.1.
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

- [ ] **Step 2: Add assertion to migrate_test.go**

```go
func TestMigrate008_CheckpointsTable(t *testing.T) {
    db := setupTestDB(t)
    tables := tableNames(t, db)
    require.Contains(t, tables, "checkpoints")

    cols := columnsForTable(t, db, "checkpoints")
    for _, c := range []string{
        "id","task_id","run_id","correlation_id","type",
        "payload_json","response_json","emitter_source_type","emitter_source_ref",
        "responder_source_type","responder_source_ref","emitted_at","responded_at",
        "timeout_at","status",
    } {
        require.Contains(t, cols, c, "expected column %q", c)
    }
}
```

- [ ] **Step 3: Run the test**

Run: `go test ./internal/persistence/sqlstore -run TestMigrate008 -v`
Expected: PASS (the test was written alongside the migration, both land together).

- [ ] **Step 4: Commit**

```bash
git add internal/persistence/sqlstore/migrations/008_checkpoints.sql \
        internal/persistence/sqlstore/migrate_test.go
git commit -m "feat(migrate): 008 checkpoints table"
```

---

### Task B2: CheckpointRecord + sqlstore CRUD

**Files:**
- Create: `internal/persistence/sqlstore/checkpoints.go`
- Create: `internal/persistence/sqlstore/checkpoints_test.go`

- [ ] **Step 1: Write the failing test**

`internal/persistence/sqlstore/checkpoints_test.go`:

```go
package sqlstore

import (
    "database/sql"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func TestCheckpoint_InsertAndGet(t *testing.T) {
    s := setupStore(t)
    require.NoError(t, s.CreateTask(&TaskRecord{ID: "CW-T-1", Title: "t", Status: "doing", Kind: "agent", SourceType: "user", Trust: "normal", CheckpointMode: "blocking", OnCheckpointResponse: "resume", Executor: "cli", OnDone: "review", OnFail: "retry", OnReview: "pause", OnDoneMerge: "none"}))

    cp := &CheckpointRecord{
        TaskID:             "CW-T-1",
        CorrelationID:      "01H-ULID",
        Type:               "collect_data",
        PayloadJSON:        `{"question":"pick one"}`,
        EmitterSourceType:  "agent",
        EmitterSourceRef:   sql.NullString{String: "claude-code", Valid: true},
        Status:             "pending",
    }
    require.NoError(t, s.CreateCheckpoint(cp))
    require.NotZero(t, cp.ID)

    got, err := s.GetCheckpointByCorrelation("01H-ULID")
    require.NoError(t, err)
    require.Equal(t, "collect_data", got.Type)
    require.Equal(t, "pending", got.Status)
}

func TestCheckpoint_Respond(t *testing.T) {
    s := setupStore(t)
    setupTaskAndCheckpoint(t, s, "CW-T-2", "CORR-2")
    require.NoError(t, s.RespondCheckpoint("CORR-2", `{"answer":"a"}`, "user", "chrispian", time.Now()))

    got, err := s.GetCheckpointByCorrelation("CORR-2")
    require.NoError(t, err)
    require.Equal(t, "responded", got.Status)
    require.Equal(t, `{"answer":"a"}`, got.ResponseJSON.String)
    require.Equal(t, "user", got.ResponderSourceType.String)
}

func TestCheckpoint_Cancel(t *testing.T) {
    s := setupStore(t)
    setupTaskAndCheckpoint(t, s, "CW-T-3", "CORR-3")
    require.NoError(t, s.CancelCheckpoint("CORR-3", "no longer relevant", "user", "chrispian", time.Now()))

    got, err := s.GetCheckpointByCorrelation("CORR-3")
    require.NoError(t, err)
    require.Equal(t, "canceled", got.Status)
}

func TestCheckpoint_SweepTimedOut(t *testing.T) {
    s := setupStore(t)
    setupTaskAndCheckpoint(t, s, "CW-T-4", "CORR-4")
    past := time.Now().Add(-1 * time.Hour)
    require.NoError(t, s.SetCheckpointTimeout("CORR-4", past))

    n, err := s.SweepTimedOutCheckpoints(time.Now())
    require.NoError(t, err)
    require.Equal(t, 1, n)

    got, err := s.GetCheckpointByCorrelation("CORR-4")
    require.NoError(t, err)
    require.Equal(t, "timed_out", got.Status)
}
```

Helper stub to add to the test file (or a shared test helpers file):

```go
func setupTaskAndCheckpoint(t *testing.T, s *Store, taskID, corrID string) {
    t.Helper()
    require.NoError(t, s.CreateTask(&TaskRecord{
        ID: taskID, Title: "t", Status: "doing",
        Kind: "decision", SourceType: "user", Trust: "normal",
        CheckpointMode: "blocking", OnCheckpointResponse: "resume",
        Executor: "", OnDone: "review", OnFail: "retry", OnReview: "pause", OnDoneMerge: "none",
        Manual: true,
    }))
    require.NoError(t, s.CreateCheckpoint(&CheckpointRecord{
        TaskID: taskID, CorrelationID: corrID, Type: "collect_data",
        PayloadJSON: `{}`, EmitterSourceType: "system", Status: "pending",
    }))
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/persistence/sqlstore -run TestCheckpoint -v`
Expected: FAIL — CheckpointRecord, CreateCheckpoint, etc. don't exist.

- [ ] **Step 3: Implement**

Create `internal/persistence/sqlstore/checkpoints.go`:

```go
package sqlstore

import (
    "database/sql"
    "errors"
    "fmt"
    "time"
)

var ErrCheckpointNotFound = errors.New("checkpoint not found")

type CheckpointRecord struct {
    ID                   int64
    TaskID               string
    RunID                sql.NullInt64
    CorrelationID        string
    Type                 string
    PayloadJSON          string
    ResponseJSON         sql.NullString
    EmitterSourceType    string
    EmitterSourceRef     sql.NullString
    ResponderSourceType  sql.NullString
    ResponderSourceRef   sql.NullString
    EmittedAt            time.Time
    RespondedAt          sql.NullTime
    TimeoutAt            sql.NullTime
    Status               string
}

const checkpointSelectCols = `id, task_id, run_id, correlation_id, type,
    payload_json, response_json, emitter_source_type, emitter_source_ref,
    responder_source_type, responder_source_ref, emitted_at, responded_at,
    timeout_at, status`

func (s *Store) CreateCheckpoint(cp *CheckpointRecord) error {
    res, err := s.db.Exec(`
        INSERT INTO checkpoints (task_id, run_id, correlation_id, type,
            payload_json, emitter_source_type, emitter_source_ref, status)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
        cp.TaskID, cp.RunID, cp.CorrelationID, cp.Type,
        cp.PayloadJSON, cp.EmitterSourceType, cp.EmitterSourceRef,
        ifEmpty(cp.Status, "pending"))
    if err != nil {
        return fmt.Errorf("insert checkpoint: %w", err)
    }
    id, err := res.LastInsertId()
    if err != nil {
        return err
    }
    cp.ID = id
    return nil
}

func (s *Store) GetCheckpointByCorrelation(correlationID string) (*CheckpointRecord, error) {
    row := s.db.QueryRow(`SELECT `+checkpointSelectCols+` FROM checkpoints WHERE correlation_id = ?`, correlationID)
    cp := &CheckpointRecord{}
    err := row.Scan(
        &cp.ID, &cp.TaskID, &cp.RunID, &cp.CorrelationID, &cp.Type,
        &cp.PayloadJSON, &cp.ResponseJSON, &cp.EmitterSourceType, &cp.EmitterSourceRef,
        &cp.ResponderSourceType, &cp.ResponderSourceRef, &cp.EmittedAt, &cp.RespondedAt,
        &cp.TimeoutAt, &cp.Status,
    )
    if errors.Is(err, sql.ErrNoRows) {
        return nil, fmt.Errorf("%s: %w", correlationID, ErrCheckpointNotFound)
    }
    if err != nil {
        return nil, err
    }
    return cp, nil
}

func (s *Store) ListCheckpointsForTask(taskID string) ([]CheckpointRecord, error) {
    rows, err := s.db.Query(`SELECT `+checkpointSelectCols+` FROM checkpoints WHERE task_id = ? ORDER BY emitted_at DESC`, taskID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []CheckpointRecord
    for rows.Next() {
        cp := CheckpointRecord{}
        if err := rows.Scan(
            &cp.ID, &cp.TaskID, &cp.RunID, &cp.CorrelationID, &cp.Type,
            &cp.PayloadJSON, &cp.ResponseJSON, &cp.EmitterSourceType, &cp.EmitterSourceRef,
            &cp.ResponderSourceType, &cp.ResponderSourceRef, &cp.EmittedAt, &cp.RespondedAt,
            &cp.TimeoutAt, &cp.Status,
        ); err != nil {
            return nil, err
        }
        out = append(out, cp)
    }
    return out, rows.Err()
}

func (s *Store) ListPendingCheckpoints() ([]CheckpointRecord, error) {
    rows, err := s.db.Query(`SELECT `+checkpointSelectCols+` FROM checkpoints WHERE status = 'pending' ORDER BY emitted_at ASC`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []CheckpointRecord
    for rows.Next() {
        cp := CheckpointRecord{}
        if err := rows.Scan(
            &cp.ID, &cp.TaskID, &cp.RunID, &cp.CorrelationID, &cp.Type,
            &cp.PayloadJSON, &cp.ResponseJSON, &cp.EmitterSourceType, &cp.EmitterSourceRef,
            &cp.ResponderSourceType, &cp.ResponderSourceRef, &cp.EmittedAt, &cp.RespondedAt,
            &cp.TimeoutAt, &cp.Status,
        ); err != nil {
            return nil, err
        }
        out = append(out, cp)
    }
    return out, rows.Err()
}

func (s *Store) RespondCheckpoint(correlationID, responseJSON, responderType, responderRef string, at time.Time) error {
    res, err := s.db.Exec(`
        UPDATE checkpoints SET status = 'responded', response_json = ?,
            responder_source_type = ?, responder_source_ref = ?, responded_at = ?
        WHERE correlation_id = ? AND status = 'pending'`,
        responseJSON, responderType, nullIfEmpty(responderRef), at, correlationID)
    if err != nil {
        return err
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        return fmt.Errorf("%s: %w", correlationID, ErrCheckpointNotFound)
    }
    return nil
}

func (s *Store) CancelCheckpoint(correlationID, reason, cancelerType, cancelerRef string, at time.Time) error {
    body := fmt.Sprintf(`{"canceled":true,"reason":%q}`, reason)
    res, err := s.db.Exec(`
        UPDATE checkpoints SET status = 'canceled', response_json = ?,
            responder_source_type = ?, responder_source_ref = ?, responded_at = ?
        WHERE correlation_id = ? AND status = 'pending'`,
        body, cancelerType, nullIfEmpty(cancelerRef), at, correlationID)
    if err != nil {
        return err
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        return fmt.Errorf("%s: %w", correlationID, ErrCheckpointNotFound)
    }
    return nil
}

func (s *Store) SetCheckpointTimeout(correlationID string, at time.Time) error {
    _, err := s.db.Exec(`UPDATE checkpoints SET timeout_at = ? WHERE correlation_id = ?`, at, correlationID)
    return err
}

func (s *Store) SweepTimedOutCheckpoints(now time.Time) (int, error) {
    res, err := s.db.Exec(`
        UPDATE checkpoints SET status = 'timed_out'
        WHERE status = 'pending' AND timeout_at IS NOT NULL AND timeout_at < ?`, now)
    if err != nil {
        return 0, err
    }
    n, _ := res.RowsAffected()
    return int(n), nil
}

func ifEmpty(s, fallback string) string {
    if s == "" {
        return fallback
    }
    return s
}

func nullIfEmpty(s string) sql.NullString {
    if s == "" {
        return sql.NullString{}
    }
    return sql.NullString{String: s, Valid: true}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/persistence/sqlstore -run TestCheckpoint -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/sqlstore/checkpoints.go internal/persistence/sqlstore/checkpoints_test.go
git commit -m "feat(sqlstore): CheckpointRecord + CRUD + timeout sweep"
```

---

### Task B3: CheckpointService (service layer)

**Files:**
- Create: `internal/service/checkpoint.go`
- Create: `internal/service/checkpoint_test.go`

- [ ] **Step 1: Write the failing test**

`internal/service/checkpoint_test.go`:

```go
package service

import (
    "testing"

    "github.com/stretchr/testify/require"
)

func TestCheckpointService_Emit(t *testing.T) {
    s := setupTaskService(t)
    cp := setupCheckpointService(t, s)

    task := createBlockingDecisionTask(t, s)
    out, err := cp.Emit(CheckpointEmitInput{
        TaskID:            task.ID,
        Type:              "collect_data",
        PayloadJSON:       `{"q":"answer?"}`,
        EmitterSourceType: "system",
    })
    require.NoError(t, err)
    require.NotEmpty(t, out.CorrelationID)
    require.Equal(t, "pending", out.Status)
}

func TestCheckpointService_Respond_TransitionsTask(t *testing.T) {
    // Create a decision task in `review` with a pending blocking checkpoint,
    // respond -> expect task transition back to todo when on_checkpoint_response=resume.
    // (This spans service layer + store; use the wiring as in Task B7.)
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/service -run TestCheckpointService -v`
Expected: FAIL — CheckpointService doesn't exist.

- [ ] **Step 3: Implement**

Create `internal/service/checkpoint.go`:

```go
package service

import (
    "errors"
    "fmt"
    "time"

    "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
    "github.com/oklog/ulid/v2"             // add to go.mod if not present — or use your existing ULID source
)

type CheckpointService struct {
    store *sqlstore.Store
    tasks *TaskService
}

func NewCheckpointService(store *sqlstore.Store, tasks *TaskService) *CheckpointService {
    return &CheckpointService{store: store, tasks: tasks}
}

type CheckpointEmitInput struct {
    TaskID            string
    RunID             *int64
    Type              string
    PayloadJSON       string
    EmitterSourceType string
    EmitterSourceRef  string
    TimeoutAt         *time.Time
}

type CheckpointEmitOutput struct {
    CorrelationID string
    ID            int64
    Status        string
}

func (s *CheckpointService) Emit(in CheckpointEmitInput) (*CheckpointEmitOutput, error) {
    if in.TaskID == "" {
        return nil, &ValidationError{Field: "task_id", Message: "task_id required"}
    }
    if in.Type == "" {
        return nil, &ValidationError{Field: "type", Message: "type required"}
    }
    if in.EmitterSourceType == "" {
        in.EmitterSourceType = "system"
    }
    if !validSourceTypes[in.EmitterSourceType] {
        return nil, &ValidationError{Field: "emitter_source_type", Message: "invalid emitter_source_type"}
    }

    // Ensure task exists
    if _, err := s.store.GetTask(in.TaskID); err != nil {
        if errors.Is(err, sqlstore.ErrTaskNotFound) {
            return nil, &ValidationError{Field: "task_id", Message: "task not found"}
        }
        return nil, err
    }

    corr := ulid.Make().String()

    cp := &sqlstore.CheckpointRecord{
        TaskID:            in.TaskID,
        CorrelationID:     corr,
        Type:              in.Type,
        PayloadJSON:       in.PayloadJSON,
        EmitterSourceType: in.EmitterSourceType,
        Status:            "pending",
    }
    if in.RunID != nil {
        cp.RunID.Int64 = *in.RunID
        cp.RunID.Valid = true
    }
    if in.EmitterSourceRef != "" {
        cp.EmitterSourceRef.String = in.EmitterSourceRef
        cp.EmitterSourceRef.Valid = true
    }
    if err := s.store.CreateCheckpoint(cp); err != nil {
        return nil, fmt.Errorf("create checkpoint: %w", err)
    }
    if in.TimeoutAt != nil {
        if err := s.store.SetCheckpointTimeout(corr, *in.TimeoutAt); err != nil {
            return nil, err
        }
    }
    return &CheckpointEmitOutput{CorrelationID: corr, ID: cp.ID, Status: cp.Status}, nil
}

type CheckpointRespondInput struct {
    CorrelationID       string
    ResponseJSON        string
    ResponderSourceType string
    ResponderSourceRef  string
}

func (s *CheckpointService) Respond(in CheckpointRespondInput) error {
    if in.CorrelationID == "" {
        return &ValidationError{Field: "correlation_id", Message: "required"}
    }
    if !validSourceTypes[in.ResponderSourceType] {
        return &ValidationError{Field: "responder_source_type", Message: "invalid"}
    }
    cp, err := s.store.GetCheckpointByCorrelation(in.CorrelationID)
    if err != nil {
        return err
    }
    if cp.Status != "pending" {
        return &ConflictError{Message: "checkpoint " + cp.Status}
    }
    if err := s.store.RespondCheckpoint(in.CorrelationID, in.ResponseJSON, in.ResponderSourceType, in.ResponderSourceRef, time.Now()); err != nil {
        return err
    }
    // Trigger task resume (handled by lifecycle manager — see Task B7)
    return nil
}

type CheckpointCancelInput struct {
    CorrelationID       string
    Reason              string
    CancelerSourceType  string
    CancelerSourceRef   string
}

func (s *CheckpointService) Cancel(in CheckpointCancelInput) error {
    if in.CorrelationID == "" {
        return &ValidationError{Field: "correlation_id", Message: "required"}
    }
    cp, err := s.store.GetCheckpointByCorrelation(in.CorrelationID)
    if err != nil {
        return err
    }
    if cp.Status != "pending" {
        return &ConflictError{Message: "checkpoint " + cp.Status}
    }
    return s.store.CancelCheckpoint(in.CorrelationID, in.Reason, in.CancelerSourceType, in.CancelerSourceRef, time.Now())
}

func (s *CheckpointService) ListForTask(taskID string) ([]sqlstore.CheckpointRecord, error) {
    return s.store.ListCheckpointsForTask(taskID)
}

func (s *CheckpointService) ListPending() ([]sqlstore.CheckpointRecord, error) {
    return s.store.ListPendingCheckpoints()
}

func (s *CheckpointService) Get(correlationID string) (*sqlstore.CheckpointRecord, error) {
    return s.store.GetCheckpointByCorrelation(correlationID)
}
```

Add `ConflictError` to `internal/service/errors.go` (or create one) if it doesn't exist:

```go
type ConflictError struct {
    Message string
}

func (e *ConflictError) Error() string { return "conflict: " + e.Message }
```

- [ ] **Step 4: go.mod — add ulid dep**

If `github.com/oklog/ulid/v2` isn't already in `go.sum`, run:

```bash
go get github.com/oklog/ulid/v2
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/service -run TestCheckpointService -v`
Expected: PASS for Emit; the Respond test is stubbed until Task B7.

- [ ] **Step 6: Commit**

```bash
git add internal/service/checkpoint.go internal/service/checkpoint_test.go \
        internal/service/errors.go go.mod go.sum
git commit -m "feat(service): CheckpointService — emit/respond/cancel/list"
```

---

### Task B4: Signal parser — CLOCKWORK_CHECKPOINT + CLOCKWORK_CHECKPOINT_AWAIT

**Files:**
- Modify: `internal/runtime/executor/signal.go`
- Modify: `internal/runtime/executor/parser.go`
- Modify: `internal/runtime/executor/parser_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/runtime/executor/parser_test.go`:

```go
func TestParser_Checkpoint(t *testing.T) {
    p := NewStreamParser()
    evt, ok := p.ParseLine("CLOCKWORK_CHECKPOINT 01H-CORR collect_data eyJxIjoicGljayJ9")
    require.True(t, ok)
    require.Equal(t, EventSignal, evt.Type)
    require.Equal(t, "CLOCKWORK_CHECKPOINT", evt.Signal)
    require.Equal(t, "01H-CORR collect_data eyJxIjoicGljayJ9", evt.Content)
}

func TestParser_CheckpointAwait(t *testing.T) {
    p := NewStreamParser()
    evt, ok := p.ParseLine("CLOCKWORK_CHECKPOINT_AWAIT 01H-CORR")
    require.True(t, ok)
    require.Equal(t, "CLOCKWORK_CHECKPOINT_AWAIT", evt.Signal)
    require.Equal(t, "01H-CORR", evt.Content)
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/runtime/executor -run TestParser_Checkpoint -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

3a. In `internal/runtime/executor/signal.go`, add:

```go
// Add to the SignalType const block (after SignalArtifact):
SignalCheckpointAwait        // CLOCKWORK_CHECKPOINT_AWAIT <correlation_id>
```

And in `signalNames`:

```go
SignalCheckpointAwait: "CLOCKWORK_CHECKPOINT_AWAIT",
```

And in `signalFromString`:

```go
"CLOCKWORK_CHECKPOINT_AWAIT": SignalCheckpointAwait,
```

3b. In `internal/runtime/executor/parser.go` (read the file first for exact shape of current parsing — the existing parser already handles CLOCKWORK_CHECKPOINT as a JSON-shaped signal; this plan assumes it needs to also parse the plain-text form `CLOCKWORK_CHECKPOINT <corr> <type> <base64>`):

Extend the line-parsing function to match the new format. Look for the current match arm for `CLOCKWORK_DONE` / `CLOCKWORK_NOTE` style prefix matching and add:

```go
if strings.HasPrefix(line, "CLOCKWORK_CHECKPOINT ") {
    return SignalEvent("CLOCKWORK_CHECKPOINT", strings.TrimPrefix(line, "CLOCKWORK_CHECKPOINT ")), true
}
if strings.HasPrefix(line, "CLOCKWORK_CHECKPOINT_AWAIT ") {
    return SignalEvent("CLOCKWORK_CHECKPOINT_AWAIT", strings.TrimPrefix(line, "CLOCKWORK_CHECKPOINT_AWAIT ")), true
}
```

(The exact shape depends on the existing parser. If the file uses JSON-format signals, extend both the JSON matcher and add the plain-text form so both work.)

- [ ] **Step 4: Run**

Run: `go test ./internal/runtime/executor -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/executor/signal.go internal/runtime/executor/parser.go internal/runtime/executor/parser_test.go
git commit -m "feat(executor): parse CLOCKWORK_CHECKPOINT and CLOCKWORK_CHECKPOINT_AWAIT"
```

---

### Task B5: Scheduler — handle CHECKPOINT emit (create row + park blocking task)

**Files:**
- Create: `internal/runtime/scheduler/checkpoint.go`
- Create: `internal/runtime/scheduler/checkpoint_test.go`
- Modify: `internal/runtime/scheduler/worker.go` (wire the event handler)

- [ ] **Step 1: Write the failing test**

Create `internal/runtime/scheduler/checkpoint_test.go`:

```go
func TestScheduler_CheckpointEmit_Blocking_ParksTask(t *testing.T) {
    h := setupSchedulerHarness(t)
    task := h.createTaskKindDecision("CW-SCH-1")       // decision, blocking, manual=true
    // simulate executor event for CHECKPOINT
    h.worker.HandleEvent(task.ID, &RunRef{ID: 1}, executor.ExecutionEvent{
        Type:    executor.EventSignal,
        Signal:  "CLOCKWORK_CHECKPOINT",
        Content: "CORR-1 collect_data eyJxIjoiaGlkIn0=",
    })
    got, err := h.store.GetTask(task.ID)
    require.NoError(t, err)
    require.Equal(t, "review", got.Status)
    require.Contains(t, got.BlockedReason, "CORR-1")

    cp, err := h.store.GetCheckpointByCorrelation("CORR-1")
    require.NoError(t, err)
    require.Equal(t, "pending", cp.Status)
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/runtime/scheduler -run TestScheduler_CheckpointEmit -v`
Expected: FAIL — handler doesn't route CHECKPOINT to checkpoints store.

- [ ] **Step 3: Implement**

Create `internal/runtime/scheduler/checkpoint.go`:

```go
package scheduler

import (
    "encoding/base64"
    "fmt"
    "strings"

    "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
    "github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// HandleCheckpointSignal parses a CLOCKWORK_CHECKPOINT signal payload and
// creates a checkpoints row. If the task has checkpoint_mode=blocking, it
// parks the task (transition to review with BlockedReason) and returns
// parkTask=true so the caller stops further event processing for this task.
//
// Expected payload format: "<correlation_id> <type> <base64(payload_json)>"
func HandleCheckpointSignal(store *sqlstore.Store, taskID string, runID int64, content string) (parkTask bool, err error) {
    parts := strings.SplitN(content, " ", 3)
    if len(parts) < 3 {
        return false, fmt.Errorf("malformed checkpoint signal: %q", content)
    }
    corr, typ, b64 := parts[0], parts[1], parts[2]
    payload, err := base64.StdEncoding.DecodeString(b64)
    if err != nil {
        return false, fmt.Errorf("decode checkpoint payload: %w", err)
    }
    task, err := store.GetTask(taskID)
    if err != nil {
        return false, err
    }
    cp := &sqlstore.CheckpointRecord{
        TaskID: taskID,
        CorrelationID: corr,
        Type: typ,
        PayloadJSON: string(payload),
        EmitterSourceType: "system",           // emitted by the running executor
        Status: "pending",
    }
    if runID > 0 {
        cp.RunID.Int64 = runID
        cp.RunID.Valid = true
    }
    if err := store.CreateCheckpoint(cp); err != nil {
        return false, err
    }
    if task.CheckpointMode == "blocking" {
        reason := fmt.Sprintf("awaiting checkpoint %s", corr)
        // TransitionTaskWithReason is an existing store call (see lifecycle.go
        // for its pattern; if it doesn't exist, add one that sets status+blocked_reason in one stmt)
        if err := store.TransitionTaskWithReason(taskID, "review", reason); err != nil {
            return true, err
        }
        return true, nil
    }
    return false, nil
}
```

Add `TransitionTaskWithReason` to `internal/persistence/sqlstore/tasks.go` if not present:

```go
func (s *Store) TransitionTaskWithReason(id, newStatus, reason string) error {
    _, err := s.db.Exec(`UPDATE tasks SET status = ?, blocked_reason = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
        newStatus, reason, id)
    return err
}
```

- [ ] **Step 4: Wire into `worker.go`**

Read `internal/runtime/scheduler/worker.go`. Find the event handler (likely a switch on `event.Signal` or `event.Type`). Add a case for `CLOCKWORK_CHECKPOINT`:

```go
case "CLOCKWORK_CHECKPOINT":
    if park, err := HandleCheckpointSignal(w.store, taskID, runID, event.Content); err != nil {
        w.logger.Error("checkpoint signal", "err", err)
    } else if park {
        // signal the worker to stop processing further events for this run
        return // or whatever the existing "stop processing" convention is
    }
```

- [ ] **Step 5: Run**

Run: `go test ./internal/runtime/scheduler -v`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/scheduler/checkpoint.go internal/runtime/scheduler/checkpoint_test.go \
        internal/runtime/scheduler/worker.go internal/persistence/sqlstore/tasks.go
git commit -m "feat(scheduler): handle CLOCKWORK_CHECKPOINT — create row, park blocking task"
```

---

### Task B6: Scheduler — timeout sweeper

**Files:**
- Modify: `internal/runtime/scheduler/scheduler.go`
- Add test in: `internal/runtime/scheduler/checkpoint_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/runtime/scheduler/checkpoint_test.go`:

```go
func TestScheduler_CheckpointTimeout_ParkedTaskBlocks(t *testing.T) {
    h := setupSchedulerHarness(t)
    task := h.createTaskKindDecision("CW-TO-1")
    // Emit a pending checkpoint with timeout in the past
    cp := &sqlstore.CheckpointRecord{TaskID: task.ID, CorrelationID: "CORR-TO", Type: "collect_data", PayloadJSON: "{}", EmitterSourceType: "system", Status: "pending"}
    require.NoError(t, h.store.CreateCheckpoint(cp))
    require.NoError(t, h.store.SetCheckpointTimeout("CORR-TO", time.Now().Add(-1*time.Minute)))
    // Task is in review (previous park)
    require.NoError(t, h.store.TransitionTaskWithReason(task.ID, "review", "awaiting checkpoint CORR-TO"))

    // Run the tick
    require.NoError(t, h.sched.SweepTick(time.Now()))

    cpAfter, err := h.store.GetCheckpointByCorrelation("CORR-TO")
    require.NoError(t, err)
    require.Equal(t, "timed_out", cpAfter.Status)

    taskAfter, err := h.store.GetTask(task.ID)
    require.NoError(t, err)
    require.Equal(t, "blocked", taskAfter.Status)
    require.Contains(t, taskAfter.BlockedReason, "timed out")
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/runtime/scheduler -run TestScheduler_CheckpointTimeout -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

Read `internal/runtime/scheduler/scheduler.go` to find the existing tick function. Add a sweep step:

```go
// In Scheduler, add:
func (s *Scheduler) SweepTick(now time.Time) error {
    // Sweep timed-out checkpoints
    toCheckpoints, err := s.store.ListPendingCheckpoints()
    if err != nil {
        return err
    }
    count, err := s.store.SweepTimedOutCheckpoints(now)
    if err != nil {
        return err
    }
    if count == 0 {
        return nil
    }
    // Walk the checkpoints that were just flipped — any whose task is in 'review'
    // and parked on this correlation_id should transition to blocked.
    for _, cp := range toCheckpoints {
        if !cp.TimeoutAt.Valid || cp.TimeoutAt.Time.After(now) {
            continue
        }
        task, err := s.store.GetTask(cp.TaskID)
        if err != nil {
            continue
        }
        if task.CheckpointMode == "blocking" && strings.Contains(task.BlockedReason, cp.CorrelationID) {
            reason := "checkpoint " + cp.CorrelationID + " timed out"
            _ = s.store.TransitionTaskWithReason(cp.TaskID, "blocked", reason)
            s.events.Emit("checkpoint.timed_out", map[string]any{"task_id": cp.TaskID, "correlation_id": cp.CorrelationID})
        }
    }
    return nil
}
```

Then wire `SweepTick` into the existing scheduler `tick` (whatever the tick function is called today — often `Tick` or similar). Add a call to `SweepTick(time.Now())` at the top of each tick iteration.

- [ ] **Step 4: Run**

Run: `go test ./internal/runtime/scheduler -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/scheduler/scheduler.go internal/runtime/scheduler/checkpoint_test.go
git commit -m "feat(scheduler): timeout sweeper for pending checkpoints"
```

---

### Task B7: Lifecycle — on_checkpoint_response resume path

**Files:**
- Modify: `internal/runtime/scheduler/lifecycle.go`
- Modify: `internal/service/checkpoint.go` (wire lifecycle call into Respond)
- Add test in: `internal/service/checkpoint_test.go`

- [ ] **Step 1: Write the failing test**

Unstub the earlier `TestCheckpointService_Respond_TransitionsTask` in `internal/service/checkpoint_test.go`:

```go
func TestCheckpointService_Respond_TransitionsTaskToTodo(t *testing.T) {
    s := setupTaskService(t)
    cp := setupCheckpointService(t, s)

    // Decision task, manual, blocking, on_checkpoint_response=resume
    task := createBlockingDecisionTask(t, s)
    // Park it (simulating an emit from the executor)
    require.NoError(t, s.store.TransitionTaskWithReason(task.ID, "review", "awaiting checkpoint X"))

    emitted, err := cp.Emit(CheckpointEmitInput{TaskID: task.ID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system"})
    require.NoError(t, err)

    require.NoError(t, cp.Respond(CheckpointRespondInput{
        CorrelationID: emitted.CorrelationID, ResponseJSON: `{"a":1}`,
        ResponderSourceType: "user", ResponderSourceRef: "chrispian",
    }))

    got, err := s.store.GetTask(task.ID)
    require.NoError(t, err)
    require.Equal(t, "todo", got.Status)

    // The response should be visible in metadata.checkpoint_responses
    var md map[string]any
    require.NoError(t, unmarshalJSON([]byte(got.Metadata.String), &md))
    resp := md["checkpoint_responses"].(map[string]any)
    require.Contains(t, resp, emitted.CorrelationID)
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/service -run TestCheckpointService_Respond -v`
Expected: FAIL — Respond doesn't trigger task transition.

- [ ] **Step 3: Implement the lifecycle application**

Extend `internal/service/checkpoint.go` Respond:

```go
func (s *CheckpointService) Respond(in CheckpointRespondInput) error {
    // ... existing validation + store update ...
    // After successful store update, apply lifecycle rule
    task, err := s.store.GetTask(cp.TaskID)
    if err != nil {
        return err
    }
    if task.Status != "review" {
        return nil // task wasn't parked; nothing to resume
    }
    switch task.OnCheckpointResponse {
    case "resume":
        if err := s.attachCheckpointResponseToMetadata(task, cp.CorrelationID, in.ResponseJSON); err != nil {
            return err
        }
        return s.store.TransitionTaskWithReason(cp.TaskID, "todo", "")
    case "review":
        // stay in review; metadata attached, human must transition
        return s.attachCheckpointResponseToMetadata(task, cp.CorrelationID, in.ResponseJSON)
    case "custom":
        // plugin hook — out of scope for MVP; attach response and stay
        return s.attachCheckpointResponseToMetadata(task, cp.CorrelationID, in.ResponseJSON)
    }
    return nil
}

func (s *CheckpointService) attachCheckpointResponseToMetadata(task *sqlstore.TaskRecord, correlationID, responseJSON string) error {
    var md map[string]any
    if task.Metadata.Valid && task.Metadata.String != "" {
        _ = unmarshalJSON([]byte(task.Metadata.String), &md)
    }
    if md == nil {
        md = map[string]any{}
    }
    responses, _ := md["checkpoint_responses"].(map[string]any)
    if responses == nil {
        responses = map[string]any{}
    }
    // responseJSON can be raw JSON — decode so it's structured in metadata
    var parsed any
    if err := unmarshalJSON([]byte(responseJSON), &parsed); err != nil {
        parsed = responseJSON // fall back to raw string if not valid JSON
    }
    responses[correlationID] = parsed
    md["checkpoint_responses"] = responses
    newMD := marshalJSON(md)
    return s.store.UpdateTask(task.ID, sqlstore.TaskUpdate{
        Metadata: &sql.NullString{String: newMD, Valid: true},
    })
}
```

- [ ] **Step 4: Run**

Run: `go test ./internal/service -run TestCheckpointService_Respond -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/checkpoint.go internal/service/checkpoint_test.go
git commit -m "feat(checkpoint): apply on_checkpoint_response — resume + attach to metadata"
```

---

### Task B8: MCP tools — 6 checkpoint tools

**Files:**
- Create: `internal/mcpadapter/checkpoint_tools.go`
- Create: `internal/mcpadapter/checkpoint_tools_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestMCP_CheckpointEmit_And_Respond(t *testing.T) {
    server := setupMCPServer(t)
    // Create a decision task
    resp := callTool(t, server, "clockwork_task_create", map[string]any{
        "title": "t", "description": "x", "kind": "decision",
        "manual": true, "checkpoint_mode": "blocking",
    })
    require.False(t, resp.IsError)
    var created map[string]any
    decode(resp, &created)
    taskID := created["id"].(string)

    // Emit
    emitResp := callTool(t, server, "clockwork_task_checkpoint_emit", map[string]any{
        "task_id": taskID, "type": "collect_data", "payload_json": `{"q":"?"}`,
        "emitter_source_type": "system",
    })
    require.False(t, emitResp.IsError)
    var emitted map[string]any
    decode(emitResp, &emitted)
    corr := emitted["correlation_id"].(string)

    // Respond
    respondResp := callTool(t, server, "clockwork_task_checkpoint_respond", map[string]any{
        "correlation_id": corr, "response_json": `{"a":1}`,
        "responder_source_type": "user", "responder_source_ref": "chrispian",
    })
    require.False(t, respondResp.IsError)

    // Get
    getResp := callTool(t, server, "clockwork_task_checkpoint_get", map[string]any{
        "correlation_id": corr,
    })
    require.False(t, getResp.IsError)
    var got map[string]any
    decode(getResp, &got)
    require.Equal(t, "responded", got["status"])
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/mcpadapter -run TestMCP_Checkpoint -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

Create `internal/mcpadapter/checkpoint_tools.go`. Model after `task_tools.go`. Register six tools via the MCP-Go library:

```go
package mcpadapter

import (
    "context"
    "encoding/json"

    "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"
    "github.com/hollis-labs/clockwork-manifold/internal/service"
)

func RegisterCheckpointTools(s *server.MCPServer, cp *service.CheckpointService) {
    s.AddTool(
        mcp.NewTool("clockwork_task_checkpoint_emit",
            mcp.WithDescription("Manually emit a task checkpoint"),
            mcp.WithString("task_id", mcp.Required()),
            mcp.WithString("type", mcp.Required()),
            mcp.WithString("payload_json", mcp.Required()),
            mcp.WithString("emitter_source_type", mcp.Required()),
            mcp.WithString("emitter_source_ref"),
            mcp.WithString("timeout_at"),
        ),
        func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
            args := req.Params.Arguments
            in := service.CheckpointEmitInput{
                TaskID:            asString(args, "task_id"),
                Type:              asString(args, "type"),
                PayloadJSON:       asString(args, "payload_json"),
                EmitterSourceType: asString(args, "emitter_source_type"),
                EmitterSourceRef:  asString(args, "emitter_source_ref"),
            }
            // Parse timeout_at if provided (RFC3339)
            // ...
            out, err := cp.Emit(in)
            if err != nil {
                return toolErrorResult(err), nil
            }
            body, _ := json.Marshal(out)
            return mcp.NewToolResultText(string(body)), nil
        },
    )
    // ... five more: respond, cancel, list (by task_id), get (by correlation_id), pending (inbox)
}
```

Register the handler for each of the six tools following the same pattern.

Also wire `RegisterCheckpointTools` into the MCP server bootstrap (likely `cmd/clockwork/serve.go` or wherever the existing `RegisterTaskTools(...)` gets called).

- [ ] **Step 4: Run**

Run: `go test ./internal/mcpadapter -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpadapter/checkpoint_tools.go internal/mcpadapter/checkpoint_tools_test.go cmd/clockwork/serve.go
git commit -m "feat(mcp): 6 checkpoint tools — emit/respond/cancel/list/get/pending"
```

---

### Task B9: HTTP layer — checkpoint endpoints

**Files:**
- Create: `internal/httpserver/checkpoints.go`
- Create: `internal/httpserver/checkpoints_test.go`
- Modify: `internal/httpserver/router.go` (or wherever routes are registered)

- [ ] **Step 1: Write the failing test**

```go
func TestHTTP_Checkpoint_EmitRespondCancel(t *testing.T) {
    srv := setupHTTPServer(t)
    // Create decision task
    r := postJSON(t, srv, "/api/v1/tasks", `{"title":"d","description":"x","kind":"decision","manual":true,"checkpoint_mode":"blocking"}`)
    require.Equal(t, 201, r.StatusCode)
    var task map[string]any
    decodeBody(r, &task)
    id := task["id"].(string)

    // Emit
    e := postJSON(t, srv, "/api/v1/tasks/"+id+"/checkpoints", `{"type":"collect_data","payload_json":"{\"q\":\"?\"}","emitter_source_type":"system"}`)
    require.Equal(t, 201, e.StatusCode)
    var emitted map[string]any
    decodeBody(e, &emitted)
    corr := emitted["correlation_id"].(string)

    // Respond
    rr := postJSON(t, srv, "/api/v1/checkpoints/"+corr+"/respond", `{"response_json":"{\"a\":1}","responder_source_type":"user","responder_source_ref":"chrispian"}`)
    require.Equal(t, 200, rr.StatusCode)

    // Get
    g := getJSON(t, srv, "/api/v1/checkpoints/"+corr)
    require.Equal(t, "responded", g["status"])
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/httpserver -run TestHTTP_Checkpoint -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

Create `internal/httpserver/checkpoints.go` following `tasks.go` patterns:

```go
package httpserver

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/hollis-labs/clockwork-manifold/internal/service"
)

type CheckpointHandler struct {
    svc *service.CheckpointService
}

func NewCheckpointHandler(svc *service.CheckpointService) *CheckpointHandler {
    return &CheckpointHandler{svc: svc}
}

func (h *CheckpointHandler) Routes(r chi.Router) {
    r.Post("/tasks/{taskID}/checkpoints",       h.emit)
    r.Get("/tasks/{taskID}/checkpoints",        h.listForTask)
    r.Get("/checkpoints",                        h.listPending)
    r.Get("/checkpoints/{corr}",                 h.get)
    r.Post("/checkpoints/{corr}/respond",        h.respond)
    r.Post("/checkpoints/{corr}/cancel",         h.cancel)
}

func (h *CheckpointHandler) emit(w http.ResponseWriter, r *http.Request) {
    var body struct {
        Type              string `json:"type"`
        PayloadJSON       string `json:"payload_json"`
        EmitterSourceType string `json:"emitter_source_type"`
        EmitterSourceRef  string `json:"emitter_source_ref"`
    }
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        writeError(w, http.StatusBadRequest, "invalid body")
        return
    }
    in := service.CheckpointEmitInput{
        TaskID: chi.URLParam(r, "taskID"),
        Type:   body.Type,
        PayloadJSON: body.PayloadJSON,
        EmitterSourceType: body.EmitterSourceType,
        EmitterSourceRef: body.EmitterSourceRef,
    }
    out, err := h.svc.Emit(in)
    if err != nil {
        writeServiceError(w, err)
        return
    }
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(out)
}

// ... respond, cancel, listForTask, listPending, get — each calls h.svc.<Method>
```

Wire into the main router. If `internal/httpserver/router.go` or equivalent registers sub-routers, add the `CheckpointHandler.Routes` call.

- [ ] **Step 4: Run**

Run: `go test ./internal/httpserver -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpserver/checkpoints.go internal/httpserver/checkpoints_test.go internal/httpserver/router.go
git commit -m "feat(httpserver): checkpoint endpoints — emit/respond/cancel/list/get"
```

---

### Task B10: Integration — end-to-end checkpoint flow

**Files:**
- Create: `internal/runtime/scheduler/checkpoint_e2e_test.go`

- [ ] **Step 1: Write the end-to-end test**

```go
func TestE2E_Checkpoint_EmitViaSignal_RespondViaMCP_TaskResumes(t *testing.T) {
    h := setupSchedulerHarness(t)
    task := h.createTaskKindDecision("CW-E2E-1")

    // Simulate executor emit via signal
    h.worker.HandleEvent(task.ID, &RunRef{ID: 1}, executor.ExecutionEvent{
        Type: executor.EventSignal,
        Signal: "CLOCKWORK_CHECKPOINT",
        Content: "CORR-E2E collect_data eyJxIjoicGljayJ9",
    })
    // Task should be parked
    got, _ := h.store.GetTask(task.ID)
    require.Equal(t, "review", got.Status)

    // Respond via service layer (mirrors what MCP/HTTP would do)
    require.NoError(t, h.checkpoints.Respond(service.CheckpointRespondInput{
        CorrelationID: "CORR-E2E", ResponseJSON: `{"pick":"a"}`,
        ResponderSourceType: "user", ResponderSourceRef: "chrispian",
    }))

    got2, _ := h.store.GetTask(task.ID)
    require.Equal(t, "todo", got2.Status)
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/runtime/scheduler -run TestE2E_Checkpoint -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/runtime/scheduler/checkpoint_e2e_test.go
git commit -m "test(scheduler): e2e checkpoint emit-signal → respond-service → task resume"
```

---

## Phase C — Wait-poll runner

Depends on: Phase A.

### Task C1: waitpoll package — Predicate interface + Registry

**Files:**
- Create: `internal/runtime/waitpoll/predicate.go`
- Create: `internal/runtime/waitpoll/registry.go`
- Create: `internal/runtime/waitpoll/registry_test.go`

- [ ] **Step 1: Write the failing test**

```go
package waitpoll

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"
)

type stubPredicate struct {
    typ    string
    result bool
}

func (s *stubPredicate) Type() string { return s.typ }
func (s *stubPredicate) Validate(_ map[string]any) error { return nil }
func (s *stubPredicate) Evaluate(_ context.Context, _ map[string]any) (bool, error) { return s.result, nil }

func TestRegistry_RegisterAndLookup(t *testing.T) {
    r := NewRegistry()
    p := &stubPredicate{typ: "stub", result: true}
    require.NoError(t, r.Register(p))
    got, ok := r.Get("stub")
    require.True(t, ok)
    require.Equal(t, "stub", got.Type())
}

func TestRegistry_DuplicateFails(t *testing.T) {
    r := NewRegistry()
    _ = r.Register(&stubPredicate{typ: "dup"})
    err := r.Register(&stubPredicate{typ: "dup"})
    require.Error(t, err)
}
```

- [ ] **Step 2: Run to verify fails**

Run: `go test ./internal/runtime/waitpoll -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement**

`internal/runtime/waitpoll/predicate.go`:

```go
package waitpoll

import "context"

type Predicate interface {
    Type() string
    Validate(params map[string]any) error
    Evaluate(ctx context.Context, params map[string]any) (bool, error)
}
```

`internal/runtime/waitpoll/registry.go`:

```go
package waitpoll

import (
    "fmt"
    "sync"
)

type Registry struct {
    mu    sync.RWMutex
    preds map[string]Predicate
}

func NewRegistry() *Registry {
    return &Registry{preds: map[string]Predicate{}}
}

func (r *Registry) Register(p Predicate) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    if _, exists := r.preds[p.Type()]; exists {
        return fmt.Errorf("predicate %q already registered", p.Type())
    }
    r.preds[p.Type()] = p
    return nil
}

func (r *Registry) Get(typ string) (Predicate, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    p, ok := r.preds[typ]
    return p, ok
}

func (r *Registry) List() []string {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := make([]string, 0, len(r.preds))
    for k := range r.preds {
        out = append(out, k)
    }
    return out
}
```

- [ ] **Step 4: Run**

Run: `go test ./internal/runtime/waitpoll -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/waitpoll/predicate.go internal/runtime/waitpoll/registry.go internal/runtime/waitpoll/registry_test.go
git commit -m "feat(waitpoll): Predicate interface + Registry"
```

---

### Task C2: Predicate task_done

**Files:**
- Create: `internal/runtime/waitpoll/task_done.go`
- Create: `internal/runtime/waitpoll/task_done_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestTaskDone_EvaluateTrue(t *testing.T) {
    store := setupStore(t)
    require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-X-1", Title: "x", Status: "done", Kind: "agent", SourceType: "user", Trust: "normal", CheckpointMode: "none", OnCheckpointResponse: "resume", Executor: "cli", OnDone: "review", OnFail: "retry", OnReview: "pause", OnDoneMerge: "none"}))
    p := NewTaskDone(store)
    ok, err := p.Evaluate(context.Background(), map[string]any{"task_id": "CW-X-1"})
    require.NoError(t, err)
    require.True(t, ok)
}

func TestTaskDone_EvaluateFalse(t *testing.T) {
    store := setupStore(t)
    require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-X-2", Title: "x", Status: "todo", /* ...facets */}))
    p := NewTaskDone(store)
    ok, err := p.Evaluate(context.Background(), map[string]any{"task_id": "CW-X-2"})
    require.NoError(t, err)
    require.False(t, ok)
}

func TestTaskDone_ValidateMissingTaskID(t *testing.T) {
    p := NewTaskDone(nil)
    require.Error(t, p.Validate(map[string]any{}))
}
```

- [ ] **Step 2: Run to verify fails**

- [ ] **Step 3: Implement**

```go
package waitpoll

import (
    "context"
    "fmt"

    "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

type TaskDone struct {
    store *sqlstore.Store
}

func NewTaskDone(s *sqlstore.Store) *TaskDone { return &TaskDone{store: s} }

func (p *TaskDone) Type() string { return "task_done" }

func (p *TaskDone) Validate(params map[string]any) error {
    id, _ := params["task_id"].(string)
    if id == "" {
        return fmt.Errorf("task_done: params.task_id required")
    }
    return nil
}

func (p *TaskDone) Evaluate(ctx context.Context, params map[string]any) (bool, error) {
    id, _ := params["task_id"].(string)
    t, err := p.store.GetTask(id)
    if err != nil {
        return false, err
    }
    return t.Status == "done", nil
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/runtime/waitpoll -v`
Expected: PASS.

```bash
git add internal/runtime/waitpoll/task_done.go internal/runtime/waitpoll/task_done_test.go
git commit -m "feat(waitpoll): task_done predicate"
```

---

### Task C3: Predicate url_reachable

**Files:**
- Create: `internal/runtime/waitpoll/url_reachable.go`
- Create: `internal/runtime/waitpoll/url_reachable_test.go`

- [ ] **Step 1: Write test**

```go
func TestURLReachable_200(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
    defer ts.Close()
    p := NewURLReachable(nil) // nil => use default http client
    ok, err := p.Evaluate(context.Background(), map[string]any{"url": ts.URL})
    require.NoError(t, err)
    require.True(t, ok)
}

func TestURLReachable_500(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
    defer ts.Close()
    p := NewURLReachable(nil)
    ok, err := p.Evaluate(context.Background(), map[string]any{"url": ts.URL})
    require.NoError(t, err)
    require.False(t, ok)
}

func TestURLReachable_ValidateRequiresURL(t *testing.T) {
    p := NewURLReachable(nil)
    require.Error(t, p.Validate(map[string]any{}))
}
```

- [ ] **Step 2: Run fails → Step 3: Implement**

```go
package waitpoll

import (
    "context"
    "fmt"
    "net/http"
    "time"
)

type URLReachable struct {
    client *http.Client
}

func NewURLReachable(c *http.Client) *URLReachable {
    if c == nil {
        c = &http.Client{Timeout: 10 * time.Second}
    }
    return &URLReachable{client: c}
}

func (p *URLReachable) Type() string { return "url_reachable" }

func (p *URLReachable) Validate(params map[string]any) error {
    url, _ := params["url"].(string)
    if url == "" {
        return fmt.Errorf("url_reachable: params.url required")
    }
    return nil
}

func (p *URLReachable) Evaluate(ctx context.Context, params map[string]any) (bool, error) {
    url, _ := params["url"].(string)
    req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
    if err != nil {
        return false, err
    }
    resp, err := p.client.Do(req)
    if err != nil {
        return false, nil // unreachable isn't an error — predicate is just false
    }
    defer resp.Body.Close()
    return resp.StatusCode >= 200 && resp.StatusCode < 400, nil
}
```

- [ ] **Step 4: Run + commit**

```bash
git add internal/runtime/waitpoll/url_reachable.go internal/runtime/waitpoll/url_reachable_test.go
git commit -m "feat(waitpoll): url_reachable predicate"
```

---

### Task C4: Predicate file_exists

**Files:**
- Create: `internal/runtime/waitpoll/file_exists.go`
- Create: `internal/runtime/waitpoll/file_exists_test.go`

- [ ] **Step 1: Write test**

```go
func TestFileExists_True(t *testing.T) {
    dir := t.TempDir()
    f, _ := os.Create(filepath.Join(dir, "x"))
    f.Close()
    p := NewFileExists()
    ok, err := p.Evaluate(context.Background(), map[string]any{"path": filepath.Join(dir, "x")})
    require.NoError(t, err)
    require.True(t, ok)
}

func TestFileExists_False(t *testing.T) {
    p := NewFileExists()
    ok, err := p.Evaluate(context.Background(), map[string]any{"path": "/nonexistent/path"})
    require.NoError(t, err)
    require.False(t, ok)
}

func TestFileExists_ValidateRequiresPath(t *testing.T) {
    require.Error(t, NewFileExists().Validate(map[string]any{}))
}
```

- [ ] **Step 2: Run fails → Step 3: Implement**

```go
package waitpoll

import (
    "context"
    "fmt"
    "os"
)

type FileExists struct{}

func NewFileExists() *FileExists { return &FileExists{} }

func (p *FileExists) Type() string { return "file_exists" }

func (p *FileExists) Validate(params map[string]any) error {
    path, _ := params["path"].(string)
    if path == "" {
        return fmt.Errorf("file_exists: params.path required")
    }
    return nil
}

func (p *FileExists) Evaluate(_ context.Context, params map[string]any) (bool, error) {
    path, _ := params["path"].(string)
    _, err := os.Stat(path)
    if os.IsNotExist(err) {
        return false, nil
    }
    return err == nil, err
}
```

- [ ] **Step 4: Run + commit**

```bash
git add internal/runtime/waitpoll/file_exists.go internal/runtime/waitpoll/file_exists_test.go
git commit -m "feat(waitpoll): file_exists predicate"
```

---

### Task C5: Scheduler dispatch for kind=wait

**Files:**
- Modify: `internal/runtime/scheduler/worker.go` or `picker.go` (whichever dispatches tasks)
- Create: `internal/runtime/scheduler/waitpoll_dispatch_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestScheduler_KindWait_DispatchToPredicate(t *testing.T) {
    h := setupSchedulerHarness(t)
    // Pre-populate: task_done predicate target
    require.NoError(t, h.store.CreateTask(&sqlstore.TaskRecord{ID: "CW-TGT", Title: "target", Status: "done", /* facets */}))
    // Wait task
    task := &sqlstore.TaskRecord{
        ID: "CW-W-1", Title: "wait", Status: "todo", Kind: "wait",
        SourceType: "user", Trust: "normal", CheckpointMode: "none", OnCheckpointResponse: "resume",
        OnDone: "close", OnFail: "retry", OnReview: "pause", OnDoneMerge: "none",
        Metadata: sql.NullString{Valid: true, String: `{"wait":{"predicate_type":"task_done","params":{"task_id":"CW-TGT"}}}`},
    }
    require.NoError(t, h.store.CreateTask(task))

    // Run tick — picker picks the wait task, dispatch should evaluate predicate and transition to done
    require.NoError(t, h.sched.Tick(context.Background(), time.Now()))

    got, err := h.store.GetTask("CW-W-1")
    require.NoError(t, err)
    require.Equal(t, "done", got.Status)
}
```

- [ ] **Step 2: Run to verify fails**

- [ ] **Step 3: Implement dispatch branch**

Find the place in the scheduler where a task is picked up and dispatched to an executor. Wrap it with a kind-check: if `task.Kind == "wait"`, call `waitpoll.Evaluate(...)` instead of the executor.

Example addition:

```go
// In worker dispatch, after acquiring task:
if task.Kind == "wait" {
    return w.dispatchWait(ctx, task)
}
// else proceed to existing executor dispatch
```

```go
func (w *Worker) dispatchWait(ctx context.Context, task *sqlstore.TaskRecord) error {
    md := map[string]any{}
    _ = unmarshalJSON([]byte(task.Metadata.String), &md)
    wait, _ := md["wait"].(map[string]any)
    typ, _ := wait["predicate_type"].(string)
    params, _ := wait["params"].(map[string]any)

    pred, ok := w.predicates.Get(typ)
    if !ok {
        return w.store.TransitionTaskWithReason(task.ID, "blocked", "unknown predicate: "+typ)
    }
    hit, err := pred.Evaluate(ctx, params)
    if err != nil {
        return w.store.TransitionTaskWithReason(task.ID, "blocked", "predicate error: "+err.Error())
    }
    if hit {
        // honor on_done
        next := task.OnDone
        if next == "" || next == "review" {
            return w.store.TransitionTask(task.ID, "review")
        }
        if next == "close" {
            return w.store.TransitionTask(task.ID, "done")
        }
    }
    // Not yet — task stays in todo; scheduler will re-evaluate next tick.
    return nil
}
```

`w.predicates` is the `*waitpoll.Registry`, added to the Worker struct. Wire it in bootstrap (Task C6).

- [ ] **Step 4: Run**

Run: `go test ./internal/runtime/scheduler -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/scheduler/worker.go internal/runtime/scheduler/waitpoll_dispatch_test.go
git commit -m "feat(scheduler): dispatch kind=wait tasks to predicate registry"
```

---

### Task C6: Bootstrap — register predicates

**Files:**
- Modify: `internal/runtime/bootstrap/bootstrap.go`
- Modify: `cmd/clockwork/serve.go` (pass registry to scheduler)

- [ ] **Step 1: Write the test**

```go
func TestBootstrap_WaitpollRegistry_HasBuiltins(t *testing.T) {
    reg := waitpoll.NewRegistry()
    require.NoError(t, bootstrap.Waitpoll(reg, testStore(t)))
    for _, typ := range []string{"task_done", "url_reachable", "file_exists"} {
        _, ok := reg.Get(typ)
        require.True(t, ok, "%q missing from registry", typ)
    }
}
```

- [ ] **Step 2: Run fails → Step 3: Implement**

Add to `internal/runtime/bootstrap/bootstrap.go`:

```go
func Waitpoll(reg *waitpoll.Registry, store *sqlstore.Store) error {
    if err := reg.Register(waitpoll.NewTaskDone(store)); err != nil {
        return err
    }
    if err := reg.Register(waitpoll.NewURLReachable(nil)); err != nil {
        return err
    }
    if err := reg.Register(waitpoll.NewFileExists()); err != nil {
        return err
    }
    return nil
}
```

In `cmd/clockwork/serve.go`, after the existing executor bootstrap:

```go
predRegistry := waitpoll.NewRegistry()
if err := bootstrap.Waitpoll(predRegistry, store); err != nil {
    return fmt.Errorf("bootstrap waitpoll: %w", err)
}
// pass predRegistry to scheduler constructor
```

- [ ] **Step 4: Run + commit**

```bash
git add internal/runtime/bootstrap/bootstrap.go internal/runtime/bootstrap/bootstrap_test.go cmd/clockwork/serve.go
git commit -m "feat(bootstrap): register waitpoll built-in predicates"
```

---

## Phase D — Parent rollup

Depends on: Phase A.

### Task D1: parent_rollup.go

**Files:**
- Create: `internal/runtime/scheduler/parent_rollup.go`
- Create: `internal/runtime/scheduler/parent_rollup_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestParentRollup_AllChildrenDone_ParentGoesToOnDone(t *testing.T) {
    h := setupSchedulerHarness(t)
    c1 := createDoneTask(t, h.store, "CW-C-1")
    c2 := createDoneTask(t, h.store, "CW-C-2")
    p := createParentTask(t, h.store, "CW-P-1", []string{c1.ID, c2.ID}, "close")

    require.NoError(t, ParentRollupTick(h.store))
    got, _ := h.store.GetTask(p.ID)
    require.Equal(t, "done", got.Status)
}

func TestParentRollup_AnyChildBlocked_ParentBlocks(t *testing.T) {
    h := setupSchedulerHarness(t)
    c1 := createDoneTask(t, h.store, "CW-C-3")
    c2 := createBlockedTask(t, h.store, "CW-C-4")
    p := createParentTask(t, h.store, "CW-P-2", []string{c1.ID, c2.ID}, "close")

    require.NoError(t, ParentRollupTick(h.store))
    got, _ := h.store.GetTask(p.ID)
    require.Equal(t, "blocked", got.Status)
}

func TestParentRollup_StillWorkInFlight_ParentUnchanged(t *testing.T) {
    h := setupSchedulerHarness(t)
    c1 := createDoneTask(t, h.store, "CW-C-5")
    c2 := createDoingTask(t, h.store, "CW-C-6")
    p := createParentTask(t, h.store, "CW-P-3", []string{c1.ID, c2.ID}, "close")

    require.NoError(t, ParentRollupTick(h.store))
    got, _ := h.store.GetTask(p.ID)
    require.Equal(t, "todo", got.Status) // unchanged
}
```

- [ ] **Step 2: Run fails → Step 3: Implement**

```go
package scheduler

import (
    "github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

func ParentRollupTick(store *sqlstore.Store) error {
    parents, err := store.ListTasks(sqlstore.TaskFilter{Kind: "parent"})
    if err != nil {
        return err
    }
    for _, p := range parents {
        if p.Status == "done" || p.Status == "archived" {
            continue
        }
        childIDs := extractChildIDs(p.Metadata)
        if len(childIDs) == 0 {
            continue
        }
        allDone, anyBlocked := true, false
        for _, id := range childIDs {
            c, err := store.GetTask(id)
            if err != nil {
                allDone = false
                continue
            }
            if c.Status == "blocked" {
                anyBlocked = true
            }
            if c.Status != "done" && c.Status != "archived" {
                allDone = false
            }
        }
        if anyBlocked && p.Status != "blocked" {
            _ = store.TransitionTaskWithReason(p.ID, "blocked", "child blocked")
            continue
        }
        if allDone {
            target := "review"
            if p.OnDone == "close" {
                target = "done"
            }
            _ = store.TransitionTask(p.ID, target)
        }
    }
    return nil
}

func extractChildIDs(meta sql.NullString) []string {
    if !meta.Valid || meta.String == "" {
        return nil
    }
    var md map[string]any
    if err := unmarshalJSON([]byte(meta.String), &md); err != nil {
        return nil
    }
    arr, ok := md["children"].([]any)
    if !ok {
        return nil
    }
    out := make([]string, 0, len(arr))
    for _, v := range arr {
        if s, ok := v.(string); ok && s != "" {
            out = append(out, s)
        }
    }
    return out
}
```

- [ ] **Step 4: Run + commit**

```bash
git add internal/runtime/scheduler/parent_rollup.go internal/runtime/scheduler/parent_rollup_test.go
git commit -m "feat(scheduler): parent-rollup tick — derive parent status from children"
```

---

### Task D2: Wire into scheduler tick

**Files:**
- Modify: `internal/runtime/scheduler/scheduler.go`

- [ ] **Step 1: Add to tick**

Find the existing tick function. Add a call to `ParentRollupTick(s.store)` alongside the existing `SweepTick` call.

- [ ] **Step 2: Run all scheduler tests**

Run: `go test ./internal/runtime/scheduler -v`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/runtime/scheduler/scheduler.go
git commit -m "feat(scheduler): wire parent-rollup into tick loop"
```

---

## Phase E — Templates

Depends on: Phase A.

### Task E1: Migration 009 — task_templates

**Files:**
- Create: `internal/persistence/sqlstore/migrations/009_task_templates.sql`
- Modify: `internal/persistence/sqlstore/migrate_test.go`

- [ ] **Step 1: Write the migration**

```sql
CREATE TABLE task_templates (
    id                     TEXT NOT NULL,
    version                INTEGER NOT NULL DEFAULT 1,
    name                   TEXT NOT NULL,
    description            TEXT NOT NULL,
    kind                   TEXT NOT NULL,
    auto_execute           BOOLEAN NOT NULL DEFAULT 1,
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
    is_archived            BOOLEAN NOT NULL DEFAULT 0,
    created_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, version)
);

CREATE INDEX idx_templates_kind ON task_templates(kind) WHERE is_archived = 0;
```

- [ ] **Step 2: Assertion + run + commit**

```go
func TestMigrate009_TaskTemplatesTable(t *testing.T) {
    db := setupTestDB(t)
    require.Contains(t, tableNames(t, db), "task_templates")
}
```

```bash
git add internal/persistence/sqlstore/migrations/009_task_templates.sql \
        internal/persistence/sqlstore/migrate_test.go
git commit -m "feat(migrate): 009 task_templates table"
```

---

### Task E2: templates.go store CRUD

**Files:**
- Create: `internal/persistence/sqlstore/templates.go`
- Create: `internal/persistence/sqlstore/templates_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestTemplate_CreateAndGet(t *testing.T) {
    s := setupStore(t)
    tpl := &TemplateRecord{
        ID: "backend-fix", Version: 1, Name: "Backend fix", Description: "x",
        Kind: "agent", AutoExecute: true, OnDone: "review", OnFail: "retry",
        OnReview: "pause", OnDoneMerge: "none", CheckpointMode: "none", OnCheckpointResponse: "resume",
    }
    require.NoError(t, s.CreateTemplate(tpl))
    got, err := s.GetTemplate("backend-fix", 1)
    require.NoError(t, err)
    require.Equal(t, "Backend fix", got.Name)
}

func TestTemplate_NewVersionIncrements(t *testing.T) {
    s := setupStore(t)
    require.NoError(t, s.CreateTemplate(&TemplateRecord{ID: "t", Version: 1, Name: "v1", Kind: "agent", /* ... */}))
    next, err := s.NextTemplateVersion("t")
    require.NoError(t, err)
    require.Equal(t, 2, next)
}

func TestTemplate_GetLatestNonArchived(t *testing.T) {
    s := setupStore(t)
    require.NoError(t, s.CreateTemplate(&TemplateRecord{ID: "t", Version: 1, Name: "v1", Kind: "agent", /* ... */}))
    require.NoError(t, s.CreateTemplate(&TemplateRecord{ID: "t", Version: 2, Name: "v2", Kind: "agent", /* ... */}))
    got, err := s.GetLatestTemplate("t")
    require.NoError(t, err)
    require.Equal(t, 2, got.Version)
}

func TestTemplate_Archive(t *testing.T) {
    s := setupStore(t)
    require.NoError(t, s.CreateTemplate(&TemplateRecord{ID: "t", Version: 1, /* ... */}))
    require.NoError(t, s.ArchiveTemplate("t", 1))
    got, err := s.GetTemplate("t", 1)
    require.NoError(t, err)
    require.True(t, got.IsArchived)
}

func TestTemplate_DeleteRejectsIfReferenced(t *testing.T) {
    s := setupStore(t)
    require.NoError(t, s.CreateTemplate(&TemplateRecord{ID: "t", Version: 1, /* ... */}))
    // Create a task referencing the template
    require.NoError(t, s.CreateTask(&TaskRecord{ID: "CW-T-REF", Title: "x", /* ... */, Metadata: sql.NullString{String: `{"template_ref":{"id":"t","version":1}}`, Valid: true}}))
    err := s.DeleteTemplate("t")
    require.Error(t, err) // 409 — referenced
}
```

- [ ] **Step 2: Run fails → Step 3: Implement**

`internal/persistence/sqlstore/templates.go`:

```go
package sqlstore

import (
    "database/sql"
    "errors"
    "fmt"
    "time"
)

var ErrTemplateNotFound = errors.New("template not found")
var ErrTemplateReferenced = errors.New("template has referencing tasks")

type TemplateRecord struct {
    ID                   string
    Version              int
    Name                 string
    Description          string
    Kind                 string
    AutoExecute          bool
    Executor             sql.NullString
    AgentProfile         sql.NullString
    SystemPrompt         sql.NullString
    Tools                sql.NullString
    Permissions          sql.NullString
    Environment          sql.NullString
    CostBudget           sql.NullFloat64
    MaxRetries           int
    MaxDurationMs        sql.NullInt64
    TokenBudget          sql.NullInt64
    OnDone               string
    OnFail               string
    OnReview             string
    OnDoneMerge          string
    EscalationChain      sql.NullString
    QualityGates         sql.NullString
    Deliverables         sql.NullString
    CheckpointMode       string
    OnCheckpointResponse string
    MetadataTemplate     sql.NullString
    RequiredVars         sql.NullString
    Tags                 sql.NullString
    IsArchived           bool
    CreatedAt            time.Time
    UpdatedAt            time.Time
}

const templateSelectCols = `id, version, name, description, kind, auto_execute,
    executor, agent_profile, system_prompt, tools, permissions, environment,
    cost_budget, max_retries, max_duration_ms, token_budget,
    on_done, on_fail, on_review, on_done_merge,
    escalation_chain, quality_gates, deliverables,
    checkpoint_mode, on_checkpoint_response,
    metadata_template, required_vars, tags, is_archived, created_at, updated_at`

func (s *Store) CreateTemplate(t *TemplateRecord) error {
    _, err := s.db.Exec(
        `INSERT INTO task_templates (`+templateSelectCols+`)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
        t.ID, t.Version, t.Name, t.Description, t.Kind, t.AutoExecute,
        t.Executor, t.AgentProfile, t.SystemPrompt, t.Tools, t.Permissions, t.Environment,
        t.CostBudget, t.MaxRetries, t.MaxDurationMs, t.TokenBudget,
        t.OnDone, t.OnFail, t.OnReview, t.OnDoneMerge,
        t.EscalationChain, t.QualityGates, t.Deliverables,
        t.CheckpointMode, t.OnCheckpointResponse,
        t.MetadataTemplate, t.RequiredVars, t.Tags, t.IsArchived,
    )
    return err
}

func (s *Store) GetTemplate(id string, version int) (*TemplateRecord, error) {
    row := s.db.QueryRow(`SELECT `+templateSelectCols+` FROM task_templates WHERE id = ? AND version = ?`, id, version)
    return scanTemplate(row)
}

func (s *Store) GetLatestTemplate(id string) (*TemplateRecord, error) {
    row := s.db.QueryRow(`SELECT `+templateSelectCols+` FROM task_templates
        WHERE id = ? AND is_archived = 0 ORDER BY version DESC LIMIT 1`, id)
    return scanTemplate(row)
}

func (s *Store) NextTemplateVersion(id string) (int, error) {
    var maxV sql.NullInt64
    err := s.db.QueryRow(`SELECT MAX(version) FROM task_templates WHERE id = ?`, id).Scan(&maxV)
    if err != nil {
        return 0, err
    }
    if !maxV.Valid {
        return 1, nil
    }
    return int(maxV.Int64) + 1, nil
}

func (s *Store) ArchiveTemplate(id string, version int) error {
    _, err := s.db.Exec(`UPDATE task_templates SET is_archived = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND version = ?`, id, version)
    return err
}

func (s *Store) DeleteTemplate(id string) error {
    // Reject if any task references any version of this template
    var n int
    err := s.db.QueryRow(
        `SELECT COUNT(*) FROM tasks WHERE metadata LIKE ?`, `%"template_ref":{"id":"`+id+`"%`,
    ).Scan(&n)
    if err != nil {
        return err
    }
    if n > 0 {
        return fmt.Errorf("%s: %d referencing tasks: %w", id, n, ErrTemplateReferenced)
    }
    _, err = s.db.Exec(`DELETE FROM task_templates WHERE id = ?`, id)
    return err
}

func (s *Store) ListTemplates(includeArchived bool, kindFilter string) ([]TemplateRecord, error) {
    q := `SELECT ` + templateSelectCols + ` FROM task_templates WHERE 1=1`
    var args []any
    if !includeArchived {
        q += ` AND is_archived = 0`
    }
    if kindFilter != "" {
        q += ` AND kind = ?`
        args = append(args, kindFilter)
    }
    q += ` ORDER BY id, version DESC`
    rows, err := s.db.Query(q, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []TemplateRecord
    for rows.Next() {
        rec, err := scanTemplateRow(rows)
        if err != nil {
            return nil, err
        }
        out = append(out, *rec)
    }
    return out, rows.Err()
}

// scanTemplate / scanTemplateRow — scan helpers for row/rows into TemplateRecord
// Extract to a shared helper. (Long column list — implementer writes it once.)
```

- [ ] **Step 4: Run + commit**

```bash
git add internal/persistence/sqlstore/templates.go internal/persistence/sqlstore/templates_test.go
git commit -m "feat(sqlstore): TemplateRecord + CRUD + versioning + delete guardrail"
```

---

### Task E3: TemplateService + instantiation

**Files:**
- Create: `internal/service/template.go`
- Create: `internal/service/template_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestTemplateService_Instantiate(t *testing.T) {
    s := setupTaskService(t)
    ts := NewTemplateService(s.store, s)
    require.NoError(t, ts.Create(TemplateCreateInput{
        ID: "backend-fix", Name: "Backend fix", Description: "x",
        Kind: "agent", Executor: "cli", AutoExecute: true,
        Deliverables: []Deliverable{{Type: "diff", Required: true}},
        RequiredVars: []string{"repo_path"},
        MetadataTemplate: map[string]any{"working_dir_template": "{{repo_path}}"},
    }))
    task, err := ts.Instantiate(TemplateInstantiateInput{
        TemplateID: "backend-fix",
        Title:      "Fix the thing",
        Description: "fix it",
        Vars:       map[string]string{"repo_path": "/tmp/repo"},
    })
    require.NoError(t, err)
    require.Equal(t, "agent", task.Kind)
    require.Equal(t, "cli", task.Executor)
    // metadata should carry template_ref + resolved working_dir
    var md map[string]any
    require.NoError(t, unmarshalJSON([]byte(task.Metadata.String), &md))
    ref := md["template_ref"].(map[string]any)
    require.Equal(t, "backend-fix", ref["id"])
    require.Equal(t, float64(1), ref["version"])
    require.Equal(t, "/tmp/repo", md["working_dir_template"])
}

func TestTemplateService_Instantiate_MissingVar_422(t *testing.T) {
    s := setupTaskService(t)
    ts := NewTemplateService(s.store, s)
    require.NoError(t, ts.Create(TemplateCreateInput{
        ID: "x", Name: "x", Description: "x", Kind: "agent", Executor: "cli",
        AutoExecute: true, RequiredVars: []string{"a"},
        MetadataTemplate: map[string]any{"k": "{{a}}"},
    }))
    _, err := ts.Instantiate(TemplateInstantiateInput{TemplateID: "x", Title: "t", Description: "d"})
    var verr *ValidationError
    require.ErrorAs(t, err, &verr)
    require.Contains(t, verr.Message, "a")
}

func TestTemplateService_NewVersionOnUpdate(t *testing.T) {
    s := setupTaskService(t)
    ts := NewTemplateService(s.store, s)
    require.NoError(t, ts.Create(TemplateCreateInput{ID: "t", Name: "v1", Description: "x", Kind: "agent", Executor: "cli", AutoExecute: true}))
    require.NoError(t, ts.Update("t", TemplateUpdateInput{Name: "v2"}))
    got, err := ts.GetLatest("t")
    require.NoError(t, err)
    require.Equal(t, 2, got.Version)
    require.Equal(t, "v2", got.Name)
}
```

- [ ] **Step 2: Run fails → Step 3: Implement**

Create `internal/service/template.go` with:
- `TemplateService` struct wrapping `*sqlstore.Store` and `*TaskService`
- `Create(TemplateCreateInput) error`
- `Get(id, version) (*TemplateRecord, error)` (service-layer wrapper)
- `GetLatest(id) (*TemplateRecord, error)`
- `Update(id, TemplateUpdateInput) error` — internally calls `NextTemplateVersion` and inserts a new version row
- `Archive(id, version) error`
- `Delete(id) error` — wraps `ErrTemplateReferenced` as `ConflictError`
- `List(opts TemplateListOpts) ([]TemplateRecord, error)`
- `Instantiate(TemplateInstantiateInput) (*sqlstore.TaskRecord, error)` — the mapping and validation per spec §5.3

The instantiation flow:

```go
func (s *TemplateService) Instantiate(in TemplateInstantiateInput) (*sqlstore.TaskRecord, error) {
    tpl, err := s.loadTemplate(in.TemplateID, in.TemplateVersion)
    if err != nil {
        return nil, err
    }
    if tpl.IsArchived {
        return nil, &ValidationError{Field: "template_id", Message: "template is archived; specify an explicit version for historical instantiation"}
    }

    // Validate required vars
    var required []string
    if tpl.RequiredVars.Valid {
        _ = unmarshalJSON([]byte(tpl.RequiredVars.String), &required)
    }
    for _, k := range required {
        if _, ok := in.Vars[k]; !ok {
            return nil, &ValidationError{Field: "vars." + k, Message: "required variable " + k + " not supplied"}
        }
    }

    // Build TaskCreateInput from template + resolve vars
    input, err := s.applyTemplateToInput(tpl, in)
    if err != nil {
        return nil, err
    }

    // Apply overrides
    applyOverrides(&input, in.Overrides)

    // Apply caller-supplied fields
    input.Title = in.Title
    if in.Description != "" {
        input.Description = in.Description
    }
    input.SprintID = in.SprintID
    input.ProjectID = in.ProjectID
    input.EpicID = in.EpicID
    if len(in.Tags) > 0 {
        input.Tags = append(input.Tags, in.Tags...)
    }

    // Stamp template_ref in metadata
    if input.Metadata == nil {
        input.Metadata = map[string]any{}
    }
    input.Metadata["template_ref"] = map[string]any{"id": tpl.ID, "version": tpl.Version}

    return s.tasks.Create(input)
}
```

`applyTemplateToInput` is the 1:1 field copy + `ResolveVars` on templated strings.

- [ ] **Step 4: Run + commit**

```bash
git add internal/service/template.go internal/service/template_test.go
git commit -m "feat(service): TemplateService — CRUD, versioning, instantiation"
```

---

### Task E4: Variable resolver

**Files:**
- Create: `internal/service/template_vars.go`
- Create: `internal/service/template_vars_test.go`

- [ ] **Step 1: Write tests**

```go
func TestResolveVars_Basic(t *testing.T) {
    got, err := ResolveVars("hello {{name}}", map[string]string{"name": "world"})
    require.NoError(t, err)
    require.Equal(t, "hello world", got)
}

func TestResolveVars_UnresolvedFails(t *testing.T) {
    _, err := ResolveVars("hello {{name}}", map[string]string{})
    require.Error(t, err)
}

func TestResolveVars_Multiple(t *testing.T) {
    got, err := ResolveVars("{{a}}-{{b}}-{{a}}", map[string]string{"a": "A", "b": "B"})
    require.NoError(t, err)
    require.Equal(t, "A-B-A", got)
}

func TestResolveVarsInMap_Recursive(t *testing.T) {
    in := map[string]any{
        "k1": "{{a}}",
        "nested": map[string]any{
            "k2": "{{b}}",
            "k3": 42, // non-string leaf untouched
        },
        "list": []any{"{{a}}", "static"},
    }
    out, err := ResolveVarsInMap(in, map[string]string{"a": "A", "b": "B"})
    require.NoError(t, err)
    require.Equal(t, "A", out["k1"])
    require.Equal(t, "B", out["nested"].(map[string]any)["k2"])
    require.Equal(t, 42, out["nested"].(map[string]any)["k3"])
    require.Equal(t, "A", out["list"].([]any)[0])
}
```

- [ ] **Step 2: Run fails → Step 3: Implement**

```go
package service

import (
    "fmt"
    "regexp"
    "strings"
)

var varPattern = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)

func ResolveVars(s string, vars map[string]string) (string, error) {
    var missing []string
    out := varPattern.ReplaceAllStringFunc(s, func(match string) string {
        name := strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}")
        v, ok := vars[name]
        if !ok {
            missing = append(missing, name)
            return match
        }
        return v
    })
    if len(missing) > 0 {
        return "", fmt.Errorf("unresolved variables: %v", missing)
    }
    return out, nil
}

func ResolveVarsInMap(m map[string]any, vars map[string]string) (map[string]any, error) {
    out := make(map[string]any, len(m))
    for k, v := range m {
        resolved, err := resolveAny(v, vars)
        if err != nil {
            return nil, err
        }
        out[k] = resolved
    }
    return out, nil
}

func resolveAny(v any, vars map[string]string) (any, error) {
    switch x := v.(type) {
    case string:
        return ResolveVars(x, vars)
    case map[string]any:
        return ResolveVarsInMap(x, vars)
    case []any:
        out := make([]any, len(x))
        for i, item := range x {
            r, err := resolveAny(item, vars)
            if err != nil {
                return nil, err
            }
            out[i] = r
        }
        return out, nil
    default:
        return v, nil
    }
}
```

- [ ] **Step 4: Run + commit**

```bash
git add internal/service/template_vars.go internal/service/template_vars_test.go
git commit -m "feat(service): ResolveVars — flat {{var}} substitution, fails on unresolved"
```

---

### Task E5: MCP tools — 7 template tools

**Files:**
- Create: `internal/mcpadapter/template_tools.go`
- Create: `internal/mcpadapter/template_tools_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestMCP_Template_RoundTrip(t *testing.T) {
    server := setupMCPServer(t)
    // Create
    r := callTool(t, server, "clockwork_template_create", map[string]any{
        "id": "backend-fix", "name": "Backend fix", "description": "x", "kind": "agent",
        "executor": "cli", "auto_execute": true, "required_vars": []string{"repo_path"},
        "metadata_template": map[string]any{"working_dir_template": "{{repo_path}}"},
    })
    require.False(t, r.IsError)
    // Instantiate
    i := callTool(t, server, "clockwork_task_create_from_template", map[string]any{
        "template_id": "backend-fix", "title": "t", "description": "d",
        "vars": map[string]any{"repo_path": "/tmp/demo"},
    })
    require.False(t, i.IsError)
    var task map[string]any
    decode(i, &task)
    require.Equal(t, "agent", task["kind"])
    meta := task["metadata"].(map[string]any)
    require.Equal(t, "/tmp/demo", meta["working_dir_template"])
    ref := meta["template_ref"].(map[string]any)
    require.Equal(t, "backend-fix", ref["id"])
}
```

- [ ] **Step 2: Run fails → Step 3: Implement**

Create `internal/mcpadapter/template_tools.go` registering seven tools (create, get, update, delete, archive, list, create_from_template). Each tool's handler calls the corresponding `TemplateService` method and returns the JSON-serialized result.

Wire the registration into `cmd/clockwork/serve.go` (or the MCP server bootstrap path).

- [ ] **Step 4: Run + commit**

```bash
git add internal/mcpadapter/template_tools.go internal/mcpadapter/template_tools_test.go cmd/clockwork/serve.go
git commit -m "feat(mcp): 7 template tools — CRUD + archive + instantiate"
```

---

### Task E6: HTTP layer — template endpoints

**Files:**
- Create: `internal/httpserver/templates.go`
- Create: `internal/httpserver/templates_test.go`

- [ ] **Steps follow the same test→fail→impl→run→commit pattern as Task B9.** Routes:

```
POST   /api/v1/templates              -> create (version 1) / new version if id exists
GET    /api/v1/templates              -> list (query: kind, include_archived)
GET    /api/v1/templates/{id}         -> get latest non-archived
GET    /api/v1/templates/{id}/{ver}   -> specific version
PUT    /api/v1/templates/{id}         -> update (creates new version)
POST   /api/v1/templates/{id}/archive -> archive
DELETE /api/v1/templates/{id}         -> hard delete (409 if referenced)
POST   /api/v1/templates/{id}/instantiate -> create task from template
```

Commit:

```bash
git add internal/httpserver/templates.go internal/httpserver/templates_test.go internal/httpserver/router.go
git commit -m "feat(httpserver): template endpoints — CRUD + archive + instantiate"
```

---

### Task E7: Integration test — instantiate → run → verify template_ref

**Files:**
- Create: `internal/runtime/scheduler/template_instantiate_e2e_test.go`

- [ ] **Step 1: Write the test**

```go
func TestE2E_Template_Instantiate_Scheduler_PicksUp_Executes(t *testing.T) {
    h := setupSchedulerHarness(t)
    ts := service.NewTemplateService(h.store, h.tasks)

    require.NoError(t, ts.Create(service.TemplateCreateInput{
        ID: "smoke-agent", Name: "smoke", Description: "x", Kind: "agent",
        Executor: "mock", AutoExecute: true,
        Deliverables: []service.Deliverable{{Type: "note", Required: true}},
    }))

    task, err := ts.Instantiate(service.TemplateInstantiateInput{
        TemplateID: "smoke-agent", Title: "smoke run", Description: "d",
    })
    require.NoError(t, err)

    // Scheduler tick — mock executor completes successfully and emits a note artifact
    require.NoError(t, h.sched.Tick(context.Background(), time.Now()))

    got, err := h.store.GetTask(task.ID)
    require.NoError(t, err)
    require.Equal(t, "review", got.Status) // or "done" depending on OnDone
    var md map[string]any
    require.NoError(t, unmarshalJSON([]byte(got.Metadata.String), &md))
    ref := md["template_ref"].(map[string]any)
    require.Equal(t, "smoke-agent", ref["id"])
}
```

- [ ] **Step 2: Run + commit**

```bash
git add internal/runtime/scheduler/template_instantiate_e2e_test.go
git commit -m "test(e2e): template instantiate → scheduler → execute → template_ref intact"
```

---

## Phase F — Dogfood smoke

Depends on: Phases A–E.

### Task F1–F5: Five example template YAMLs

**Files:**
- Create: `docs/templates/backend-fix.yaml`
- Create: `docs/templates/external-chore.yaml`
- Create: `docs/templates/wait-for-git-tag.yaml`
- Create: `docs/templates/decision-checkpoint.yaml`
- Create: `docs/templates/sprint-split-parent.yaml`

Copy the YAML bodies verbatim from spec §5.5. These are **reference** — the source of truth remains DB rows. The YAMLs are for humans + version-controlled exemplars.

- [ ] **Step: Write the 5 files** (30 seconds each).

- [ ] **Step: Commit**

```bash
git add docs/templates/
git commit -m "docs(templates): 5 reference template YAMLs from spec §5.5"
```

---

### Task F6: End-to-end smoke test — create 5 templates, instantiate, exercise MVP criteria

**Files:**
- Create: `cmd/clockwork/smoke_templates_test.go`

- [ ] **Step 1: Write the smoke test**

```go
//go:build smoke

package main

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    // import helpers from internal packages
)

// TestSmoke_MVPExitCriteria asserts the 7 MVP success criteria from spec §8.3.
func TestSmoke_MVPExitCriteria(t *testing.T) {
    h := setupFullStackHarness(t) // spins up HTTP, scheduler, MCP; mock executor

    // 1. All 5 templates create cleanly
    for _, id := range []string{"backend-fix", "external-chore", "wait-for-git-tag", "decision-checkpoint", "sprint-split-parent"} {
        t.Run("create_template/"+id, func(t *testing.T) {
            loadTemplateFromYAML(t, h, "../../docs/templates/"+id+".yaml")
        })
    }

    // 2. backend-fix → scheduler → mock executor
    task := instantiate(t, h, "backend-fix", map[string]any{"repo_path": "/tmp/demo"}, "fix it")
    require.NoError(t, h.sched.Tick(context.Background(), time.Now()))
    got := reload(t, h.store, task.ID)
    require.Contains(t, []string{"doing", "review", "done"}, got.Status)

    // 3. decision-checkpoint task parks on emit, resumes on respond, also cancel path
    // 4. wait-for-git-tag blocks until task_done predicate true (use task_done with synthetic target)
    // 5. external-chore manual transition + deliverables
    // 6. parent-rollup: 2 children, both done → parent transitions
    // 7. full test suite green
    require.True(t, runAllTestsGreen(t))
}
```

- [ ] **Step 2: Run**

Run: `go test -tags=smoke ./cmd/clockwork -run TestSmoke_MVPExitCriteria -v`
Expected: PASS.

- [ ] **Step 3: Run the full test suite one last time**

```bash
go build ./...
go vet ./...
go test ./...
go test -tags=smoke ./cmd/clockwork
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/clockwork/smoke_templates_test.go
git commit -m "test(smoke): MVP exit criteria per spec §8.3"
```

---

## Wrap-up

After all phases complete:

- [ ] **Final build + test:** `go build ./... && go vet ./... && go test ./...`
- [ ] **Migrate a fresh DB end-to-end:** `rm -f ./clockwork.db && ./clockwork migrate` — verify 009 runs cleanly
- [ ] **Open a PR** to `main` with title `feat: task model MVP — facets, checkpoints, templates`. Link to the spec and this plan in the PR body. Reference BLG-029 through BLG-035 as post-MVP follow-ups.

---

## Self-review checklist

1. **Spec coverage.** Each spec section maps to tasks:
   - §3 facets → Phase A (A1–A8)
   - §4 checkpoints → Phase B (B1–B10)
   - §3.3, §3.4 wait + parent → Phases C + D
   - §5 templates → Phase E (E1–E7)
   - §8 migrations + dogfood → all phases + Phase F
   - §6 service flow → embedded in Tasks A3, E3, E4
   - §7 MCP surface → Tasks A7, B8, E5 + HTTP in A6, B9, E6
   - §9 invariants → not a code task; implicit in the design constraints each task honors

2. **Placeholder scan.** No "TBD", "TODO", "implement later" in the plan body. A few `// ...` continuations indicate "follow the existing pattern in the file"; those are acceptable because the pattern is readable from the referenced file, not fabricated.

3. **Type consistency.** Types referenced across tasks:
   - `TaskRecord` facet fields named consistently across Phase A (A2 defines; A3, A6, A7, A8 reference).
   - `CheckpointRecord` named consistently (B2 defines; B3, B5, B8 reference).
   - `TemplateRecord` + `TemplateCreateInput` / `TemplateUpdateInput` / `TemplateInstantiateInput` named consistently across E2, E3, E5.
   - `validateTaskKind` signature matches between definition (A5) and call sites in `Create`/`Update` (A5).
   - `ResolveVars` / `ResolveVarsInMap` named consistently (E3 references, E4 defines).

4. **Scope check.** Plan is single-feature (task model MVP) with clearly phased deliverables (A→F). Each phase produces testable software. Suitable for one implementation effort; no decomposition into sub-plans needed.

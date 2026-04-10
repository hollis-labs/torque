# Task API Expansion — Design Spec

**Date:** 2026-04-09
**Project:** clockwork-manifold
**Status:** Draft — awaiting user review
**Author:** brainstormed with Claude

## 1. Context

The canonical Clockwork task model (spec `2026-04-07-clockwork-manifold-design.md` Section 3.2) defines ~34 fields per task covering execution context, budget, lifecycle rules, deliverables, dependencies, and metadata. The storage layer has supported all of them since day one — `sqlstore.TaskRecord` and `sqlstore.TaskUpdate` expose every field.

Project 1 (the tag system, merged as PR #1) surfaced `Tag[]` through the HTTP API and TypeScript types. Eleven other fields remain invisible to clients: the task API still accepts ~6 fields on create, ~4 fields on update, and omits 12 fields from the `taskJSON` response.

This project closes that gap. After it lands, the task API exposes the full canonical model — all fields readable on `GET`, writable on `POST` and `PUT`, with strict validation on lifecycle enums and deliverable types.

### Where this sits in the broader roadmap

Three-project chain for the task detail/edit page:

1. **Tag system** — shipped in PR #1
2. **Task API expansion (this spec)** — data/service/HTTP/TS plumbing for the 12 remaining canonical fields
3. **Task detail/edit page** — rebuild the `TaskDetailPage` using the tasks home visual language with the full editable surface now available from Project 2

Project 3 cannot render the detail page correctly without Project 2's field expansion. This project is the necessary plumbing pass before the UI work.

## 2. Goals

- Every field in the canonical task model is readable on `GET /api/v1/tasks/:id`
- Every writable field is settable on `POST /api/v1/tasks` and updatable on `PUT /api/v1/tasks/:id`
- Lifecycle enum fields (`on_done`, `on_fail`, `on_review`, `on_done_merge`) are validated against their allowed value sets on both create and update
- Deliverable type fields are validated against the 12 built-in artifact types
- `DependsOn` references are validated for existence at write time (fast feedback on typos)
- Numeric bound checks on `cost_budget`, `max_retries`, `max_duration_ms`, `token_budget`
- Named request types (`TaskCreateRequest`, `TaskUpdateRequest`) in the HTTP layer provide strong typing and clean error messages
- TypeScript `Task` interface includes every field with enum union types for lifecycle rules
- Clean cutover — no backwards compatibility with the current lean contract (nothing consumes the missing fields yet)
- Shared validation helper in the service layer so `Create` and `Update` enforce the same rules

## 3. Non-Goals

- **Scheduler/executor integration for `Deliverables`.** Project 2 exposes the field through the API read/write surface. Wiring the field into the scheduler/executor task-completion gate is a separate follow-up tracked as a narrowed version of **BLG-20260409-001**. The API-only pass is cheap and unblocks Project 3's UI.
- **Task detail page redesign.** Scheduled as Project 3.
- **New task filter fields on the list endpoint.** `GET /api/v1/tasks?...` still filters on the existing set (status, priority, sprint_id, project_id, epic_id, executor). Filtering by the new fields would be a separate scope.
- **SSE event payload expansion.** Broadcasts still emit the task ID and title. Listeners that want the full task read from the API after receiving the event.
- **Backwards compatibility for consumers that relied on the lean shape.** Nothing consumes the missing fields yet; the frontend's `Task` interface doesn't reference them; the CLI and MCP layers either don't expose the fields or carry them via `sqlstore.TaskRecord` marshaling (unchanged).
- **Permissions schema.** The spec says "permission overrides (optional)" without defining a precise shape. We expose it as a free-form `map[string]any` for now. When a concrete Permissions consumer exists, a follow-up can narrow the shape.
- **Metadata schema.** Same reasoning — free-form `map[string]any`.
- **Input validation for `Tools`, `Files`, `EscalationChain`, `QualityGates`.** These are lists of strings with domain-specific meaning (tool names, file paths, agent names, shell commands). No global validation rule makes sense; each consumer validates its own contents at execution time.
- **PATCH vs PUT harmonization across the task API.** Known inconsistency (task update uses PUT, tag endpoints use PATCH); tracked as a follow-up but not in this spec's scope.

## 4. Field Inventory

### 4.1 Fields already exposed (no work needed)

| Field | Read | Write (create) | Write (update) |
|---|---|---|---|
| `id`, `status`, `created_at`, `updated_at` | ✓ | auto | auto |
| `title`, `description`, `priority` | ✓ | ✓ | ✓ |
| `tags` | ✓ (Project 1) | ✓ (Project 1) | ✓ (Project 1) |
| `manual`, `executor` | ✓ | ✓ | ✗ ← adds in Project 2 |
| `agent_profile`, `working_dir`, `system_prompt` | ✓ | ✗ | ✗ |
| `cost_budget`, `max_retries` | ✓ | ✗ | ✗ |
| `on_done`, `on_fail`, `on_review`, `on_done_merge` | ✓ | ✗ | ✗ |
| `blocked_reason` | ✓ | ✗ | ✗ |
| `sprint_id`, `project_id`, `epic_id` | ✓ | ✗ | ✗ |

All the "write: ✗" entries are gaps in the HTTP handler even though the service layer already accepts them. Project 2 closes them by threading the full request through the new `TaskCreateRequest` / `TaskUpdateRequest` types.

### 4.2 Fields added to the API in Project 2

Twelve fields missing from the current `taskJSON` response and write handlers:

| Field | Storage | HTTP/TS shape | Default | Validation |
|---|---|---|---|---|
| `tools` | `sql.NullString` (JSON blob) | `string[]` / `string[]` | `[]` | none (contents domain-specific) |
| `permissions` | `sql.NullString` (JSON blob) | `map[string]any` / `Record<string, unknown>` | `{}` | none (free-form) |
| `environment` | `sql.NullString` (JSON blob) | `map[string]string` / `Record<string, string>` | `{}` | none (values are env var values) |
| `files` | `sql.NullString` (JSON blob) | `string[]` / `string[]` | `[]` | none |
| `max_duration_ms` | `sql.NullInt64` | `*int64` / `number \| null` | `null` | `-1` (unlimited) or `> 0` if set |
| `token_budget` | `sql.NullInt64` | `*int64` / `number \| null` | `null` | `-1` (unlimited) or `> 0` if set |
| `escalation_chain` | `sql.NullString` (JSON blob) | `[]string` / `string[]` | `[]` | none (agent names) |
| `quality_gates` | `sql.NullString` (JSON blob) | `[]string` / `string[]` | `[]` | none (shell commands) |
| `deliverables` | `sql.NullString` (JSON blob) | `[]Deliverable` / `Deliverable[]` | `[]` | each `type` must be in the 12-value set |
| `deliverable_preset` | `string` (plain column) | `string` / `string` | `""` | none (references named preset) |
| `depends_on` | `sql.NullString` (JSON blob) | `[]string` / `string[]` | `[]` | each ID must exist at write time |
| `metadata` | `sql.NullString` (JSON blob) | `map[string]any` / `Record<string, unknown>` | `{}` | none (free-form) |

## 5. Data Model and Service Layer

### 5.1 New `Deliverable` type in `internal/service/task.go`

```go
// Deliverable declares an expected artifact a task must produce before it
// can be marked complete. See the canonical Clockwork design spec section
// on deliverables for the list of built-in types.
type Deliverable struct {
    Type        string `json:"type"`
    Required    bool   `json:"required"`
    Description string `json:"description,omitempty"`
}
```

Exported from the service package. The HTTP layer serializes it directly via `json.Marshal` (field tags on the struct). The TypeScript layer mirrors it as `interface Deliverable`.

### 5.2 Built-in deliverable types (constants)

```go
var validDeliverableTypes = map[string]bool{
    "diff":         true,
    "test-results": true,
    "screenshot":   true,
    "pr-link":      true,
    "branch":       true,
    "commit":       true,
    "log":          true,
    "finding":      true,
    "report":       true,
    "note":         true,
    "metrics":      true,
    "custom":       true,
}
```

### 5.2a `Unlimited` sentinel for nullable numeric fields

```go
// Unlimited is the sentinel value meaning "no cap" for the three nullable
// numeric task fields: CostBudget, MaxDurationMs, TokenBudget. Use this
// instead of a magic -1 at call sites.
const Unlimited = int64(-1)
```

Exported from the service package. The wire format for `cost_budget` uses `-1.0` (float); `max_duration_ms` and `token_budget` use `-1` (int). Semantic model per nullable numeric field:

| Value | Meaning |
|---|---|
| `null` / omitted | Use default (sprint/global inheritance) |
| `-1` | Explicit unlimited (override inheritance, no cap) |
| `0` | Explicit zero — only valid for `cost_budget`; rejected for `max_duration_ms` and `token_budget` |
| positive | Explicit specific value |
| `< -1` | Rejected with validation error |

This gives clients three distinct states (`null`, `-1`, and specific value) which resolves the most common "reset a previously-set cap" workflow: send `-1` to remove the cap. The remaining edge case — "restore inheritance from sprint defaults after setting a specific value" — still requires task recreation, but is rare enough to defer.

### 5.3 Lifecycle enum sets

```go
var validOnDone      = map[string]bool{"close": true, "review": true, "notify": true}
var validOnFail      = map[string]bool{"retry": true, "block": true, "escalate": true, "notify": true}
var validOnReview    = map[string]bool{"pause": true, "notify": true, "auto-approve": true}
var validOnDoneMerge = map[string]bool{"none": true, "auto": true, "pr": true, "auto-resolve": true}
```

Defaults applied in the service layer when the field is empty:
- `OnDone` → `"review"`
- `OnFail` → `"retry"`
- `OnReview` → `"pause"`
- `OnDoneMerge` → `"none"`

Empty string on create means "use default". Empty string on update also means "use default" — this is consistent with the create semantics and avoids a separate "reset to default" sentinel.

### 5.4 `TaskCreateInput` expansion

Nine new fields added to the existing struct:

```go
type TaskCreateInput struct {
    // ... existing 22 fields unchanged ...
    Title             string
    Description       string
    Priority          int
    Tags              []string
    Manual            bool
    Executor          string
    AgentProfile      string
    WorkingDir        string
    Tools             []string
    SystemPrompt      string
    Files             []string
    CostBudget        *float64
    MaxRetries        *int
    OnDone            string
    OnFail            string
    OnReview          string
    OnDoneMerge       string
    DeliverablePreset string
    DependsOn         []string
    SprintID          string
    ProjectID         string
    EpicID            string

    // New in Project 2
    Permissions       map[string]any
    Environment       map[string]string
    MaxDurationMs     *int64
    TokenBudget       *int64
    EscalationChain   []string
    QualityGates      []string
    Deliverables      []Deliverable
    BlockedReason     string
    Metadata          map[string]any
}
```

### 5.5 Shared validation helper

```go
// validateTaskWrites enforces write-time invariants shared between Create
// and Update. Returns nil on success or a *ValidationError on the first
// failed rule.
func (s *TaskService) validateTaskWrites(fields taskWriteFields) error { ... }
```

`taskWriteFields` is an internal struct that collects the writable fields both `Create` and `Update` need to validate. It is populated from `TaskCreateInput` at `Create` time and from `TaskUpdateInput` at `Update` time (only fields where the update pointer is non-nil are checked).

The helper runs these checks in order and returns the first failure:

1. **Lifecycle enums** — if `OnDone` is non-empty and not in `validOnDone`, return `ValidationError{Field: "on_done", Message: "invalid on_done: got '<v>', expected one of: close, review, notify"}`. Same pattern for the other three.
2. **Numeric bounds** (all use pointer types so `nil` means "not provided"):
   - `CostBudget`: allowed values are `-1` (unlimited), `0` (zero budget), or any positive float. Reject if `v < -1 || (v > -1 && v < 0)`.
   - `MaxRetries`: must be `>= 0` if provided. (No sentinel; zero retries is meaningful.)
   - `MaxDurationMs`: allowed values are `-1` (unlimited) or any positive int. Reject if `v != -1 && v <= 0`.
   - `TokenBudget`: allowed values are `-1` (unlimited) or any positive int. Reject if `v != -1 && v <= 0`.

   Validation error messages name the allowed set explicitly, e.g.: `"max_duration_ms must be -1 (unlimited) or a positive value in milliseconds"`.
3. **Deliverable types** — for each `Deliverables[i]`, if `.Type` is not in `validDeliverableTypes`, return `ValidationError{Field: "deliverables[<i>].type"}`.
4. **`DependsOn` existence** — for each task ID in `DependsOn`, call `s.store.GetTask(id)`. First not-found returns `ValidationError{Field: "depends_on", Message: "task <id> not found"}`. (N+1 lookups, acceptable given typical dependency counts.)

### 5.6 `Create` flow changes

After the existing title/priority/feature-gate validation (unchanged), call `validateTaskWrites` with the full input. If it passes, marshal the new JSON-blob fields into `sql.NullString` on the record and set the numeric nullable fields. The existing marshaling pattern for `Tools`/`Files`/`DependsOn` extends naturally to the new blob fields.

Enum fields default empty → canonical value:

```go
rec.OnDone = orDefault(input.OnDone, "review")
rec.OnFail = orDefault(input.OnFail, "retry")
rec.OnReview = orDefault(input.OnReview, "pause")
rec.OnDoneMerge = orDefault(input.OnDoneMerge, "none")
```

### 5.7 `Update` flow changes

```go
func (s *TaskService) Update(id string, input TaskUpdateInput) error {
    if err := s.validateTaskWrites(extractUpdateFields(input)); err != nil {
        return err
    }
    if err := s.store.UpdateTask(id, input.TaskUpdate); err != nil {
        return err
    }
    if input.Tags != nil {
        slugs, err := s.tags.ResolveNames(*input.Tags)
        if err != nil {
            return err
        }
        return s.store.SetTaskTags(id, slugs)
    }
    return nil
}
```

`extractUpdateFields` builds a `taskWriteFields` from the non-nil pointer fields on `input.TaskUpdate`. Fields where the pointer is nil are skipped (no validation runs for "no change").

## 6. HTTP Contract

### 6.1 New named request type — `TaskCreateRequest`

File: `internal/httpserver/tasks.go` (colocated with handlers).

```go
type TaskCreateRequest struct {
    Title             string                `json:"title"`
    Description       string                `json:"description,omitempty"`
    Priority          int                   `json:"priority,omitempty"`
    Tags              []string              `json:"tags,omitempty"`
    Manual            bool                  `json:"manual,omitempty"`
    Executor          string                `json:"executor,omitempty"`
    AgentProfile      string                `json:"agent_profile,omitempty"`
    WorkingDir        string                `json:"working_dir,omitempty"`
    Tools             []string              `json:"tools,omitempty"`
    Permissions       map[string]any        `json:"permissions,omitempty"`
    Environment       map[string]string     `json:"environment,omitempty"`
    SystemPrompt      string                `json:"system_prompt,omitempty"`
    Files             []string              `json:"files,omitempty"`
    CostBudget        *float64              `json:"cost_budget,omitempty"`
    MaxRetries        *int                  `json:"max_retries,omitempty"`
    MaxDurationMs     *int64                `json:"max_duration_ms,omitempty"`
    TokenBudget       *int64                `json:"token_budget,omitempty"`
    OnDone            string                `json:"on_done,omitempty"`
    OnFail            string                `json:"on_fail,omitempty"`
    OnReview          string                `json:"on_review,omitempty"`
    OnDoneMerge       string                `json:"on_done_merge,omitempty"`
    EscalationChain   []string              `json:"escalation_chain,omitempty"`
    QualityGates      []string              `json:"quality_gates,omitempty"`
    Deliverables      []service.Deliverable `json:"deliverables,omitempty"`
    DeliverablePreset string                `json:"deliverable_preset,omitempty"`
    DependsOn         []string              `json:"depends_on,omitempty"`
    BlockedReason     string                `json:"blocked_reason,omitempty"`
    Metadata          map[string]any        `json:"metadata,omitempty"`
    SprintID          string                `json:"sprint_id,omitempty"`
    ProjectID         string                `json:"project_id,omitempty"`
    EpicID            string                `json:"epic_id,omitempty"`
}
```

### 6.2 New named request type — `TaskUpdateRequest`

Every field is a pointer so "omitted" is distinguishable from "explicit zero value":

```go
type TaskUpdateRequest struct {
    Title             *string                `json:"title,omitempty"`
    Description       *string                `json:"description,omitempty"`
    Priority          *int                   `json:"priority,omitempty"`
    Tags              *[]string              `json:"tags,omitempty"`
    Manual            *bool                  `json:"manual,omitempty"`
    Executor          *string                `json:"executor,omitempty"`
    AgentProfile      *string                `json:"agent_profile,omitempty"`
    WorkingDir        *string                `json:"working_dir,omitempty"`
    Tools             *[]string              `json:"tools,omitempty"`
    Permissions       *map[string]any        `json:"permissions,omitempty"`
    Environment       *map[string]string     `json:"environment,omitempty"`
    SystemPrompt      *string                `json:"system_prompt,omitempty"`
    Files             *[]string              `json:"files,omitempty"`
    CostBudget        *float64               `json:"cost_budget,omitempty"`
    MaxRetries        *int                   `json:"max_retries,omitempty"`
    MaxDurationMs     *int64                 `json:"max_duration_ms,omitempty"`
    TokenBudget       *int64                 `json:"token_budget,omitempty"`
    OnDone            *string                `json:"on_done,omitempty"`
    OnFail            *string                `json:"on_fail,omitempty"`
    OnReview          *string                `json:"on_review,omitempty"`
    OnDoneMerge       *string                `json:"on_done_merge,omitempty"`
    EscalationChain   *[]string              `json:"escalation_chain,omitempty"`
    QualityGates      *[]string              `json:"quality_gates,omitempty"`
    Deliverables      *[]service.Deliverable `json:"deliverables,omitempty"`
    DeliverablePreset *string                `json:"deliverable_preset,omitempty"`
    DependsOn         *[]string              `json:"depends_on,omitempty"`
    BlockedReason     *string                `json:"blocked_reason,omitempty"`
    Metadata          *map[string]any        `json:"metadata,omitempty"`
    SprintID          *string                `json:"sprint_id,omitempty"`
    ProjectID         *string                `json:"project_id,omitempty"`
    EpicID            *string                `json:"epic_id,omitempty"`

    // Present for detection only — handler rejects with 400.
    Status *string `json:"status,omitempty"`
}
```

### 6.3 Partial-update semantics (documented contract)

| Client sends | Meaning |
|---|---|
| Omit the field entirely | No change |
| JSON `null` | Same as omit — no change (`json.Unmarshal` sets pointer to nil) |
| `""`, `[]`, `{}` | Explicit clear (empty string, empty array, empty map persisted) |
| `"value"`, `[...]`, `{...}` | Explicit set |
| `-1` (nullable numerics only) | Explicit unlimited — see Section 5.2a |

**Why null and omit collapse:** Go's `encoding/json` produces `nil` for a pointer field whether the JSON contained `null` or the field was absent. There's no way at the type level to distinguish the two. Collapsing them to "no change" is the common REST convention and avoids needing custom unmarshalers or `json.RawMessage` parsing.

**Nullable numeric fields use sentinel values** (per Section 5.2a) to express the three states clients actually need:
- `null` / omitted → no change (or "use default" on create)
- `-1` → explicit unlimited (remove a previously-set cap)
- positive → explicit specific value
- `0` (cost_budget only) → explicit zero budget

The remaining edge case — restoring a previously-set numeric field to SQL NULL (so sprint/global defaults take over again) — is not supported through the update API. A caller that needs this would have to recreate the task. This is deliberate: it's a rare workflow, and adding custom three-state unmarshal logic to cover it adds code you'd mostly never exercise. A future follow-up can add a dedicated "reset field to default" endpoint if a concrete need emerges.

### 6.4 `updateTask` status rejection

```go
if req.Status != nil {
    writeError(w, http.StatusBadRequest,
        "status updates are not allowed via PUT /tasks/:id — use POST /tasks/:id/transition")
    return
}
```

Keeps the FSM-aware transition endpoint as the single source of truth for status changes. Consistent with the non-goal on bypassing the FSM.

### 6.5 Expanded `taskJSON` response

All 12 new fields added to the existing response map. The current field order is preserved for minimal diff noise; the new fields slot in at logical positions (execution → budget → lifecycle → deliverables → metadata):

```go
func taskJSON(t *sqlstore.TaskRecord, tags []sqlstore.TagRecord) map[string]interface{} {
    return map[string]interface{}{
        "id":                 t.ID,
        "title":              t.Title,
        "description":        t.Description,
        "status":             t.Status,
        "priority":           t.Priority,
        "tags":               tagsJSON(tags),
        "manual":             t.Manual,
        "executor":           t.Executor,
        "agent_profile":      t.AgentProfile,
        "working_dir":        t.WorkingDir,
        "tools":              parseStringArray(t.Tools),       // new
        "permissions":        parseFreeMap(t.Permissions),     // new
        "environment":        parseStringMap(t.Environment),   // new
        "system_prompt":      t.SystemPrompt,
        "files":              parseStringArray(t.Files),       // new
        "cost_budget":        nullFloat(t.CostBudget),
        "max_retries":        t.MaxRetries,
        "max_duration_ms":    nullInt(t.MaxDurationMs),        // new
        "token_budget":       nullInt(t.TokenBudget),          // new
        "on_done":            t.OnDone,
        "on_fail":            t.OnFail,
        "on_review":          t.OnReview,
        "on_done_merge":      t.OnDoneMerge,
        "escalation_chain":   parseStringArray(t.EscalationChain), // new
        "quality_gates":      parseStringArray(t.QualityGates),    // new
        "deliverables":       parseDeliverables(t.Deliverables),   // new
        "deliverable_preset": t.DeliverablePreset,                 // new
        "depends_on":         parseStringArray(t.DependsOn),       // new
        "blocked_reason":     t.BlockedReason,
        "metadata":           parseFreeMap(t.Metadata),        // new
        "sprint_id":          nullStr(t.SprintID),
        "project_id":         nullStr(t.ProjectID),
        "epic_id":            nullStr(t.EpicID),
        "created_at":         t.CreatedAt,
        "updated_at":         t.UpdatedAt,
    }
}
```

### 6.6 JSON-blob parse helpers

New helpers in `internal/httpserver/tasks.go`:

```go
// parseStringArray parses a NullString containing a JSON-encoded []string.
// Returns an empty slice on missing/invalid data (never nil, never panics).
func parseStringArray(ns sql.NullString) []string

// parseStringMap parses a NullString containing a JSON-encoded map[string]string.
// Returns an empty map on missing/invalid data.
func parseStringMap(ns sql.NullString) map[string]string

// parseFreeMap parses a NullString containing a JSON-encoded map[string]any.
// Returns an empty map on missing/invalid data.
func parseFreeMap(ns sql.NullString) map[string]any

// parseDeliverables parses a NullString containing a JSON-encoded []Deliverable.
// Returns an empty slice on missing/invalid data.
func parseDeliverables(ns sql.NullString) []service.Deliverable
```

Each returns a non-nil empty value on unparseable input for a stable JSON response shape. Unparseable input is not expected in practice (all blobs are written through `json.Marshal`), but the helpers are defensive against database corruption or older data formats.

### 6.7 JSON-blob write-side helper

```go
// nullJSONString marshals v to JSON and wraps it in a *sql.NullString
// suitable for TaskUpdate pointer fields. Returns a nil-valued NullString
// if marshaling fails (should never happen for well-formed Go values).
func nullJSONString(v any) *sql.NullString {
    raw, err := json.Marshal(v)
    if err != nil {
        return &sql.NullString{Valid: false}
    }
    return &sql.NullString{String: string(raw), Valid: true}
}
```

Used in `updateTask` to set pointer fields on `sqlstore.TaskUpdate` from the request's pointer-to-slice/pointer-to-map values. Example:

```go
if req.Tools != nil {
    update.Tools = nullJSONString(*req.Tools)
}
if req.Permissions != nil {
    update.Permissions = nullJSONString(*req.Permissions)
}
```

### 6.8 Handler shape after refactor

Both `createTask` and `updateTask` become thin translation layers:

1. Parse request body into the typed struct via `readJSON(r, &req)`
2. Validate required fields (`title` on create) and reject status on update
3. Project fields into the service-layer input type
4. Call `s.svc.Task.Create(input)` or `s.svc.Task.Update(id, input)`
5. Map errors:
   - `*service.ValidationError` → 422
   - `json.Unmarshal` error → 400 (from `readJSON`)
   - Other (including existing task-not-found strings) → 500 for update's `s.svc.Task.Get(id)` path, preserving current behavior
6. Load tags via `s.svc.Task.ListTags`, build response via `taskJSON`, broadcast SSE, write response

### 6.9 Error responses

| Status | Trigger |
|---|---|
| `400` | Malformed JSON body, missing `title` on create, `status` field present on update |
| `404` | Task ID not found (on update/get/transition — existing behavior unchanged) |
| `422` | Validation error — enum values, numeric bounds, unknown `DependsOn` task IDs, unknown `Deliverables[].type`, sprint/project/epic not found or not feature-enabled |
| `500` | Unexpected storage errors |

## 7. Frontend Ripple

### 7.1 New TypeScript types in `apps/gui/src/lib/types.ts`

```ts
export type OnDone = 'close' | 'review' | 'notify'
export type OnFail = 'retry' | 'block' | 'escalate' | 'notify'
export type OnReview = 'pause' | 'notify' | 'auto-approve'
export type OnDoneMerge = 'none' | 'auto' | 'pr' | 'auto-resolve'

export type DeliverableType =
  | 'diff'
  | 'test-results'
  | 'screenshot'
  | 'pr-link'
  | 'branch'
  | 'commit'
  | 'log'
  | 'finding'
  | 'report'
  | 'note'
  | 'metrics'
  | 'custom'

export interface Deliverable {
  type: DeliverableType
  required: boolean
  description?: string
}

/**
 * Sentinel value meaning "unlimited — no cap" for the three nullable
 * numeric task fields (cost_budget, max_duration_ms, token_budget).
 */
export const UNLIMITED = -1 as const
```

### 7.2 Expanded `Task` interface

```ts
export interface Task {
  id: string
  title: string
  description: string
  status: TaskStatus
  priority: number
  tags: Tag[]
  manual: boolean
  executor: string
  agent_profile: string
  working_dir: string

  // Execution context
  tools: string[]                          // new
  permissions: Record<string, unknown>     // new
  environment: Record<string, string>      // new
  system_prompt: string
  files: string[]                          // new

  // Budget & limits
  /**
   * Cost budget in dollars. Sentinel values:
   * - `null` — use default (inherit from sprint/global)
   * - `-1` — unlimited (no cap)
   * - `0` — explicit zero (no spend allowed)
   * - positive — specific budget
   */
  cost_budget: number | null
  max_retries: number
  /**
   * Max execution duration in milliseconds. Sentinel values:
   * - `null` — use default
   * - `-1` — unlimited
   * - positive — specific duration
   */
  max_duration_ms: number | null           // new
  /**
   * Max tokens per run. Sentinel values:
   * - `null` — use default
   * - `-1` — unlimited
   * - positive — specific cap
   */
  token_budget: number | null              // new

  // Lifecycle rules (narrowed from string to enum union)
  on_done: OnDone
  on_fail: OnFail
  on_review: OnReview
  on_done_merge: OnDoneMerge

  // Lifecycle extensions
  escalation_chain: string[]               // new
  quality_gates: string[]                  // new

  // Deliverables
  deliverables: Deliverable[]              // new
  deliverable_preset: string               // new

  // Dependencies
  depends_on: string[]                     // new
  blocked_reason: string

  // Metadata
  metadata: Record<string, unknown>        // new

  // Grouping
  sprint_id: string | null
  project_id: string | null
  epic_id: string | null

  // Audit
  created_at: string
  updated_at: string
}
```

Narrowing `on_done: string` → `on_done: OnDone` is a compile-time break for any existing call site passing a raw string. Grepping the current frontend, no call site reads or writes these fields (they weren't exposed by the old `taskJSON`), so this is a free narrowing.

### 7.3 API client updates — `apps/gui/src/lib/api.ts`

- Extend the type imports to include `Deliverable`, `DeliverableType`, `OnDone`, `OnFail`, `OnReview`, `OnDoneMerge`
- Existing `createTask` / `updateTask` signatures use `Partial<Omit<Task, 'tags'>> & { tags?: string[] }` — no signature change needed, the new fields are automatically available via `Partial<Task>`

## 8. Testing Strategy

TDD throughout. Write failing tests first, implement, verify pass, commit.

| Layer | File | Cases |
|---|---|---|
| Service — validation helper | `internal/service/task_validation_test.go` (new) | Each lifecycle enum: valid accept, invalid reject with clear message. `CostBudget`, `MaxRetries`, `MaxDurationMs`, `TokenBudget` bound checks (negative/zero where applicable). `Deliverables[].Type` validation for all 12 valid types + rejection of an unknown type. `DependsOn` existence: happy path with created tasks + rejection of unknown task ID. |
| Service — `Create` | extend `internal/service/task_test.go` | Full-field create: populate every field in `TaskCreateInput`, call `Create`, round-trip via `Get`, verify each JSON-blob field is correctly marshaled and re-serialized. Defaults for empty enum fields. Validation errors bubble up as `*ValidationError`. |
| Service — `Update` | extend `internal/service/task_test.go` | Partial update of each new field (one at a time). Enum validation runs on update. Omitted-vs-cleared semantics: nil pointer = no change, empty slice/map = explicit clear. |
| HTTP — `createTask` | extend `internal/httpserver/server_test.go` | Full-field create → 201 with complete `taskJSON` response. Malformed JSON → 400. Invalid enum (`on_done:"purge"`) → 422. Missing `title` → 400. Unknown `depends_on` ID → 422. Invalid `deliverables[].type` → 422. Negative `cost_budget` → 422. |
| HTTP — `updateTask` | extend `internal/httpserver/server_test.go` | Partial update of each new field type. Empty body `{}` → 200 with unchanged task. `status` field present → 400 with clear message. Unknown enum value → 422. Non-existent task ID → 500 on the refetch path (existing behavior; not ideal but not changed here). |
| HTTP — `taskJSON` read path | extend `internal/httpserver/server_test.go` | `GET /tasks/:id` for a task with all fields populated returns every field with correct shapes and non-nil empty defaults for unset optional fields. |
| Frontend types | manual | `cd apps/gui && npx tsc --noEmit` clean. `cd apps/gui && npm run lint` clean (no new errors; 11 pre-existing remain). |

## 9. File Inventory

**New files:**
- `internal/service/task_validation_test.go` — isolated tests for the shared `validateTaskWrites` helper
- `docs/superpowers/specs/2026-04-09-task-api-expansion-design.md` (this spec)

**Modified files:**
- `internal/service/task.go` — new `Deliverable` type, new `validDeliverableTypes` / `validOnDone` / `validOnFail` / `validOnReview` / `validOnDoneMerge` maps, new `validateTaskWrites` helper + `taskWriteFields` internal type, expand `TaskCreateInput` with 9 new fields, extend `Create` to populate them + run validation, extend `Update` to run validation via the same helper
- `internal/service/task_test.go` — round-trip create/update tests for all new fields
- `internal/httpserver/tasks.go` — new `TaskCreateRequest` + `TaskUpdateRequest` types, new `parseStringArray` / `parseStringMap` / `parseFreeMap` / `parseDeliverables` read helpers, new `nullJSONString` write helper, rewrite `createTask` + `updateTask` handlers to use typed request structs, expand `taskJSON` with 12 new fields, add status rejection in `updateTask`
- `internal/httpserver/server_test.go` — ~15 new handler tests (full-field create, per-field update, enum error cases, round-trip)
- `apps/gui/src/lib/types.ts` — add `OnDone`, `OnFail`, `OnReview`, `OnDoneMerge`, `DeliverableType`, `Deliverable`, expand `Task` interface, narrow existing enum fields
- `apps/gui/src/lib/api.ts` — add type imports (no signature changes)

## 10. Implementation Order

Single vertical slice, stepped internally:

1. **Service layer — shared validation helper + `Deliverable` type** — write `task_validation_test.go`, implement `validateTaskWrites` and the valid-value maps, add the `Deliverable` type. Green.
2. **Service layer — `TaskCreateInput` expansion + `Create`/`Update` wiring** — extend the input type, wire each new field into the Create flow and the store write, wire `validateTaskWrites` into both `Create` and `Update`, add round-trip tests. Green.
3. **HTTP layer — request types + parse helpers + handler rewrite** — add `TaskCreateRequest`, `TaskUpdateRequest`, the four parse helpers, `nullJSONString`, rewrite handlers, expand `taskJSON`, add handler tests. Green.
4. **Frontend — TS types + enum narrowing** — add new types and expand `Task`, narrow the enum fields, verify `tsc --noEmit` clean and lint count unchanged.
5. **Rebuild + smoke test** — `cerberus_rebuild clockwork-api`, round-trip a task with all fields populated via curl, verify the response, restart `clockwork-frontend` to verify TS still compiles.
6. **PR cycle** — branch review, address feedback, merge.

## 11. Risks and Open Questions

- **N+1 on `DependsOn` validation.** The validation helper calls `GetTask` for each dependency ID. For typical dependency counts (< 10) this is trivial. If a task somehow depends on hundreds of others, validation becomes a visible cost. Acceptable for now; a future batched lookup is a clean follow-up.
- **`json.Unmarshal` behavior with null values on pointer-to-slice fields.** Unmarshaling JSON `null` into `*[]string` leaves the pointer nil, identical to "field omitted". Documented in Section 6.3 as the intended convention. For the three nullable numeric fields (`cost_budget`, `max_duration_ms`, `token_budget`), sentinel values (Section 5.2a) cover the common "remove a previously-set cap" workflow by letting clients send `-1` explicitly. The remaining case — restoring a specific numeric field to SQL NULL so sprint defaults take over again — still requires task recreation, but is rare enough to defer.
- **`updateTask` refetch path maps missing task to 500.** The existing code path `task, err := s.svc.Task.Get(id)` after a successful update returns 500 if the task disappeared (e.g., another process deleted it between the update and the refetch). This is the existing behavior and not introduced by this project, but worth noting as a carried-forward oddity.
- **Deliverable type validation vs. `custom`.** The `custom` value in the validation set is intentional per the canonical spec — it's the plugin-extensibility escape hatch. Plugin-defined types are allowed to pass through as `custom` and carry a `Description` that names the actual type.
- **Permissions and Metadata shape are deliberately loose.** When a concrete consumer of either field emerges (e.g., a permission-override system or a metadata schema for specific plugin integrations), that consumer's spec can narrow the shape via a follow-up. Keeping them as `map[string]any` now unblocks Project 3 without forcing premature design.
- **Empty body update `{}`** is a valid no-op. Handler returns 200 with the unchanged task. Test case covers it.

## 12. Deferred Items

- **BLG-20260409-001** — narrowed to "wire `Task.Deliverables` into the scheduler/executor completion gate" (the API-surface concern is addressed by this project)
- **PATCH vs PUT harmonization** on the task update endpoint (known inconsistency with tag endpoints; captured elsewhere)
- **Typed `ErrTaskNotFound` sentinel** parallel to `ErrTagNotFound` — would improve the refetch-path error mapping in `updateTask`
- **Timestamp serialization inconsistency** between `taskJSON` (raw `time.Time`) and `tagJSON` (`.Format(time.RFC3339)`) — cosmetic
- **Tools / Permissions domain validation** — accept any strings/keys for now; narrow when consumers exist
- **Task list endpoint filters on new fields** — not scoped for Project 2

## 13. Success Criteria

- `go build ./...` clean
- `go vet ./...` clean
- `go test ./...` all 20 packages green
- `gofmt -l` on touched files empty
- `cd apps/gui && npx tsc --noEmit` clean
- `cd apps/gui && npm run lint` → no new errors beyond the pre-existing 11
- Round-trip smoke test: `POST /api/v1/tasks` with all 30 writable fields populated → response includes every field with the correct shape → `GET /api/v1/tasks/:id` returns the same → `PUT /api/v1/tasks/:id` with a partial update (e.g., `{"cost_budget": 5.0, "tools": ["bash"]}`) changes only the specified fields
- Enum validation rejects `{"on_done":"bogus"}` with 422 and a message naming the allowed set
- Status rejection: `PUT /api/v1/tasks/:id` with `{"status":"done"}` returns 400 with a message pointing to `/transition`
- Unknown `depends_on` task ID returns 422 with a message identifying the missing ID
- Unknown `deliverables[0].type` returns 422 with a message naming the allowed type set
- Sentinel value round-trip: `POST /api/v1/tasks` with `{"cost_budget": -1, "max_duration_ms": -1, "token_budget": -1}` persists and reads back as `-1` for all three; subsequent `PUT` with `{"cost_budget": 50.0}` changes only that field
- Sentinel value rejection: `{"max_duration_ms": 0}` returns 422 (zero duration is meaningless); `{"max_duration_ms": -2}` returns 422 (only -1 is the valid negative); `{"cost_budget": -5}` returns 422
- The expanded `Task` interface type-checks against the actual API response for a fully-populated task

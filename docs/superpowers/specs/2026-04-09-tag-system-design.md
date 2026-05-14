# Tag System — Design Spec

**Date:** 2026-04-09
**Project:** torque
**Status:** Draft — awaiting user review
**Author:** brainstormed with Claude

## 1. Context

Tags on tasks are currently stored as a JSON-encoded string in the `tasks.tags` column (e.g. `"[\"bug\",\"ui\"]"`). The frontend parses this string at render time via a `parseTags()` helper. This is a legacy shape that conflates two concerns: a task's set of labels, and the vocabulary of labels available system-wide.

This project promotes tags to a first-class object with a relational model, so tags can be renamed, recolored, described, and merged without touching every task that uses them. It also replaces the JSON-string contract with a structured array-of-objects shape in the API.

### Where this sits in the broader roadmap

This is the first of a three-project chain aimed at shipping a proper task detail/edit page:

1. **Tag system (this spec)** — data/service/HTTP/frontend-types for tags-as-object
2. **Task API expansion** — surface the canonical rich Task fields (`tools`, `files`, `depends_on`, `permissions`, `environment`, `metadata`, etc.) through HTTP and TypeScript; consumes the tag system built here
3. **Task detail/edit page** — rebuild the TaskDetailPage using the tasks home visual language with inline edit via query param, port the context-aware NavArrows from Fragments Engine

The UI side is deferred to Project 3, so this project is scoped to the data/API layer plus the minimum frontend changes needed to keep the existing tasks board working.

## 2. Goals

- Tags are a relational entity with CRUD, not a string blob
- Tags can be renamed, recolored, described, and merged without rewriting task rows
- Tag vocabulary is shared across tasks; typing `Bug` once creates `bug`, and every later reference uses the same tag
- The task API returns structured tag objects
- The frontend has a reusable `<TagChip>` component with a semantic-token color palette
- Clean cutover — no backwards compatibility for the old JSON-string format

## 3. Non-Goals

- Polymorphic taggables (sprints/projects/epics). Tags apply only to tasks in this project; generalization is a future refactor
- Tag management UI. Captured as backlog item BLG-20260409-002 (post-MVP, P3) — CRUD is via curl/API only for now
- Data migration from existing JSON-string tags. Pre-release project, no data worth preserving; migration is a clean create-tables-only step
- Usage counts / tag detail views showing tagged items. Part of the future management UI
- Tag groups, namespaces, or archival flags
- MCP tool surface for tags. HTTP only for now; MCP can wrap the service later if needed
- JSON:API envelope. Torque uses plain JSON throughout and this spec stays consistent with that

## 4. Data Model

### 4.1 Tables

Migration: `internal/persistence/sqlstore/migrations/005_tags.sql`

```sql
CREATE TABLE tags (
  slug        TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  color       TEXT NOT NULL DEFAULT 'zinc',
  created_at  TIMESTAMP NOT NULL,
  updated_at  TIMESTAMP NOT NULL,
  CHECK (color IN ('zinc','red','orange','amber','green','teal','blue','violet','pink'))
);

CREATE TABLE task_tags (
  task_id    TEXT NOT NULL,
  tag_slug   TEXT NOT NULL,
  sort_order INTEGER NOT NULL,         -- 0-indexed assignment order
  created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (task_id, tag_slug),
  FOREIGN KEY (task_id)  REFERENCES tasks(id) ON DELETE CASCADE,
  FOREIGN KEY (tag_slug) REFERENCES tags(slug) ON DELETE CASCADE
);
CREATE INDEX idx_task_tags_tag_slug ON task_tags(tag_slug);

-- Drop the legacy column in the same migration.
ALTER TABLE tasks DROP COLUMN tags;
```

SQLite 3.35+ supports `DROP COLUMN`. Torque already targets a modern SQLite (WAL + JSON1), so this is safe.

### 4.2 Slug rules

Enforced in the service layer:

- Lowercase alphanumeric plus hyphens, matching `^[a-z0-9]+(-[a-z0-9]+)*$`
- Length 1–48
- No leading, trailing, or consecutive hyphens
- Deterministic name→slug normalization via `strutil.Slugify()` (see Section 5)

### 4.3 Color palette

Nine values: `zinc` (default), `red`, `orange`, `amber`, `green`, `teal`, `blue`, `violet`, `pink`. Stored as text with a SQL `CHECK` constraint. The frontend maps each name to tailwind classes; the data layer never knows hex values.

### 4.4 Tag assignment ordering

Tag lists on task responses are ordered `sort_order ASC`. `SetTaskTags` assigns sequential indices (0, 1, 2, ...) in input order. `created_at` is stored for audit purposes but is not used for ordering because SQLite timestamp precision can collide on back-to-back inserts within the same transaction.

## 5. `strutil` — external dependency

The Go module `github.com/hollis-labs/go-strutil` lives at `~/Projects-apps/framework/utils/go-strutil` and provides string helpers modeled on Laravel's `Str` facade. The module is already built (separate effort) and ships `Slugify`, `SnakeCase`, `KebabCase`, `CamelCase`, `StudlyCase`, `Title`, `UcFirst`, `LcFirst`, `Truncate`, `Words`, `Limit`, `Squish`, `Finish`, `Start`, `After`, `Before`, `Between`, `ContainsAll`, `ContainsAny`, and `Random` — plus a bonus `SlugifyN(s, maxRunes)` variant.

Only `strutil.Slugify(s string) string` is consumed by this project.

Torque imports it via a local `replace` directive in `go.mod` during pre-release:

```go
// torque/go.mod
require github.com/hollis-labs/go-strutil v0.0.0-00010101000000-000000000000
replace github.com/hollis-labs/go-strutil => ../framework/utils/go-strutil
```

Publishing the strutil module to a remote is deferred to before public release.

## 6. Store Layer

File: `internal/persistence/sqlstore/tags.go`

```go
type TagRecord struct {
    Slug        string
    Name        string
    Description string
    Color       string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type TagUpdate struct {
    Name        *string
    Description *string
    Color       *string
}

// CRUD
func (s *Store) CreateTag(t *TagRecord) error
func (s *Store) GetTag(slug string) (*TagRecord, error)
func (s *Store) ListTags() ([]TagRecord, error)            // ORDER BY name ASC
func (s *Store) UpdateTag(slug string, u TagUpdate) error  // name/description/color only
func (s *Store) DeleteTag(slug string) error               // CASCADE via FK

// Linking
func (s *Store) SetTaskTags(taskID string, slugs []string) error   // replace-all in tx
func (s *Store) ListTaskTags(taskID string) ([]TagRecord, error)   // ORDER BY task_tags.sort_order ASC
func (s *Store) MergeTags(sourceSlug, destSlug string) error       // tx: dedupe, update, delete source
```

### 6.1 `SetTaskTags` semantics

Transactional replace-all. Called from task create and task update:

1. `DELETE FROM task_tags WHERE task_id = ?`
2. For `i, slug := range slugs`: `INSERT INTO task_tags (task_id, tag_slug, sort_order, created_at) VALUES (?, ?, ?, ?)` — `sort_order = i`, `created_at = time.Now().UTC()`
3. Commit

Idempotent. Preserves input order via explicit `sort_order` indices, independent of timestamp precision.

If `slugs` is empty, step 2 is a no-op and the task ends up with zero tags — this is intentional and not an error.

### 6.2 `MergeTags` semantics

Transactional, in this order:

1. `DELETE FROM task_tags WHERE tag_slug = source AND task_id IN (SELECT task_id FROM task_tags WHERE tag_slug = dest)` — drop rows where the task already has the destination tag
2. `UPDATE task_tags SET tag_slug = dest WHERE tag_slug = source` — move remaining rows
3. `DELETE FROM tags WHERE slug = source`
4. Commit

If `source == dest` or either doesn't exist, return an error before touching anything.

## 7. Service Layer

### 7.0 TagService — `internal/service/tag.go`

```go
type TagService struct {
    store *sqlstore.Store
}

type TagCreateInput struct {
    Name        string  // required
    Slug        string  // optional; derived from Name if empty
    Description string  // optional, max 500 chars
    Color       string  // optional; defaults to "zinc"
}

type TagUpdateInput = sqlstore.TagUpdate

func (s *TagService) List() ([]sqlstore.TagRecord, error)
func (s *TagService) Get(slug string) (*sqlstore.TagRecord, error)
func (s *TagService) Create(input TagCreateInput) (*sqlstore.TagRecord, error)
func (s *TagService) Update(slug string, u TagUpdateInput) (*sqlstore.TagRecord, error)
func (s *TagService) Delete(slug string) error
func (s *TagService) Merge(sourceSlug, destSlug string) error
func (s *TagService) ResolveNames(inputs []string) ([]string, error)
```

### 7.0.1 TaskService integration

`TaskService` gains a reference to `TagService` so it can call `ResolveNames` during task create/update:

```go
type TaskService struct {
    store   *sqlstore.Store
    feature *FeatureService
    tags    *TagService     // NEW
}
```

The `Service` container in `internal/service/service.go` is updated to construct `TagService` first, then pass it into the `TaskService` struct literal. The container also gains a `Tag *TagService` field so HTTP handlers can reach it.

A new service-level input type wraps the store-level `TaskUpdate` and adds a tags field, because the store-level `TaskUpdate` no longer carries tags (they live in `task_tags`, not `tasks`):

```go
// In internal/service/task.go
type TaskUpdateInput struct {
    sqlstore.TaskUpdate        // embedded — all existing pointer-fields for task columns
    Tags *[]string             // nil = no change; non-nil = replace all linked tags
}

func (s *TaskService) Update(id string, input TaskUpdateInput) error {
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

The `store.UpdateTask` and `store.SetTaskTags` calls run as independent statements, not a single transaction across methods. If `SetTaskTags` fails after `UpdateTask` succeeds, the task row is updated but tags are not — the caller gets a `500` and can retry. This is a deliberate tradeoff: adding cross-method transactions would require threading a `*sql.Tx` through the store API, which is a larger refactor and not worth it for a non-critical consistency window.

The same split applies to `TaskService.Create`: create the task row, then resolve and link tags in a second call. If tag linking fails, the task exists with zero tags — retry via update.

### 7.1 Validation rules

| Field | Rule |
|---|---|
| `name` | required; trimmed; 1–64 chars after trim |
| `slug` (explicit) | must match `^[a-z0-9]+(-[a-z0-9]+)*$`; 1–48 chars |
| `slug` (derived) | `strutil.Slugify(name)` must produce non-empty result |
| `color` | must be one of the nine palette values; empty defaults to `zinc` |
| `description` | optional; max 500 chars |
| Merge | source and dest must both exist; source ≠ dest |

Violations return `service.ValidationError{Field, Message}` using the existing error type.

### 7.2 `ResolveNames` — auto-create on write

Called by `TaskService.Create` and `TaskService.Update` when a task request includes `tags: []string`. The input is any mix of tag names or slugs:

1. For each input string, trim whitespace and call `strutil.Slugify` → candidate slug
2. Skip any input whose candidate slug is empty after normalization (silent drop, not a validation error)
3. Deduplicate remaining slugs while preserving first-occurrence order
4. For each distinct slug, `INSERT OR IGNORE INTO tags (slug, name, color, created_at, updated_at)` — first-seen name wins, color defaults to `zinc`
5. Return the deduplicated slug list in input order

Existing tags keep their existing display name, color, and description — only newly created tags adopt the raw input as their name.

Edge cases:
- Duplicate inputs (`["Bug", "bug"]`) collapse to one slug
- Whitespace-only and garbage inputs (`"  "`, `"!!!"`) are dropped silently
- If all inputs drop, the resolved list is empty (not an error) — the task ends up with zero tags
- Explicit tag creation via `POST /tags` with a name that slugifies to empty IS a validation error (Section 7.1) — the lenient path is only for the task-create-side auto-resolution

## 8. HTTP API

File: `internal/httpserver/tags.go`

### 8.1 Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET`    | `/api/v1/tags`               | List all tags |
| `POST`   | `/api/v1/tags`               | Create tag |
| `GET`    | `/api/v1/tags/:slug`         | Get one tag |
| `PATCH`  | `/api/v1/tags/:slug`         | Update name/description/color |
| `DELETE` | `/api/v1/tags/:slug`         | Delete (CASCADE removes task_tags) |
| `POST`   | `/api/v1/tags/:slug/merge`   | Merge `:slug` into `body.into` |

### 8.2 Request/response shapes

**Create request:**
```json
{
  "name": "Frontend Bug",
  "slug": "frontend-bug",
  "description": "UI issues in the React frontend",
  "color": "red"
}
```
`slug`, `description`, and `color` are optional.

**Single-tag response (all endpoints except list and delete):**
```json
{
  "slug": "frontend-bug",
  "name": "Frontend Bug",
  "description": "UI issues in the React frontend",
  "color": "red",
  "created_at": "2026-04-09T10:00:00Z",
  "updated_at": "2026-04-09T10:00:00Z"
}
```

**List response:**
```json
{ "tags": [ { ... }, { ... } ] }
```

**Delete response:** `204 No Content`.

**Merge request:**
```
POST /api/v1/tags/bug/merge
{ "into": "defect" }
```
Response: the destination tag record after merge.

**PATCH request:** any subset of `name`, `description`, `color`. Unknown fields ignored.

### 8.3 Error responses

Standard `{"error": "..."}` via the existing `writeError` helper.

| Status | Trigger |
|---|---|
| `400` | Invalid JSON body |
| `404` | Tag slug not found |
| `409` | Slug already exists on create; merge source and dest identical |
| `422` | Validation failure (bad color, bad slug format, name length, auto-create failed) |
| `500` | Unexpected storage error |

## 9. Task API Contract Change

### 9.1 Read — `taskJSON`

The `tags` field changes from a JSON-encoded string to an array of `Tag` objects. The `taskJSON` helper loads linked tags via `store.ListTaskTags(taskID)` and serializes them inline:

```json
{
  "id": "CW-20260409-0001",
  "title": "Fix the thing",
  "tags": [
    { "slug": "bug", "name": "Bug", "description": "", "color": "red",
      "created_at": "...", "updated_at": "..." },
    { "slug": "ui",  "name": "UI",  "description": "", "color": "blue",
      "created_at": "...", "updated_at": "..." }
  ]
}
```

Performance note: `listTasks` loads tags per task in the current implementation. For list views with many tasks this is N+1. Acceptable for now given task volumes; an `IN (...)` batch query is a trivial follow-up if the board ever feels slow.

### 9.2 Write — `createTask` / `updateTask`

Both handlers accept `"tags": []string` — names or slugs, any mix. The handler builds a service-level input and calls `TaskService.Create` or `TaskService.Update`. If `ResolveNames` fails with a `ValidationError`, the handler returns `422`. If `SetTaskTags` fails with a storage error, the handler returns `500`.

Layering changes:

- **Store layer** — `TaskRecord.Tags` (the `string` column field) is removed entirely. `sqlstore.TaskUpdate` loses its `Tags *string` field. `scanTask`, `taskSelectCols`, and the INSERT/UPDATE statements in `tasks.go` all drop the `tags` column.
- **Service layer** — `TaskCreateInput.Tags` changes from `[]string` (it's already `[]string`, but was written to the row as a JSON-marshalled string via `marshalJSON(input.Tags)`) to a value that `ResolveNames` consumes directly. A new `TaskUpdateInput` wraps `sqlstore.TaskUpdate` and adds `Tags *[]string` (see Section 7.0.1). `TaskService` takes a `*TagService` dependency and calls `ResolveNames` + `SetTaskTags` in sequence. The `Service` container in `service.go` gains a `Tag *TagService` field.
- **HTTP layer** — the handler bodies are updated to decode `tags` as a JSON array and build the appropriate service input.

The Go code that currently writes `Tags: marshalJSON(input.Tags)` at `internal/service/task.go:110` is removed. The new flow: create the task row, then call `SetTaskTags(id, resolvedSlugs)` as a separate store call.

### 9.3 Backwards compatibility

None. The `tags` field shape changes from string to `Tag[]`. No users, no production data, clean cutover. All consumers (frontend, CLI if any, MCP if any) are updated in the same PR.

## 10. Frontend Ripple

### 10.1 Types — `apps/gui/src/lib/types.ts`

```ts
export type TagColor =
  | 'zinc' | 'red' | 'orange' | 'amber'
  | 'green' | 'teal' | 'blue' | 'violet' | 'pink'

export interface Tag {
  slug: string
  name: string
  description: string
  color: TagColor
  created_at: string
  updated_at: string
}

export interface Task {
  // ... existing fields ...
  tags: Tag[]   // was: tags: string
}
```

### 10.2 Constants — `apps/gui/src/lib/constants.ts`

Add `TAG_COLOR_CLASSES: Record<TagColor, string>` mapping each palette name to tailwind classes consistent with the existing HUD aesthetic:

```ts
export const TAG_COLOR_CLASSES: Record<TagColor, string> = {
  zinc:   'bg-zinc-900 text-zinc-400',
  red:    'bg-red-950 text-red-400',
  orange: 'bg-orange-950 text-orange-400',
  amber:  'bg-amber-950 text-amber-400',
  green:  'bg-green-950 text-green-400',
  teal:   'bg-teal-950 text-teal-400',
  blue:   'bg-blue-950 text-blue-400',
  violet: 'bg-violet-950 text-violet-400',
  pink:   'bg-pink-950 text-pink-400',
}
```

### 10.3 Component — `apps/gui/src/components/domain/tag-chip.tsx`

```tsx
import type { Tag } from '@/lib/types'
import { TAG_COLOR_CLASSES } from '@/lib/constants'

interface TagChipProps {
  tag: Tag
  title?: string
}

export function TagChip({ tag, title }: TagChipProps) {
  const hoverText = title ?? (tag.description || tag.name)
  return (
    <span
      className={`rounded px-1.5 py-0.5 text-[10px] font-mono ${TAG_COLOR_CLASSES[tag.color]}`}
      title={hoverText}
    >
      {tag.name}
    </span>
  )
}
```

### 10.4 Board row — `apps/gui/src/components/domain/task-row.tsx`

- Remove the `parseTags` import and call
- Render `task.tags` directly with `<TagChip>`
- Cap display at 3 tags; render `<span>+{n} more</span>` with the overflow count if more than 3 are present
- The existing title + executor subtitle layout stays untouched

### 10.5 Task detail page — `apps/gui/src/pages/TaskDetailPage.tsx`

Current code already loops over `parseTags(task.tags)` and renders inline spans. Replace with `task.tags.map(tag => <TagChip key={tag.slug} tag={tag} />)`. No cap here — single views show the full set.

### 10.6 Utils — `apps/gui/src/lib/utils.ts`

Remove `parseTags()`. Any other references across the codebase are audited and updated in the same pass.

### 10.7 API client — `apps/gui/src/lib/api.ts`

Add tag methods:

```ts
async listTags(): Promise<{ tags: Tag[] }>
async getTag(slug: string): Promise<Tag>
async createTag(data: {
  name: string
  slug?: string
  description?: string
  color?: TagColor
}): Promise<Tag>
async updateTag(slug: string, data: {
  name?: string
  description?: string
  color?: TagColor
}): Promise<Tag>
async deleteTag(slug: string): Promise<void>
async mergeTags(sourceSlug: string, into: string): Promise<Tag>
```

Update the existing `createTask` / `updateTask` method signatures so `tags` is typed as `string[]` on the request side (names or slugs) — distinct from the `Tag[]` shape on the response side. In practice that means:

```ts
async createTask(data: Partial<Omit<Task, 'tags'>> & { tags?: string[] }): Promise<Task>
async updateTask(id: string, data: Partial<Omit<Task, 'tags'>> & { tags?: string[] }): Promise<Task>
```

## 11. Testing Strategy

TDD throughout: write failing tests first, implement, verify pass.

| Layer | File | Coverage |
|---|---|---|
| Store | `internal/persistence/sqlstore/tags_test.go` | CRUD happy paths; SetTaskTags replace semantics + idempotency; ListTaskTags ordering by created_at; MergeTags dedup + cleanup; FK CASCADE on tag delete |
| Service | `internal/service/tag_test.go` | Create validates name/slug/color/description; Create rejects name that slugifies to empty; auto-derive slug from name; Update partial; Delete; Merge rejects source==dest and missing slugs; ResolveNames auto-creates, dedups, preserves order, silently drops empty-after-slugify inputs, preserves first-seen display name |
| HTTP handlers | extend `internal/httpserver/server_test.go` | GET/POST/PATCH/DELETE `/tags`; POST `/tags/:slug/merge`; 400/404/409/422 error paths; task create/update with `tags: []string` in body; task response contains `Tag[]`; board and detail views render correctly (manual verification since there's no frontend test harness) |

No e2e frontend tests in this project — the user has captured the tag management GUI backlog with a note to add snapshot/e2e tests then. Manual verification via `cerberus restart torque-frontend` and eyeballing the board + detail view is sufficient for this pass.

## 12. Implementation Order

Single vertical slice, but stepped internally to keep each commit coherent:

1. **Migration + store layer** — write migration 005, `tags.go` with `TagRecord`/`TagUpdate` and all store methods, `tags_test.go` with table-driven tests. Run `go test ./internal/persistence/sqlstore/...` green before moving on.
2. **Service layer** — write `service/tag.go` with `TagService`, register it in `service/service.go`. Write `tag_test.go`. Green before moving on.
3. **`strutil` wiring** — add the `replace` directive to Torque's `go.mod` pointing at `~/Projects-apps/framework/utils/go-strutil`. Confirm `strutil.Slugify` imports cleanly. (The `strutil` module itself is built in parallel by a separate agent using `BOOT.md`.)
4. **Task service + task storage updates** — remove `Tags` from `TaskRecord` and `sqlstore.TaskUpdate`; update `tasks.go` `taskSelectCols`, `scanTask`, CREATE and UPDATE SQL to drop the column. In the service layer: update `TaskCreateInput.Tags` to `[]string`, introduce `service.TaskUpdateInput` (embeds `sqlstore.TaskUpdate`, adds `Tags *[]string`), add a `*TagService` field on `TaskService`, update `Services` container wiring in `service/service.go`, and change `TaskService.Create`/`Update` to call `ResolveNames` + `SetTaskTags`. Update existing `task_test.go` and `task_sprint_test.go` to use the new shape. Green before moving on.
5. **HTTP layer** — `httpserver/tags.go` with all endpoints, register routes in `server.go`, update `taskJSON` to load and serialize `Tag[]`, update `createTask`/`updateTask` to accept `tags: []string`. Extend `server_test.go`. Green before moving on.
6. **Frontend types + constants + TagChip** — add `Tag`, `TagColor`, `TAG_COLOR_CLASSES`, `<TagChip>` component. No consumers yet, just the building blocks.
7. **Board row + task detail + api client + utils cleanup** — swap `parseTags` for `<TagChip>`, cap board row at 3 tags, add API client methods, remove `parseTags`. Manual visual verification.
8. **Rebuild and restart services** — `cerberus rebuild torque-api`, `cerberus restart torque-frontend`, smoke-test by creating a task with `["bug", "ui"]` tags via the API and confirming it renders with the right color chips.

## 13. Risks and Open Questions

- **N+1 on list endpoints.** Loading tags per task in a loop is fine at current scales but will need a batch query (`IN (...)`) if task counts grow. Marked as a follow-up, not a blocker.
- **`go.mod` replace directive** is local-machine only. If CI is ever added before the strutil module is published, CI will break until the module is pushed. No current CI, so this is a paper cut.
- **Empty color default at DB level.** The migration sets `DEFAULT 'zinc'` and the CHECK constraint allows it, but the service layer should still validate on write to catch typos from API clients sending `"Zinc"` (capitalized) before the DB does.

## 14. Deferred Items

- **BLG-20260409-001** — Expose `Task.Deliverables` in the HTTP API (separate project — tangled with run/artifact pipeline)
- **BLG-20260409-002** — Tag management GUI (post-MVP, P3 — settings page, usage counts, merge, history of recurring overwrites to watch for)
- **Polymorphic taggables** — tasks only for now; sprints/projects/epics can be added later with a generalized `taggables` join table
- **MCP tools for tags** — HTTP only; wrap in MCP when there's a clear use case
- **JSON:API envelope for the Torque API** — no action; may revisit if API consistency becomes a priority
- **Tag usage counts on list endpoint** — part of the future GUI, not the MVP

## 15. Success Criteria

- Migration 005 applies cleanly on a fresh database
- `go test ./...` passes, including all new store/service/handler tests
- Creating a task via `POST /api/v1/tasks` with `"tags": ["Bug", "UI"]` succeeds and returns the task with a `Tag[]` containing both tags at their auto-created slugs
- `GET /api/v1/tags` returns both tags
- `POST /api/v1/tags/bug/merge` with `{"into": "defect"}` moves all tasks tagged `bug` to `defect`, deletes `bug`
- The tasks board renders tag chips with palette colors, capped at 3 per row with `+N more` overflow
- The task detail page renders all tags without a cap
- No `parseTags` calls remain anywhere in `apps/gui/`

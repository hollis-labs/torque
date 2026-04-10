# Session Summary — 2026-04-09

**Started with:** Entity pages and backend enrichment complete from the prior session (2026-04-08), task system using a JSON-string `tags` column on the tasks table, and a lean HTTP API exposing only ~22 of the 35 canonical task fields. Open work: rebuild the task detail/edit page with visual parity to the tasks home.

**Ended with:** Two complete vertical slices shipped (Projects 1 and 2), unblocking the third (the actual detail/edit page rebuild). Tag system is now first-class relational with a reusable frontend component. Task HTTP API surfaces every field in the canonical model with strict validation.

## Projects shipped this session

### Project 1 — Tag system (PR #1)

Promoted tags from a JSON-string column to a first-class relational entity.

| Area | Files | Key changes |
|---|---|---|
| Migration | `internal/persistence/sqlstore/migrations/005_tags.sql` | New `tags` table (slug PK, name, description, color CHECK, timestamps), `task_tags` join (composite PK, sort_order, FK CASCADE both sides), drops legacy `tasks.tags` column |
| Store layer | `internal/persistence/sqlstore/tags.go` | `TagRecord`, CRUD, `SetTaskTags`/`ListTaskTags` (transactional, sort_order ordered), `MergeTags` with dedupe, `CreateTagIfNotExists` (race-free), `ErrTagNotFound` sentinel |
| Service layer | `internal/service/tag.go` | `TagService` with palette + slug validation, `Merge`, `ResolveNames` (auto-create on write, race-free) |
| HTTP | `internal/httpserver/tags.go`, `tasks.go` | `/api/v1/tags` CRUD + merge endpoints, `taskJSON` returns structured `Tag[]`, create/update accept `tags: []string` |
| MCP | `internal/mcpadapter/task_tools.go` | `taskWithTags` helper restores tag inline output on single-task tools |
| Frontend | `apps/gui/src/components/domain/tag-chip.tsx`, `lib/types.ts`, `lib/constants.ts`, `task-row.tsx`, `TaskDetailPage.tsx` | Reusable `<TagChip>` component, board row 3-cap + `+N more` overflow, detail page uncapped, `parseTags` removed, color palette in `TAG_COLOR_CLASSES` |
| External dep | `github.com/hollis-labs/go-strutil` | New shared module providing `Slugify` + 18 other Laravel-style helpers, published to GitHub (initially developed alongside Clockwork in `~/Projects-apps/framework/utils/go-strutil` and wired via local `replace` during development; switched to the published version after Project 2 merge) |
| Worktree resolver | `internal/worktree/resolver.go` | Merge-resolution auto-tasks re-attach `merge-resolution` + `auto-generated` tags via `CreateTagIfNotExists` + `SetTaskTags` |

### Project 2 — Task API expansion (PR #2)

Surfaced the 12 missing canonical task fields through the HTTP API with strict validation.

| Area | Files | Key changes |
|---|---|---|
| Service layer foundation | `internal/service/task_validation.go` (new) | `Unlimited` sentinel constant (untyped), `Deliverable` type, valid-value maps for lifecycle enums + deliverable types, `taskWriteFields` projection struct, shared `validateTaskWrites` helper, `extractCreateFields`/`extractUpdateFields` projections |
| Service layer wiring | `internal/service/task.go` | `TaskCreateInput` expanded with 9 fields (Permissions, Environment, MaxDurationMs, TokenBudget, EscalationChain, QualityGates, Deliverables, BlockedReason, Metadata), `Create` and `Update` both call `validateTaskWrites`, lifecycle enum defaults applied via `orDefault` in record builder |
| HTTP layer | `internal/httpserver/tasks.go` | `TaskCreateRequest`/`TaskUpdateRequest` named typed structs replace inline anonymous + map-based parsing, 4 new parse helpers (`parseStringArray`/`parseStringMap`/`parseFreeMap`/`parseDeliverables`), `nullJSONString` write helper, `taskJSON` expanded with all 12 fields, `status` field rejection on update (400 → `/transition`) |
| Store sentinel | `internal/persistence/sqlstore/tasks.go` | New `ErrTaskNotFound` parallel to Project 1's `ErrTagNotFound`, `GetTask` wraps via `%w` |
| TypeScript | `apps/gui/src/lib/types.ts` | New `OnDone`/`OnFail`/`OnReview`/`OnDoneMerge` enum unions, `DeliverableType`, `Deliverable`, `UNLIMITED` const; `Task` interface expanded with 12 new fields; lifecycle fields narrowed from `string` to enum unions; JSDoc on the three sentinel-using numeric fields |

### Sentinel value semantics

For the three nullable numeric task fields (`cost_budget`, `max_duration_ms`, `token_budget`):

- `null` / omitted → use default (inherit from sprint/global)
- `-1` → explicit unlimited (override inheritance, no cap)
- `0` → explicit zero (cost_budget only; rejected for the other two)
- positive → specific value
- `< -1` → validation error

This resolves the common "remove a previously-set cap" workflow without needing custom three-state JSON unmarshaling. Documented in `docs/superpowers/specs/2026-04-09-task-api-expansion-design.md` Section 6.3.

### Test coverage added

- **Tag system:** ~38 store + service + HTTP tests covering CRUD, merge dedup, sort order, auto-create on write, sentinel behavior, race-free creation, FK CASCADE, error mapping (404 via sentinel, 409 on duplicate, 422 on validation)
- **Task API expansion:** ~30 service + HTTP tests covering enum acceptance/rejection per field, sentinel value persistence and rejection, deliverable type validation, depends_on existence (typed sentinel detection via `errors.As`), full-field round-trip (create then read), partial update, status rejection on PUT, empty enum rejection on update (added during the Copilot review fix pass), invalid JSON blob payload rejection
- **Migration tests:** backfilled `migrate_test.go` assertions for 005's new tables and the dropped `tasks.tags` column

## What's next

**Project 3 — Task detail/edit page rebuild.** With the tag system shipped (Project 1) and the task API surfacing every canonical field with validation (Project 2), the detail page now has:

- A clean read contract for every field (tags, deliverables, escalation chain, all of it)
- A clean write contract via `TaskCreateRequest`/`TaskUpdateRequest` with strict validation
- TypeScript enum unions for `on_done`/`on_fail`/`on_review`/`on_done_merge`
- A reusable `<TagChip>` component for the chip rendering
- Sentinel constants for the nullable numerics

**Goals (unbrainstormed):**

1. Rebuild the page to match the tasks home visual style (zinc-950 HUD aesthetic, mono labels, accent bars)
2. Inline edit via `?edit=1` query param — single page, two modes
3. Port the context-aware `NavArrows` from Fragments Engine — left/right navigation among siblings within a project/epic/sprint scope, auto-advance on delete/archive, works for Tasks/Sprints/Epics/Projects
4. Use semantic tokens, reusable components, props down / events up
5. Leverage the now-exposed canonical fields for a comprehensive edit form

**Reference materials already ported:**

- `docs/architecture/` — design philosophy, unified messaging, MCP registry, special agent patterns, task model v0.1, backlog model, loop runner (all from FE)
- `docs/superpowers/specs/2026-04-07-clockwork-manifold-design.md` — canonical Task record + dependency resolution rules
- `docs/superpowers/plans/2026-04-07-plan-7-gui.md` — original FE GUI plan with task detail layout

## Backlog items captured this session

| ID | Title | Priority | Status |
|---|---|---|---|
| BLG-20260409-001 | Expose `Task.Deliverables` in HTTP API | P2 | API surface complete via Project 2; narrowed to "wire into scheduler/executor completion gate" |
| BLG-20260409-002 | Tag management GUI for Clockwork Settings | P3 | Post-MVP — will need spec + e2e tests when shipped (note: previously overwritten ~3 times) |

## Known follow-ups (deferred, not blocking Project 3)

- **PUT vs PATCH harmonization** — task update uses PUT, tag endpoints use PATCH (pre-existing inconsistency)
- **Timestamp serialization** — `taskJSON` uses raw `time.Time`, `tagJSON` uses `.Format(time.RFC3339)` (cosmetic)
- **N+1 task-tag loading** in `tasksJSON` — acceptable at current scale, batched lookup is a clean future cleanup
- **`GOPRIVATE` setup** — all three hollis-labs modules (`go-strutil`, `plugin`, `go-queue`) are now consumed from their published GitHub repos with no local `replace` directives. Until the repos are indexed by `proxy.golang.org`/`sum.golang.org`, future `go get`/`go mod tidy` operations need `GOPRIVATE=github.com/hollis-labs/*` in the environment (set globally via `go env -w` in this dev env)
- **Task list endpoint filters on new fields** — current filter set is status/priority/sprint/project/epic/executor only

## Test health

All 20 backend Go packages green. `go vet` clean. `gofmt` clean on all touched files. `cd apps/gui && npx tsc --noEmit` clean. `cd apps/gui && npm run lint` shows 11 pre-existing errors (unchanged from start of session — they're shadcn/setState-in-effect patterns unrelated to this work).

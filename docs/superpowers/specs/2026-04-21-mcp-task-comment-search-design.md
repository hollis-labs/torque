# MCP Task Filter Expansion + Comment Search

**Date:** 2026-04-21
**Status:** Design complete, pending implementation plan
**Scope:** MCP adapter + backend (Go) — no frontend, no CLI, no public API additions (those are explicit follow-ups)
**Related:** `docs/superpowers/specs/2026-04-21-tasks-filter-bar-redesign-design.md` (which surfaced the backend `Search` filter consumed here), commit `d224f82` (backend `?search=` wiring)

---

## Summary

The MCP adapter's task tools are missing filter parity with the backend. `clockwork_task_list` doesn't expose `project_id`, `sprint_id`, `epic_id`, `tags`, `manual`, or `search` even though `sqlstore.TaskFilter` supports them all. `clockwork_task_search` accepts only a free-text `query`, which forces LLM agents to either use the wrong tool or accept unfiltered results. Separately, there is no way to search comments via MCP — only per-task listing.

This spec adds the missing filters to both task tools and introduces a new `clockwork_comment_search` tool.

## Goals

- Bring MCP task filter coverage to full parity with `sqlstore.TaskFilter`.
- Let MCP callers combine free-text search with faceted filters in a single request.
- Provide a first-class way to discover comments by content, optionally scoped to a task or author.

## Non-goals

- HTTP API or CLI additions for comment search — deferred. MCP reaches the service layer directly; no new HTTP route is needed. A `GET /comments/search` endpoint will be added later when the API/CLI work happens.
- Full-text search (FTS5), relevance scoring, or snippet highlighting — plain `LIKE` matching keeps parity with existing task search.
- Removing the now-redundant `/tasks/search` HTTP endpoint and `Store.SearchTasks` function — follow-up cleanup once CLI callers migrate.
- Comment edit, delete, or update tools — not requested.
- Pagination beyond `limit` — matches existing task-search ergonomics.

## Decisions locked

- **Unified backend path:** both `task_list` and `task_search` MCP handlers build a `sqlstore.TaskFilter` and call `svc.Task.List(filter)`. `task_search` becomes "list-with-required-query". The dedicated `/tasks/search` HTTP endpoint and `Store.SearchTasks` stay in place (CLI/HTTP still use them) but MCP stops calling them.
- **Semantic distinction preserved** via tool descriptions: `task_search` is for free-text discovery (guides the LLM to provide a query); `task_list` is for filter-first browsing (query is optional).
- **`manual` param accepts UI and HTTP vocabularies.** `both`/`auto`/`manual` (matches the filter bar) plus `true`/`false`/`1`/`0` (matches the existing HTTP handler switch). Normalized to `*bool` on `TaskFilter.Manual`.
- **`tags` is a JSON-encoded array** of tag slugs. AND-match semantics (task must have all listed tags), matching `TaskFilter.TagSlugs`.
- **`manual` on `task_list` and `task_search`** uses the string alias vocabulary; the MCP-tool boundary is where the translation happens.
- **Comment search filters:** `query` (required), `task_id` (optional), `author` (optional, exact match).
- **Comment response shape:** full `CommentRecord` items; no brief variant (comments are already small).
- **Ordering:** comment search returns `ORDER BY created_at DESC` — newest first, matches the common "what's latest" scan.

## Architecture

### Files modified or extended

```
internal/persistence/sqlstore/comments.go       # add CommentFilter struct + SearchComments method
internal/persistence/sqlstore/comments_test.go  # add TestSearchComments
internal/service/comment.go (if standalone; else the relevant service file) # add Search wrapper
internal/mcpadapter/comment_tools.go (if standalone; else wherever clockwork_comment_* tools register) # add handleCommentSearch + tool registration
internal/mcpadapter/<adapter-test>              # add full-stack test for comment search
internal/mcpadapter/task_tools.go               # add 6 filter params to task_list and task_search; both call svc.Task.List
internal/mcpadapter/task_tools_test.go          # extend TestFullStack_SearchTasks coverage
```

The implementation plan will resolve exact file locations — some of these may already exist as standalone, others may live inside larger aggregated files. No new HTTP routes, no backend service-layer surface beyond a single `Search` method on the comment service.

### Data flow (unchanged)

```
MCP client → mcp-go server → Adapter handler → svc.Task.List / svc.Comment.Search → sqlstore → SQLite
```

No new layers. The adapter is the thin translation boundary; everything below already exists or is a small mirror of existing code.

## `clockwork_task_list` and `clockwork_task_search` — new filter params

Both tools gain these optional parameters (`task_search` keeps `query` as required; it maps to `TaskFilter.Search`):

| Param | Type (MCP) | Maps to | Notes |
|---|---|---|---|
| `project_id` | string | `TaskFilter.ProjectID` | exact match |
| `sprint_id` | string | `TaskFilter.SprintID` | exact match |
| `epic_id` | string | `TaskFilter.EpicID` | exact match |
| `tags` | string (JSON array) | `TaskFilter.TagSlugs` | AND-match, same as `task_update.tags` |
| `manual` | string | `TaskFilter.Manual` (*bool) | accepts `both`/`auto`/`manual` (UI) plus `true`/`false`/`1`/`0` (HTTP); `both` or omission ⇒ `nil` (no filter) |
| `search` | string | `TaskFilter.Search` | substring match on title + description (`task_search` uses `query` here) |

Parsing the `manual` string lives in a small helper shared between `task_list` and `task_search`. Parsing `tags` reuses the existing JSON-array convention from `task_update`.

The `task_search` handler stops calling `svc.Task.Search` and `handleTaskSearch` instead constructs a `TaskFilter{ Search: query, ... }` and calls `svc.Task.List(filter)`.

### Tool-description updates

Both tools get updated descriptions reflecting the new params. `task_search`'s description adds: "Filters (project_id, sprint_id, epic_id, tags, manual) combine with the query via AND — use them to narrow free-text results."

## `clockwork_comment_search` — new tool

### MCP signature

```
clockwork_comment_search(
  query:   string (required) — substring match on comment.content
  task_id: string (optional) — restrict to one task
  author:  string (optional) — exact match
  limit:   string (optional, default 25, max 100) — cap results
)
→ { items: [CommentRecord...], meta: {truncated, returned, limit, hint?} }
```

### `sqlstore.CommentFilter`

```go
type CommentFilter struct {
    TaskID string
    Author string
    Search string // substring match on content
    Limit  int
}
```

### `Store.SearchComments`

Signature:
```go
func (s *Store) SearchComments(f CommentFilter) ([]CommentRecord, error)
```

Behavior:
- `Search` is required by the calling HTTP/MCP layer — the store treats empty as "no content filter" so it's composable, but the MCP tool requires `query` and rejects empty at the tool boundary (mirrors `task_search`'s `query` required).
- SQL built the same way as `ListTasks`: a `where` slice + args.
  - `content LIKE ?` with `%search%` when `Search != ""`
  - `task_id = ?` when `TaskID != ""`
  - `author = ?` when `Author != ""`
- `ORDER BY created_at DESC`
- `LIMIT ?` when `Limit > 0`

### `svc.Comment.Search`

Thin wrapper — validates nothing beyond what the store enforces. Business-layer surface kept consistent with `svc.Task.List`.

### MCP handler `handleCommentSearch`

Mirrors `handleTaskSearch`'s shape: clamp limit, build filter, call service, return envelope via a new `commentsToEnvelope` helper (or a generic envelope helper if one already fits — the implementation plan will check).

## Error handling

- Empty `query` on `task_search` or `comment_search` ⇒ structured MCP error (same pattern as existing required-param validation).
- Unknown `manual` value ⇒ silently ignored (same as HTTP handler today — "no filter").
- Invalid `tags` JSON ⇒ structured MCP error (matches `task_update.tags` behavior).
- DB errors propagate via `errFromService` (existing helper).

## Testing

### sqlstore

- `TestSearchComments` new cases:
  - content match
  - task_id filter
  - author filter
  - combined filters
  - no match returns empty slice
  - `Limit` truncates

### MCP adapter

- `TestFullStack_SearchTasks` gains cases for each new filter (`project_id`, `tags`, `manual`, combined with `query`).
- New `TestFullStack_CommentSearch`: create task + 3 comments (different authors, different content), call `clockwork_comment_search` with each filter, assert envelope and rows.

No frontend tests — the FE isn't touched.

## Migration / compatibility

- No DB migration needed.
- Existing MCP callers passing no new params see no behavior change.
- `task_search` behavior is preserved for callers who only pass `query` — same results, different code path (list with search filter). Verify via existing `TestFullStack_SearchTasks` coverage.

## Follow-up candidates (explicitly deferred)

- Expose comment search via HTTP (`GET /api/v1/comments/search`) and CLI (`clockwork comment search`) — the user deferred the API/CLI surface; this is when to add them. The MCP changes here leave the service-layer surface (`svc.Comment.Search`) ready for that next step.
- Remove `/tasks/search` HTTP endpoint and `Store.SearchTasks` once CLI callers migrate to `list?search=`.
- Add comment edit/delete MCP tools if needed.
- FTS5 / ranking for both task and comment search.
- Date-range filters (`created_after`, `created_before`) on comment search.

## Known limitations

- `LIKE %q%` has no FTS indexing. At current scale (hundreds of tasks, comment counts similar) this is fine. If either table grows to 10k+ rows, the `LIKE` scan becomes a latency concern worth addressing — but that's the same limitation the existing task search has today, so no regression.
- `author` is exact match, not a substring search. If agents want "fuzzy" author search they'll need to ask for the exact string. Matches the existing lookup conventions; upgrade with a follow-up if it becomes a friction point.

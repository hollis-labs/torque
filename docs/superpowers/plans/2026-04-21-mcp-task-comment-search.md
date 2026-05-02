# MCP Task Filter Expansion + Comment Search — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring MCP task filter coverage to full parity with `sqlstore.TaskFilter`, let agents combine free-text search with faceted filters in a single request, and introduce a new `clockwork_comment_search` tool backed by a new `Store.SearchComments` / `svc.Comment.Search` path.

**Scope:** MCP adapter + backend Go only. No frontend, no HTTP API additions, no CLI surface. See spec for deferred items.

**Reference spec:** `docs/superpowers/specs/2026-04-21-mcp-task-comment-search-design.md`

---

## Conventions

- **Commit message prefix:** `feat(mcp):` for new behavior, `fix(mcp):` for bug fixes, `test(mcp):` for test-only commits.
- **No back-compat shims.** Rename / extend cleanly. No deprecation wrappers.
- **No emojis** in code, commits, or documentation.
- **No `--no-verify`**, no hook bypass. Fix the underlying issue if hooks fail.
- **TDD discipline:** sqlstore test cases written before the `SearchComments` implementation; MCP full-stack test cases written before the handler.
- **Run tests from** `~/Projects-apps/clockwork-manifold` (repo root) via `go test ./...` (full suite) or targeted: `go test ./internal/persistence/sqlstore/...` and `go test ./internal/mcpadapter/...`.
- **Backend binary:** Cerberus manages the service. Rebuild via `cerberus rebuild clockwork-manifold` after changes; do **not** manually start/stop.

---

## Exact file map

| What changes | File | Change type |
|---|---|---|
| Add `CommentFilter` + `SearchComments` | `internal/persistence/sqlstore/comments.go` | extend |
| Add `TestSearchComments` cases | `internal/persistence/sqlstore/comments_test.go` | extend |
| Add `svc.Comment.Search` wrapper | `internal/service/comment.go` | extend |
| Wire 6 new filter params into `handleTaskList` | `internal/mcpadapter/task_tools.go` | extend |
| Redirect `handleTaskSearch` to use `svc.Task.List` | `internal/mcpadapter/task_tools.go` | refactor |
| Add `parseManuaFilter` helper | `internal/mcpadapter/task_tools.go` | add helper |
| Add `handleCommentSearch` + tool registration | `internal/mcpadapter/comment_tools.go` | extend |
| Extend `TestFullStack_SearchTasks` | `internal/mcpadapter/task_tools_test.go` | extend |
| Add `TestFullStack_CommentSearch` | `internal/mcpadapter/comment_tools_test.go` (new file or append to task_tools_test.go — see Step 7) | add |

> **Note on comment tool tests:** If `comment_tools_test.go` does not exist, create it using the same `setupAdapter` / `callTool` helpers from `task_tools_test.go`. If the helpers are unexported they live in the same package (`package mcpadapter_test`) — the new file naturally shares them.

---

## Steps

### Step 1 — sqlstore: add `CommentFilter` and `SearchComments`

- [ ] Open `internal/persistence/sqlstore/comments.go`.
- [ ] Add the `CommentFilter` struct after the existing `CommentRecord` struct:
  ```go
  type CommentFilter struct {
      TaskID string
      Author string
      Search string // substring match on content
      Limit  int
  }
  ```
- [ ] Add `SearchComments(f CommentFilter) ([]CommentRecord, error)` below `ListComments`.
  - Build a `where []string` + `args []interface{}` slice (same pattern as `ListTasks` in `tasks.go`).
  - Append `content LIKE ?` / `%search%` when `f.Search != ""`.
  - Append `task_id = ?` when `f.TaskID != ""`.
  - Append `author = ?` when `f.Author != ""`.
  - Full query: `SELECT id, task_id, author, content, created_at FROM comments [WHERE ...] ORDER BY created_at DESC [LIMIT ?]`
  - Apply `LIMIT ?` only when `f.Limit > 0`.
  - Return `nil, nil` (empty slice, no error) when no rows — same nil-slice convention as `ListComments`.
- [ ] Compile: `go build ./internal/persistence/sqlstore/...`

---

### Step 2 — sqlstore: write and pass `TestSearchComments`

- [ ] Open `internal/persistence/sqlstore/comments_test.go`.
- [ ] Add `TestSearchComments` covering all cases from the spec:
  - content match (at least two comments, search returns only the matching one)
  - `task_id` filter (two tasks, search scoped to one)
  - `author` filter (two different authors, exact match)
  - combined filters (query + task_id + author together)
  - no match returns empty (non-nil) slice
  - `Limit` truncates (3 comments, Limit=2, assert `len(result)==2`)
- [ ] Run: `go test ./internal/persistence/sqlstore/... -run TestSearchComments -v`
- [ ] All cases pass.

---

### Step 3 — service layer: add `svc.Comment.Search`

- [ ] Open `internal/service/comment.go`.
- [ ] Add `Search(f sqlstore.CommentFilter) ([]sqlstore.CommentRecord, error)` to `CommentService`:
  ```go
  func (s *CommentService) Search(f sqlstore.CommentFilter) ([]sqlstore.CommentRecord, error) {
      return s.store.SearchComments(f)
  }
  ```
- [ ] Compile: `go build ./internal/service/...`

---

### Step 4 — MCP adapter: add `parseManualFilter` helper

- [ ] Open `internal/mcpadapter/task_tools.go`.
- [ ] Add a package-level helper (unexported) after the imports or near the existing helpers:
  ```go
  // parseManualFilter maps the MCP manual param string to *bool for TaskFilter.Manual.
  // Accepts UI vocabulary (both/auto/manual) and HTTP vocabulary (true/false/1/0).
  // Unknown or empty value returns nil (no filter applied).
  func parseManualFilter(v string) *bool {
      switch strings.ToLower(v) {
      case "true", "1", "manual":
          t := true
          return &t
      case "false", "0", "auto":
          f := false
          return &f
      }
      return nil // "both", empty, or unrecognized → no filter
  }
  ```
- [ ] Ensure `strings` is imported (it likely already is — confirm).
- [ ] Compile: `go build ./internal/mcpadapter/...`

---

### Step 5 — MCP adapter: wire 6 new filters into `handleTaskList`

- [ ] In `handleTaskList` (around line 254), add the following after the existing filter wiring, before calling `svc.Task.List`:
  ```go
  if v := reqStr(req, "project_id"); v != "" {
      filter.ProjectID = v
  }
  if v := reqStr(req, "sprint_id"); v != "" {
      filter.SprintID = v
  }
  if v := reqStr(req, "epic_id"); v != "" {
      filter.EpicID = v
  }
  if raw := reqStr(req, "tags"); raw != "" {
      var slugs []string
      if err := json.Unmarshal([]byte(raw), &slugs); err != nil {
          return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
      }
      filter.TagSlugs = slugs
  }
  if v := reqStr(req, "manual"); v != "" {
      filter.Manual = parseManualFilter(v)
  }
  if v := reqStr(req, "search"); v != "" {
      filter.Search = v
  }
  ```
- [ ] Update the `clockwork_task_list` tool schema (the `mcp.Tool` definition for this handler) to declare the 6 new optional parameters with their descriptions, matching the spec table. Add them to the `inputSchema` properties and `required` (none of them are required).
  - `project_id`: "Filter by project ID; exact match."
  - `sprint_id`: "Filter by sprint ID; exact match."
  - `epic_id`: "Filter by epic ID; exact match."
  - `tags`: "JSON array of tag slugs; task must have ALL listed tags. e.g. `[\"backend\",\"p1\"]`"
  - `manual`: "Filter by manual flag. Accepts `both`/`auto`/`manual` (UI) or `true`/`false`/`1`/`0`. Omit or `both` for no filter."
  - `search`: "Substring match on title and description. Combines with other filters via AND."
- [ ] Update the tool description to note the new filters.
- [ ] Compile: `go build ./internal/mcpadapter/...`

---

### Step 6 — MCP adapter: redirect `handleTaskSearch` to `svc.Task.List`

- [ ] In `handleTaskSearch` (around line 518), replace the body:

  **Before:**
  ```go
  results, err := a.svc.Task.Search(reqStr(req, "query"))
  if err != nil { ... }
  // post-query limit slice
  ```

  **After:**
  ```go
  query := reqStr(req, "query")
  if query == "" {
      return errResult(ErrCodeArgInvalid, "query is required", "query")
  }
  limit := clampLimit(reqStr(req, "limit"), defaultSearchLimit, maxSearchLimit)
  filter := sqlstore.TaskFilter{
      Search:    query,
      Limit:     limit,
  }
  if v := reqStr(req, "project_id"); v != "" {
      filter.ProjectID = v
  }
  if v := reqStr(req, "sprint_id"); v != "" {
      filter.SprintID = v
  }
  if v := reqStr(req, "epic_id"); v != "" {
      filter.EpicID = v
  }
  if raw := reqStr(req, "tags"); raw != "" {
      var slugs []string
      if err := json.Unmarshal([]byte(raw), &slugs); err != nil {
          return errResult(ErrCodeArgInvalid, fmt.Sprintf("invalid tags JSON: %v", err), "tags")
      }
      filter.TagSlugs = slugs
  }
  if v := reqStr(req, "manual"); v != "" {
      filter.Manual = parseManualFilter(v)
  }
  tasks, err := a.svc.Task.List(filter)
  if err != nil {
      return errFromService(err)
  }
  ```
  Then build the envelope from `tasks` the same way `handleTaskList` does (reuse the existing response helper; adapt if the old `Search` path did it differently).

  > **Check first:** Confirm whether `svc.Task.List` and `svc.Task.Search` return the same shape (`[]sqlstore.TaskRecord`). If so, swap is mechanical. The limit is now server-side (pushed into the filter) rather than a Go slice post-query.

- [ ] Update the `clockwork_task_search` tool schema to declare `project_id`, `sprint_id`, `epic_id`, `tags`, and `manual` as optional parameters (same descriptions as `task_list`). Keep `query` as required.
- [ ] Update description: append "Filters (project_id, sprint_id, epic_id, tags, manual) combine with the query via AND — use them to narrow free-text results."
- [ ] Compile: `go build ./internal/mcpadapter/...`

---

### Step 7 — MCP adapter: add `handleCommentSearch` + tool registration

- [ ] Open `internal/mcpadapter/comment_tools.go`.
- [ ] Add the new tool registration in the `registerCommentTools` (or equivalent) function:
  ```go
  server.AddTool(mcp.Tool{
      Name: "clockwork_comment_search",
      Description: "Search comments by content, optionally scoped to a task or author. Returns full CommentRecord items newest first.",
      InputSchema: mcp.ToolInputSchema{
          Type: "object",
          Properties: map[string]interface{}{
              "query":   map[string]interface{}{"type": "string", "description": "Required. Substring match on comment content."},
              "task_id": map[string]interface{}{"type": "string", "description": "Restrict results to one task."},
              "author":  map[string]interface{}{"type": "string", "description": "Exact match on comment author slug/id."},
              "limit":   map[string]interface{}{"type": "string", "description": "Max results (default 25, max 100)."},
          },
          Required: []string{"query"},
      },
  }, a.handleCommentSearch)
  ```
- [ ] Add `handleCommentSearch` method on `Adapter`:
  ```go
  func (a *Adapter) handleCommentSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
      query := reqStr(req, "query")
      if query == "" {
          return errResult(ErrCodeArgInvalid, "query is required", "query")
      }
      limit := clampLimit(reqStr(req, "limit"), 25, 100)
      f := sqlstore.CommentFilter{
          Search: query,
          Limit:  limit,
      }
      if v := reqStr(req, "task_id"); v != "" {
          f.TaskID = v
      }
      if v := reqStr(req, "author"); v != "" {
          f.Author = v
      }
      items, err := a.svc.Comment.Search(f)
      if err != nil {
          return errFromService(err)
      }
      // Build envelope. Check whether a generic list envelope helper exists;
      // if so, use it. Otherwise mirror the pattern from handleCommentList.
      truncated := len(items) == limit
      return cappedJSONResult(map[string]interface{}{
          "items": items,
          "meta": map[string]interface{}{
              "returned":  len(items),
              "limit":     limit,
              "truncated": truncated,
          },
      })
  }
  ```
  > Adjust the envelope construction to match the exact helper signatures used by other list tools in this file.

- [ ] Compile: `go build ./internal/mcpadapter/...`

---

### Step 8 — Tests: extend `TestFullStack_SearchTasks`

- [ ] Open `internal/mcpadapter/task_tools_test.go`.
- [ ] Extend `TestFullStack_SearchTasks` (line 413) or add sibling sub-tests:
  - Filter by `project_id`: create two tasks in different projects; assert only the right one is returned.
  - Filter by `tags`: create two tasks, one tagged `backend`, one tagged `frontend`; search with `tags='["backend"]'`; assert only the backend task is returned.
  - Filter by `manual="manual"`: create one manual and one auto task; assert correct filtering.
  - Filter `query` + `project_id` combined (AND semantics): ensure only the task matching both is returned.
  - `task_search` with combined `query` + `sprint_id`: same AND-semantics check.
  - Empty `query` on `task_search` returns error with code `arg_invalid` and field `query`.
- [ ] Run: `go test ./internal/mcpadapter/... -run TestFullStack_SearchTasks -v`
- [ ] All new cases pass.

---

### Step 9 — Tests: add `TestFullStack_CommentSearch`

- [ ] Create `internal/mcpadapter/comment_tools_test.go` (or append to `task_tools_test.go` — whichever keeps the existing file < ~600 lines; prefer a new file for clarity).
- [ ] Add `TestFullStack_CommentSearch`:
  ```
  Setup: create 2 tasks; add 3 comments total (different authors, different content words):
    - task1 / author="alice" / content="deploy pipeline configuration"
    - task1 / author="bob"   / content="rollback the deploy step"
    - task2 / author="alice" / content="unit test coverage report"

  Sub-tests:
    1. query="deploy"        → 2 results (both task1 comments)
    2. query="deploy" + task_id=task2 → 0 results
    3. query="coverage"      → 1 result (task2 comment)
    4. query="alice" (not in content) → 0 results (author is NOT searched, only content)
    5. query="pipeline" + author="alice" → 1 result
    6. query="deploy" + limit="1" → 1 result, truncated=true
    7. Empty query → error code=arg_invalid, field=query
  ```
- [ ] Run: `go test ./internal/mcpadapter/... -run TestFullStack_CommentSearch -v`
- [ ] All cases pass.

---

### Step 10 — Full suite green + rebuild

- [ ] Run full suite: `go test ./...`
- [ ] Fix any regressions (none expected if existing `TestFullStack_SearchTasks` passes via the new `svc.Task.List` path).
- [ ] Rebuild the service via Cerberus: `cerberus rebuild clockwork-manifold` (or the configured service ID).
- [ ] Smoke test via MCP client or `curl` against the running instance:
  - `clockwork_task_list` with `project_id` → filtered results.
  - `clockwork_task_search` with `query` + `tags` → AND-filtered results.
  - `clockwork_comment_search` with `query` → results in newest-first order.
  - `clockwork_comment_search` with empty `query` → structured error.

---

## Acceptance criteria

- [ ] `clockwork_task_list` accepts and correctly applies `project_id`, `sprint_id`, `epic_id`, `tags`, `manual`, and `search` params.
- [ ] `clockwork_task_search` accepts the same 5 optional filters (not `search` — it uses `query`) and calls `svc.Task.List` internally.
- [ ] `clockwork_comment_search` exists, requires `query`, supports `task_id` / `author` / `limit`, returns `ORDER BY created_at DESC`.
- [ ] `TestSearchComments` covers all 6 spec cases and passes.
- [ ] `TestFullStack_SearchTasks` covers all new filter combinations and passes.
- [ ] `TestFullStack_CommentSearch` covers all 7 spec cases and passes.
- [ ] `go test ./...` passes clean.
- [ ] No new HTTP routes added. No frontend changes.
- [ ] Existing MCP callers with no new params see identical behavior.

---

## Deferred (do not implement now)

- HTTP `GET /api/v1/comments/search` endpoint and CLI `clockwork comment search`.
- Remove `/tasks/search` HTTP endpoint and `Store.SearchTasks` once CLI migrates.
- Comment edit/delete MCP tools.
- FTS5 / relevance scoring.
- Date-range filters on comment search.
- Pagination beyond `limit` (offset/cursor).

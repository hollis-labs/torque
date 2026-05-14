# Tag System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote tags from a JSON-encoded string column into a first-class relational entity with CRUD, merge, auto-create-on-write semantics, and a semantic-token color palette, exposed through a new HTTP surface and consumed by the existing Torque React GUI.

**Architecture:** New `tags` table + `task_tags` join table (tasks-only, not polymorphic). Store methods for CRUD and linking. Service layer with validation and auto-resolve. HTTP handlers at `/api/v1/tags`. Task API changes so `tags` returns `Tag[]` and accepts `[]string` on write. Frontend introduces a reusable `<TagChip>` component. The slug helper comes from an external module `github.com/hollis-labs/go-strutil` wired via a local `replace` directive.

**Tech Stack:** Go 1.26, `modernc.org/sqlite`, `stretchr/testify`, React 19, TypeScript, Tailwind CSS 4, shadcn/ui, `golang.org/x/text` (indirectly via go-strutil).

**Reference:** `docs/superpowers/specs/2026-04-09-tag-system-design.md` — read this first if any task is unclear.

---

## File Structure

### New files

| Path | Responsibility |
|---|---|
| `internal/persistence/sqlstore/migrations/005_tags.sql` | Schema migration: create tags + task_tags, drop legacy `tasks.tags` column |
| `internal/persistence/sqlstore/tags.go` | `TagRecord`, `TagUpdate`, all store methods for tags and task_tags linking |
| `internal/persistence/sqlstore/tags_test.go` | Table-driven store tests |
| `internal/service/tag.go` | `TagService`, `TagCreateInput`, `TagUpdateInput`, validation, merge, `ResolveNames` |
| `internal/service/tag_test.go` | Service tests |
| `internal/httpserver/tags.go` | HTTP handlers for `/api/v1/tags` CRUD + merge |
| `apps/gui/src/components/domain/tag-chip.tsx` | Reusable tag chip component with color palette |

### Modified files

| Path | Changes |
|---|---|
| `go.mod`, `go.sum` | Add `github.com/hollis-labs/go-strutil` require + local `replace` directive |
| `internal/persistence/sqlstore/tasks.go` | Remove `Tags` field from `TaskRecord` and `TaskUpdate`; update `taskSelectCols`, `scanTask`, CREATE/UPDATE SQL |
| `internal/persistence/sqlstore/tasks_test.go` | Remove all references to `TaskRecord.Tags` string field |
| `internal/service/task.go` | Replace `marshalJSON(input.Tags)` write path with `ResolveNames` + `SetTaskTags`; introduce `TaskUpdateInput`; add `*TagService` field on `TaskService` |
| `internal/service/task_test.go`, `task_sprint_test.go` | Update to new task shape |
| `internal/service/service.go` | Add `Tag *TagService` field to `Service` container; wire TagService before TaskService |
| `internal/httpserver/server.go` | Register tag routes |
| `internal/httpserver/tasks.go` | `taskJSON` loads `Tag[]` via store; `createTask`/`updateTask` accept `"tags": []string` |
| `internal/httpserver/server_test.go` | Tag handler tests + updated task tests |
| `apps/gui/src/lib/types.ts` | Add `Tag`, `TagColor`; change `Task.tags` to `Tag[]` |
| `apps/gui/src/lib/constants.ts` | Add `TAG_COLOR_CLASSES` map |
| `apps/gui/src/lib/utils.ts` | Delete `parseTags` |
| `apps/gui/src/lib/api.ts` | Add tag methods; update task create/update signatures |
| `apps/gui/src/components/domain/task-row.tsx` | Use `<TagChip>` with 3-cap + `+N more` overflow |
| `apps/gui/src/pages/TaskDetailPage.tsx` | Use `<TagChip>`, no cap |

---

## Task 1: Wire the strutil dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum` (via `go mod tidy`)
- Create: `internal/strutil_smoke_test.go` (temporary smoke test, deleted in step 5)

- [ ] **Step 1: Add require and replace directives to `go.mod`**

Open `go.mod` and add a new `require` block (if there's an existing `require` block, add the line inside it) plus a `replace` directive. The file will look like:

```go
module github.com/hollis-labs/torque

go 1.26.1

require (
    // ... existing requires ...
    github.com/hollis-labs/go-strutil v0.0.0-00010101000000-000000000000
)

replace github.com/hollis-labs/go-strutil => ../framework/utils/go-strutil
```

The relative path `../framework/utils/go-strutil` is correct because Torque lives at `~/Projects-apps/torque` and the strutil module lives at `~/Projects-apps/framework/utils/go-strutil`.

- [ ] **Step 2: Run `go mod tidy`**

Run: `go mod tidy`
Expected: no errors; `go.sum` is updated; `go.mod` shows the new require.

- [ ] **Step 3: Create a temporary smoke test**

Create `internal/strutil_smoke_test.go`:

```go
package internal

import (
    "testing"

    "github.com/hollis-labs/go-strutil"
)

func TestStrutilImports(t *testing.T) {
    got := strutil.Slugify("Hello World")
    if got != "hello-world" {
        t.Errorf("strutil.Slugify(\"Hello World\") = %q, want \"hello-world\"", got)
    }
}
```

- [ ] **Step 4: Run the smoke test**

Run: `go test ./internal/ -run TestStrutilImports -v`
Expected: PASS.

- [ ] **Step 5: Delete the smoke test file**

```bash
rm internal/strutil_smoke_test.go
```

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(go.mod): wire github.com/hollis-labs/go-strutil via local replace"
```

---

## Task 2: Migration 005 — tags and task_tags schema

**Files:**
- Create: `internal/persistence/sqlstore/migrations/005_tags.sql`

- [ ] **Step 1: Write the migration SQL**

Create `internal/persistence/sqlstore/migrations/005_tags.sql` with this content:

```sql
-- 005_tags.sql
-- Promote tags to a first-class relational entity.
-- Drops the legacy tasks.tags JSON-string column.

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
  sort_order INTEGER NOT NULL,
  created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (task_id, tag_slug),
  FOREIGN KEY (task_id)  REFERENCES tasks(id) ON DELETE CASCADE,
  FOREIGN KEY (tag_slug) REFERENCES tags(slug) ON DELETE CASCADE
);

CREATE INDEX idx_task_tags_tag_slug ON task_tags(tag_slug);

-- Drop the legacy column. SQLite 3.35+ supports this.
ALTER TABLE tasks DROP COLUMN tags;
```

- [ ] **Step 2: Run the existing migration test to verify schema applies cleanly**

Run: `go test ./internal/persistence/sqlstore/migrations/... -v`
Expected: PASS. If the migration test is `migrate_test.go` (verified during exploration), it runs all migrations and confirms no errors.

If a migration test failure appears because the test refers to `tasks.tags`, skip that specific assertion — it's being removed in Task 10. If there's no such reference, you should see only PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/persistence/sqlstore/migrations/005_tags.sql
git commit -m "feat(db): add migration 005 for tags and task_tags tables"
```

---

## Task 3: Store layer — TagRecord type and basic CRUD

**Files:**
- Create: `internal/persistence/sqlstore/tags.go`
- Create: `internal/persistence/sqlstore/tags_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/persistence/sqlstore/tags_test.go`:

```go
package sqlstore_test

import (
    "testing"
    "time"

    "github.com/hollis-labs/torque/internal/persistence/sqlstore"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func sampleTag(slug string) *sqlstore.TagRecord {
    return &sqlstore.TagRecord{
        Slug:        slug,
        Name:        slug,
        Description: "",
        Color:       "zinc",
    }
}

func TestCreateAndGetTag(t *testing.T) {
    store := setupTestStore(t)

    tag := sampleTag("bug")
    tag.Name = "Bug"
    tag.Description = "Something is broken"
    tag.Color = "red"

    require.NoError(t, store.CreateTag(tag))

    got, err := store.GetTag("bug")
    require.NoError(t, err)
    assert.Equal(t, "bug", got.Slug)
    assert.Equal(t, "Bug", got.Name)
    assert.Equal(t, "Something is broken", got.Description)
    assert.Equal(t, "red", got.Color)
    assert.False(t, got.CreatedAt.IsZero())
    assert.False(t, got.UpdatedAt.IsZero())
}

func TestGetTagNotFound(t *testing.T) {
    store := setupTestStore(t)

    _, err := store.GetTag("nonexistent")
    assert.Error(t, err)
}

func TestListTagsEmpty(t *testing.T) {
    store := setupTestStore(t)

    tags, err := store.ListTags()
    require.NoError(t, err)
    assert.Empty(t, tags)
}

func TestListTagsSortedByName(t *testing.T) {
    store := setupTestStore(t)

    require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "ui", Name: "UI", Color: "zinc"}))
    require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "bug", Name: "Bug", Color: "red"}))
    require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "frontend", Name: "Frontend", Color: "blue"}))

    tags, err := store.ListTags()
    require.NoError(t, err)
    require.Len(t, tags, 3)
    assert.Equal(t, "Bug", tags[0].Name)
    assert.Equal(t, "Frontend", tags[1].Name)
    assert.Equal(t, "UI", tags[2].Name)

    // CreatedAt/UpdatedAt are populated
    assert.WithinDuration(t, time.Now(), tags[0].CreatedAt, 5*time.Second)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/persistence/sqlstore/... -run 'TestCreateAndGetTag|TestGetTagNotFound|TestListTags' -v`
Expected: compile errors — `sqlstore.TagRecord`, `store.CreateTag`, `store.GetTag`, `store.ListTags` don't exist yet.

- [ ] **Step 3: Create `tags.go` with `TagRecord`, `TagUpdate`, and basic CRUD**

Create `internal/persistence/sqlstore/tags.go`:

```go
package sqlstore

import (
    "database/sql"
    "fmt"
    "strings"
    "time"
)

// TagRecord mirrors the tags table row.
type TagRecord struct {
    Slug        string
    Name        string
    Description string
    Color       string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// TagUpdate holds optional fields to update; nil pointer = no change.
type TagUpdate struct {
    Name        *string
    Description *string
    Color       *string
}

const tagSelectCols = `slug, name, description, color, created_at, updated_at`

// scanTag scans a single row into a TagRecord.
func scanTag(row interface {
    Scan(...any) error
}) (*TagRecord, error) {
    var t TagRecord
    err := row.Scan(&t.Slug, &t.Name, &t.Description, &t.Color, &t.CreatedAt, &t.UpdatedAt)
    if err != nil {
        return nil, err
    }
    return &t, nil
}

// CreateTag inserts a new tag row.
func (s *Store) CreateTag(t *TagRecord) error {
    now := time.Now().UTC()
    t.CreatedAt = now
    t.UpdatedAt = now

    if t.Color == "" {
        t.Color = "zinc"
    }

    _, err := s.db.Exec(
        `INSERT INTO tags (slug, name, description, color, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?)`,
        t.Slug, t.Name, t.Description, t.Color, t.CreatedAt, t.UpdatedAt,
    )
    return err
}

// GetTag fetches a single tag by slug.
func (s *Store) GetTag(slug string) (*TagRecord, error) {
    row := s.db.QueryRow(`SELECT `+tagSelectCols+` FROM tags WHERE slug = ?`, slug)
    t, err := scanTag(row)
    if err == sql.ErrNoRows {
        return nil, fmt.Errorf("tag %s not found", slug)
    }
    return t, err
}

// ListTags returns all tags ordered by name ASC (case-insensitive).
func (s *Store) ListTags() ([]TagRecord, error) {
    rows, err := s.db.Query(`SELECT ` + tagSelectCols + ` FROM tags ORDER BY name COLLATE NOCASE ASC`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var tags []TagRecord
    for rows.Next() {
        t, err := scanTag(rows)
        if err != nil {
            return nil, err
        }
        tags = append(tags, *t)
    }
    return tags, rows.Err()
}

// UpdateTag applies a partial update.
func (s *Store) UpdateTag(slug string, u TagUpdate) error {
    var sets []string
    var args []any

    if u.Name != nil {
        sets = append(sets, "name = ?")
        args = append(args, *u.Name)
    }
    if u.Description != nil {
        sets = append(sets, "description = ?")
        args = append(args, *u.Description)
    }
    if u.Color != nil {
        sets = append(sets, "color = ?")
        args = append(args, *u.Color)
    }

    if len(sets) == 0 {
        return nil
    }

    sets = append(sets, "updated_at = ?")
    args = append(args, time.Now().UTC())
    args = append(args, slug)

    q := `UPDATE tags SET ` + strings.Join(sets, ", ") + ` WHERE slug = ?`
    res, err := s.db.Exec(q, args...)
    if err != nil {
        return err
    }
    n, err := res.RowsAffected()
    if err != nil {
        return err
    }
    if n == 0 {
        return fmt.Errorf("tag %s not found", slug)
    }
    return nil
}

// DeleteTag removes a tag by slug. CASCADE via FK removes task_tags rows.
func (s *Store) DeleteTag(slug string) error {
    res, err := s.db.Exec(`DELETE FROM tags WHERE slug = ?`, slug)
    if err != nil {
        return err
    }
    n, err := res.RowsAffected()
    if err != nil {
        return err
    }
    if n == 0 {
        return fmt.Errorf("tag %s not found", slug)
    }
    return nil
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./internal/persistence/sqlstore/... -run 'TestCreateAndGetTag|TestGetTagNotFound|TestListTags' -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/sqlstore/tags.go internal/persistence/sqlstore/tags_test.go
git commit -m "feat(store): add TagRecord and CRUD (Create/Get/List)"
```

---

## Task 4: Store layer — UpdateTag and DeleteTag

**Files:**
- Modify: `internal/persistence/sqlstore/tags_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/persistence/sqlstore/tags_test.go`:

```go
func TestUpdateTag(t *testing.T) {
    store := setupTestStore(t)

    require.NoError(t, store.CreateTag(&sqlstore.TagRecord{
        Slug: "bug", Name: "Bug", Description: "orig", Color: "zinc",
    }))

    newName := "Bug Report"
    newColor := "red"
    require.NoError(t, store.UpdateTag("bug", sqlstore.TagUpdate{
        Name:  &newName,
        Color: &newColor,
    }))

    got, err := store.GetTag("bug")
    require.NoError(t, err)
    assert.Equal(t, "Bug Report", got.Name)
    assert.Equal(t, "red", got.Color)
    assert.Equal(t, "orig", got.Description) // unchanged
    assert.True(t, got.UpdatedAt.After(got.CreatedAt) || got.UpdatedAt.Equal(got.CreatedAt))
}

func TestUpdateTagNotFound(t *testing.T) {
    store := setupTestStore(t)

    newName := "x"
    err := store.UpdateTag("nope", sqlstore.TagUpdate{Name: &newName})
    assert.Error(t, err)
}

func TestUpdateTagNoChanges(t *testing.T) {
    store := setupTestStore(t)
    require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "bug", Name: "Bug", Color: "zinc"}))

    // Empty update should be a no-op, not an error
    err := store.UpdateTag("bug", sqlstore.TagUpdate{})
    assert.NoError(t, err)
}

func TestDeleteTag(t *testing.T) {
    store := setupTestStore(t)
    require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "bug", Name: "Bug", Color: "zinc"}))

    require.NoError(t, store.DeleteTag("bug"))

    _, err := store.GetTag("bug")
    assert.Error(t, err)
}

func TestDeleteTagNotFound(t *testing.T) {
    store := setupTestStore(t)

    err := store.DeleteTag("nope")
    assert.Error(t, err)
}
```

- [ ] **Step 2: Run the new tests and verify they pass**

The implementations already exist from Task 3. Run:
`go test ./internal/persistence/sqlstore/... -run 'TestUpdateTag|TestDeleteTag' -v`
Expected: PASS (5 tests).

- [ ] **Step 3: Commit**

```bash
git add internal/persistence/sqlstore/tags_test.go
git commit -m "test(store): add tag update and delete tests"
```

---

## Task 5: Store layer — SetTaskTags and ListTaskTags

**Files:**
- Modify: `internal/persistence/sqlstore/tags.go`
- Modify: `internal/persistence/sqlstore/tags_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/persistence/sqlstore/tags_test.go`:

```go
// createTaskAndTags is a test helper that creates a task row and the referenced tags.
// We create a minimal task via the existing sampleTask() helper from tasks_test.go.
// Tags are created via store.CreateTag.
func seedTaskAndTags(t *testing.T, store *sqlstore.Store, taskID string, tagSlugs ...string) {
    t.Helper()
    // Create the task row via the existing sampleTask helper + CreateTask
    task := sampleTask(taskID)
    require.NoError(t, store.CreateTask(task))
    for _, slug := range tagSlugs {
        require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: slug, Name: slug, Color: "zinc"}))
    }
}

func TestSetTaskTagsReplaceAll(t *testing.T) {
    store := setupTestStore(t)
    seedTaskAndTags(t, store, "CW-0001", "bug", "ui", "frontend")

    require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug", "ui"}))

    linked, err := store.ListTaskTags("CW-0001")
    require.NoError(t, err)
    require.Len(t, linked, 2)
    assert.Equal(t, "bug", linked[0].Slug)
    assert.Equal(t, "ui", linked[1].Slug)

    // Replace with a different set
    require.NoError(t, store.SetTaskTags("CW-0001", []string{"frontend"}))
    linked, err = store.ListTaskTags("CW-0001")
    require.NoError(t, err)
    require.Len(t, linked, 1)
    assert.Equal(t, "frontend", linked[0].Slug)
}

func TestSetTaskTagsPreservesOrder(t *testing.T) {
    store := setupTestStore(t)
    seedTaskAndTags(t, store, "CW-0001", "z-zzz", "a-aaa", "m-mmm")

    // Explicitly non-alphabetical order
    require.NoError(t, store.SetTaskTags("CW-0001", []string{"z-zzz", "a-aaa", "m-mmm"}))

    linked, err := store.ListTaskTags("CW-0001")
    require.NoError(t, err)
    require.Len(t, linked, 3)
    assert.Equal(t, "z-zzz", linked[0].Slug)
    assert.Equal(t, "a-aaa", linked[1].Slug)
    assert.Equal(t, "m-mmm", linked[2].Slug)
}

func TestSetTaskTagsEmpty(t *testing.T) {
    store := setupTestStore(t)
    seedTaskAndTags(t, store, "CW-0001", "bug")

    require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug"}))
    require.NoError(t, store.SetTaskTags("CW-0001", []string{}))

    linked, err := store.ListTaskTags("CW-0001")
    require.NoError(t, err)
    assert.Empty(t, linked)
}

func TestListTaskTagsEmpty(t *testing.T) {
    store := setupTestStore(t)
    seedTaskAndTags(t, store, "CW-0001")

    linked, err := store.ListTaskTags("CW-0001")
    require.NoError(t, err)
    assert.Empty(t, linked)
}

func TestDeleteTagCascadesTaskTags(t *testing.T) {
    store := setupTestStore(t)
    seedTaskAndTags(t, store, "CW-0001", "bug")
    require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug"}))

    require.NoError(t, store.DeleteTag("bug"))

    linked, err := store.ListTaskTags("CW-0001")
    require.NoError(t, err)
    assert.Empty(t, linked)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/persistence/sqlstore/... -run 'TestSetTaskTags|TestListTaskTags|TestDeleteTagCascades' -v`
Expected: compile errors — `SetTaskTags` and `ListTaskTags` don't exist yet.

- [ ] **Step 3: Implement `SetTaskTags` and `ListTaskTags`**

Append to `internal/persistence/sqlstore/tags.go`:

```go
// SetTaskTags replaces all tags linked to the given task in a single transaction.
// Preserves input order via explicit sort_order (0-indexed). An empty slugs slice
// clears all tags on the task.
func (s *Store) SetTaskTags(taskID string, slugs []string) error {
    tx, err := s.db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    if _, err := tx.Exec(`DELETE FROM task_tags WHERE task_id = ?`, taskID); err != nil {
        return err
    }

    now := time.Now().UTC()
    for i, slug := range slugs {
        _, err := tx.Exec(
            `INSERT INTO task_tags (task_id, tag_slug, sort_order, created_at)
             VALUES (?, ?, ?, ?)`,
            taskID, slug, i, now,
        )
        if err != nil {
            return err
        }
    }

    return tx.Commit()
}

// ListTaskTags returns all tags linked to a task, ordered by the user's
// assignment order (sort_order ASC).
func (s *Store) ListTaskTags(taskID string) ([]TagRecord, error) {
    rows, err := s.db.Query(
        `SELECT `+prefixCols("t.", tagSelectCols)+`
         FROM tags t
         JOIN task_tags tt ON tt.tag_slug = t.slug
         WHERE tt.task_id = ?
         ORDER BY tt.sort_order ASC`,
        taskID,
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var tags []TagRecord
    for rows.Next() {
        t, err := scanTag(rows)
        if err != nil {
            return nil, err
        }
        tags = append(tags, *t)
    }
    return tags, rows.Err()
}

// prefixCols prefixes a comma-separated column list with a table alias.
// e.g. prefixCols("t.", "slug, name") → "t.slug, t.name"
func prefixCols(prefix, cols string) string {
    parts := strings.Split(cols, ",")
    out := make([]string, len(parts))
    for i, p := range parts {
        out[i] = prefix + strings.TrimSpace(p)
    }
    return strings.Join(out, ", ")
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./internal/persistence/sqlstore/... -run 'TestSetTaskTags|TestListTaskTags|TestDeleteTagCascades' -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/sqlstore/tags.go internal/persistence/sqlstore/tags_test.go
git commit -m "feat(store): add SetTaskTags and ListTaskTags with sort_order"
```

---

## Task 6: Store layer — MergeTags

**Files:**
- Modify: `internal/persistence/sqlstore/tags.go`
- Modify: `internal/persistence/sqlstore/tags_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/persistence/sqlstore/tags_test.go`:

```go
func TestMergeTagsMovesLinks(t *testing.T) {
    store := setupTestStore(t)
    seedTaskAndTags(t, store, "CW-0001", "bug", "defect")
    seedTaskAndTags(t, store, "CW-0002", "bug", "defect")

    // Task 1 has only "bug"
    require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug"}))
    // Task 2 has only "defect"
    require.NoError(t, store.SetTaskTags("CW-0002", []string{"defect"}))

    // Merge bug into defect
    require.NoError(t, store.MergeTags("bug", "defect"))

    // Source tag is gone
    _, err := store.GetTag("bug")
    assert.Error(t, err)

    // Task 1 now has "defect" (the rewritten row)
    linked1, err := store.ListTaskTags("CW-0001")
    require.NoError(t, err)
    require.Len(t, linked1, 1)
    assert.Equal(t, "defect", linked1[0].Slug)

    // Task 2 still has "defect"
    linked2, err := store.ListTaskTags("CW-0002")
    require.NoError(t, err)
    require.Len(t, linked2, 1)
    assert.Equal(t, "defect", linked2[0].Slug)
}

func TestMergeTagsDeduplicates(t *testing.T) {
    store := setupTestStore(t)
    seedTaskAndTags(t, store, "CW-0001", "bug", "defect")

    // Task has BOTH bug and defect
    require.NoError(t, store.SetTaskTags("CW-0001", []string{"bug", "defect"}))

    // Merge bug into defect — the task already has defect, so the bug row
    // should be deleted (not cause a PK conflict).
    require.NoError(t, store.MergeTags("bug", "defect"))

    linked, err := store.ListTaskTags("CW-0001")
    require.NoError(t, err)
    require.Len(t, linked, 1)
    assert.Equal(t, "defect", linked[0].Slug)
}

func TestMergeTagsSourceNotFound(t *testing.T) {
    store := setupTestStore(t)
    require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "defect", Name: "Defect", Color: "zinc"}))

    err := store.MergeTags("nope", "defect")
    assert.Error(t, err)
}

func TestMergeTagsDestNotFound(t *testing.T) {
    store := setupTestStore(t)
    require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "bug", Name: "Bug", Color: "zinc"}))

    err := store.MergeTags("bug", "nope")
    assert.Error(t, err)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/persistence/sqlstore/... -run 'TestMergeTags' -v`
Expected: compile error — `MergeTags` doesn't exist.

- [ ] **Step 3: Implement `MergeTags`**

Append to `internal/persistence/sqlstore/tags.go`:

```go
// MergeTags folds the source tag into the destination.
// All task_tags rows pointing at source are rewritten to dest.
// If a task already has both source and dest, the source row is dropped
// (dest wins). Finally the source tag row is deleted.
// Both tags must exist; source must differ from dest.
func (s *Store) MergeTags(sourceSlug, destSlug string) error {
    if sourceSlug == destSlug {
        return fmt.Errorf("merge source and destination cannot be the same")
    }
    // Verify both exist before touching anything
    if _, err := s.GetTag(sourceSlug); err != nil {
        return err
    }
    if _, err := s.GetTag(destSlug); err != nil {
        return err
    }

    tx, err := s.db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // Remove source rows for tasks that already have the destination
    if _, err := tx.Exec(
        `DELETE FROM task_tags
         WHERE tag_slug = ?
           AND task_id IN (SELECT task_id FROM task_tags WHERE tag_slug = ?)`,
        sourceSlug, destSlug,
    ); err != nil {
        return err
    }

    // Rewrite remaining source rows to dest
    if _, err := tx.Exec(
        `UPDATE task_tags SET tag_slug = ? WHERE tag_slug = ?`,
        destSlug, sourceSlug,
    ); err != nil {
        return err
    }

    // Delete the source tag itself
    if _, err := tx.Exec(`DELETE FROM tags WHERE slug = ?`, sourceSlug); err != nil {
        return err
    }

    return tx.Commit()
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./internal/persistence/sqlstore/... -run 'TestMergeTags' -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Run ALL tag store tests to verify no regressions**

Run: `go test ./internal/persistence/sqlstore/... -v`
Expected: PASS for everything tag-related plus existing tests.

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/sqlstore/tags.go internal/persistence/sqlstore/tags_test.go
git commit -m "feat(store): add MergeTags with dedupe semantics"
```

---

## Task 7: Service layer — TagService CRUD + validation

**Files:**
- Create: `internal/service/tag.go`
- Create: `internal/service/tag_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/service/tag_test.go`:

```go
package service_test

import (
    "database/sql"
    "testing"

    "github.com/hollis-labs/torque/internal/persistence/sqlstore"
    "github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
    "github.com/hollis-labs/torque/internal/service"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    _ "modernc.org/sqlite"
)

func setupTagServiceTest(t *testing.T) (*service.Service, *sqlstore.Store) {
    t.Helper()
    db, err := sql.Open("sqlite", ":memory:")
    require.NoError(t, err)
    require.NoError(t, migrations.Run(db))
    store, err := sqlstore.New(db, "sqlite")
    require.NoError(t, err)
    t.Cleanup(func() { store.Close() })
    svc := service.New(store)
    return svc, store
}

func TestTagServiceCreateWithName(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    tag, err := svc.Tag.Create(service.TagCreateInput{Name: "Frontend Bug"})
    require.NoError(t, err)
    assert.Equal(t, "frontend-bug", tag.Slug)
    assert.Equal(t, "Frontend Bug", tag.Name)
    assert.Equal(t, "zinc", tag.Color)
}

func TestTagServiceCreateWithExplicitSlug(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    tag, err := svc.Tag.Create(service.TagCreateInput{
        Name: "User Interface",
        Slug: "ui",
        Color: "blue",
    })
    require.NoError(t, err)
    assert.Equal(t, "ui", tag.Slug)
    assert.Equal(t, "User Interface", tag.Name)
    assert.Equal(t, "blue", tag.Color)
}

func TestTagServiceCreateRequiresName(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    _, err := svc.Tag.Create(service.TagCreateInput{Name: ""})
    assert.Error(t, err)
}

func TestTagServiceCreateRejectsEmptySlug(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    // Name that slugifies to empty
    _, err := svc.Tag.Create(service.TagCreateInput{Name: "!!!"})
    assert.Error(t, err)
}

func TestTagServiceCreateRejectsBadColor(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    _, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug", Color: "turquoise"})
    assert.Error(t, err)
}

func TestTagServiceCreateRejectsBadExplicitSlug(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    _, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug", Slug: "Bug!"})
    assert.Error(t, err)
}

func TestTagServiceCreateRejectsLongName(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    longName := ""
    for i := 0; i < 65; i++ {
        longName += "a"
    }
    _, err := svc.Tag.Create(service.TagCreateInput{Name: longName})
    assert.Error(t, err)
}

func TestTagServiceCreateRejectsLongDescription(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    longDesc := ""
    for i := 0; i < 501; i++ {
        longDesc += "a"
    }
    _, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug", Description: longDesc})
    assert.Error(t, err)
}

func TestTagServiceUpdate(t *testing.T) {
    svc, _ := setupTagServiceTest(t)
    _, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
    require.NoError(t, err)

    newName := "Bug Report"
    newColor := "red"
    updated, err := svc.Tag.Update("bug", service.TagUpdateInput{
        Name:  &newName,
        Color: &newColor,
    })
    require.NoError(t, err)
    assert.Equal(t, "Bug Report", updated.Name)
    assert.Equal(t, "red", updated.Color)
}

func TestTagServiceUpdateRejectsBadColor(t *testing.T) {
    svc, _ := setupTagServiceTest(t)
    _, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
    require.NoError(t, err)

    bad := "turquoise"
    _, err = svc.Tag.Update("bug", service.TagUpdateInput{Color: &bad})
    assert.Error(t, err)
}

func TestTagServiceList(t *testing.T) {
    svc, _ := setupTagServiceTest(t)
    _, _ = svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
    _, _ = svc.Tag.Create(service.TagCreateInput{Name: "UI"})

    tags, err := svc.Tag.List()
    require.NoError(t, err)
    assert.Len(t, tags, 2)
}

func TestTagServiceDelete(t *testing.T) {
    svc, _ := setupTagServiceTest(t)
    _, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
    require.NoError(t, err)

    require.NoError(t, svc.Tag.Delete("bug"))

    _, err = svc.Tag.Get("bug")
    assert.Error(t, err)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/service/... -run TestTagService -v`
Expected: compile errors — `service.Service.Tag`, `service.TagCreateInput`, `service.TagUpdateInput` don't exist.

- [ ] **Step 3: Create `tag.go`**

Create `internal/service/tag.go`:

```go
package service

import (
    "strings"

    "github.com/hollis-labs/torque/internal/persistence/sqlstore"
    "github.com/hollis-labs/go-strutil"
)

// TagService provides business logic for tags.
type TagService struct {
    store *sqlstore.Store
}

// TagCreateInput holds fields for creating a tag.
type TagCreateInput struct {
    Name        string
    Slug        string
    Description string
    Color       string
}

// TagUpdateInput is a re-export of the store-layer update type.
type TagUpdateInput = sqlstore.TagUpdate

var validTagColors = map[string]bool{
    "zinc": true, "red": true, "orange": true, "amber": true,
    "green": true, "teal": true, "blue": true, "violet": true, "pink": true,
}

// slugPattern validates explicit slug input: lowercase alphanumeric + hyphens,
// no leading/trailing/consecutive hyphens, 1-48 chars.
func isValidSlug(s string) bool {
    if len(s) < 1 || len(s) > 48 {
        return false
    }
    // Re-slugify and compare — this catches all the rules at once
    // (lowercase, no leading/trailing hyphens, no consecutive, only allowed chars).
    return strutil.Slugify(s) == s
}

func validateColor(c string) (string, error) {
    if c == "" {
        return "zinc", nil
    }
    if !validTagColors[c] {
        return "", &ValidationError{Field: "color", Message: "invalid color; must be one of zinc, red, orange, amber, green, teal, blue, violet, pink"}
    }
    return c, nil
}

// Create creates a new tag, deriving the slug from the name if not provided.
func (s *TagService) Create(input TagCreateInput) (*sqlstore.TagRecord, error) {
    name := strings.TrimSpace(input.Name)
    if name == "" {
        return nil, &ValidationError{Field: "name", Message: "name is required"}
    }
    if len(name) > 64 {
        return nil, &ValidationError{Field: "name", Message: "name must be at most 64 characters"}
    }
    if len(input.Description) > 500 {
        return nil, &ValidationError{Field: "description", Message: "description must be at most 500 characters"}
    }

    color, err := validateColor(input.Color)
    if err != nil {
        return nil, err
    }

    slug := input.Slug
    if slug == "" {
        slug = strutil.Slugify(name)
        if slug == "" {
            return nil, &ValidationError{Field: "name", Message: "name does not produce a valid slug"}
        }
    } else if !isValidSlug(slug) {
        return nil, &ValidationError{Field: "slug", Message: "slug must be lowercase alphanumeric with hyphens, 1-48 chars"}
    }

    rec := &sqlstore.TagRecord{
        Slug:        slug,
        Name:        name,
        Description: input.Description,
        Color:       color,
    }
    if err := s.store.CreateTag(rec); err != nil {
        return nil, err
    }
    return s.store.GetTag(slug)
}

// Get returns a single tag.
func (s *TagService) Get(slug string) (*sqlstore.TagRecord, error) {
    return s.store.GetTag(slug)
}

// List returns all tags.
func (s *TagService) List() ([]sqlstore.TagRecord, error) {
    return s.store.ListTags()
}

// Update applies a partial update to a tag. Does not allow changing the slug.
func (s *TagService) Update(slug string, u TagUpdateInput) (*sqlstore.TagRecord, error) {
    if u.Name != nil {
        name := strings.TrimSpace(*u.Name)
        if name == "" {
            return nil, &ValidationError{Field: "name", Message: "name cannot be blank"}
        }
        if len(name) > 64 {
            return nil, &ValidationError{Field: "name", Message: "name must be at most 64 characters"}
        }
        u.Name = &name
    }
    if u.Description != nil && len(*u.Description) > 500 {
        return nil, &ValidationError{Field: "description", Message: "description must be at most 500 characters"}
    }
    if u.Color != nil {
        c, err := validateColor(*u.Color)
        if err != nil {
            return nil, err
        }
        u.Color = &c
    }

    if err := s.store.UpdateTag(slug, u); err != nil {
        return nil, err
    }
    return s.store.GetTag(slug)
}

// Delete removes a tag by slug.
func (s *TagService) Delete(slug string) error {
    return s.store.DeleteTag(slug)
}
```

- [ ] **Step 4: Update `Service` container to register TagService**

Edit `internal/service/service.go`:

```go
package service

import "github.com/hollis-labs/torque/internal/persistence/sqlstore"

// Service is the root service dispatcher that aggregates all domain services.
type Service struct {
    Task     *TaskService
    Run      *RunService
    Artifact *ArtifactService
    Comment  *CommentService
    Settings *SettingsService
    Feature  *FeatureService
    Sprint   *SprintService
    Project  *ProjectService
    Epic     *EpicService
    Tag      *TagService
}

// New constructs a Service wired to the provided store.
func New(store *sqlstore.Store) *Service {
    feature := &FeatureService{store: store}
    tag := &TagService{store: store}
    task := &TaskService{store: store, feature: feature, tags: tag}
    return &Service{
        Task:     task,
        Run:      &RunService{store: store},
        Artifact: &ArtifactService{store: store},
        Comment:  &CommentService{store: store},
        Settings: &SettingsService{store: store},
        Feature:  feature,
        Sprint:   &SprintService{store: store, feature: feature, task: task},
        Project:  &ProjectService{store: store, feature: feature},
        Epic:     &EpicService{store: store, feature: feature},
        Tag:      tag,
    }
}
```

Note: this references a new `tags` field on `TaskService`. We'll add that field in Task 10. For now, this file will fail to compile — that's expected, and Task 10 fixes it. DO NOT run the full test suite yet.

- [ ] **Step 5: Run the tag service tests only (not the full suite)**

Run: `go test ./internal/service/... -run TestTagService -v`
Expected: compile errors because `TaskService` doesn't have a `tags` field yet. That's fine.

**Alternative:** temporarily comment out the `tags: tag,` line in `service.go` to get this task green, then uncomment in Task 10. Or, add an unused private field `tags *TagService` to `TaskService` now (in `task.go`) without using it — which is cleaner. Do the latter: add the unused field now.

- [ ] **Step 6: Add the unused `tags` field to `TaskService`**

In `internal/service/task.go`, find the `TaskService` struct:

```go
type TaskService struct {
    store   *sqlstore.Store
    feature *FeatureService
}
```

Change to:

```go
type TaskService struct {
    store   *sqlstore.Store
    feature *FeatureService
    tags    *TagService
}
```

- [ ] **Step 7: Run tag service tests again**

Run: `go test ./internal/service/... -run TestTagService -v`
Expected: PASS (all tag service tests).

- [ ] **Step 8: Commit**

```bash
git add internal/service/tag.go internal/service/tag_test.go internal/service/service.go internal/service/task.go
git commit -m "feat(service): add TagService with CRUD and validation"
```

---

## Task 8: Service layer — Merge and ResolveNames

**Files:**
- Modify: `internal/service/tag.go`
- Modify: `internal/service/tag_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/service/tag_test.go`:

```go
func TestTagServiceMerge(t *testing.T) {
    svc, store := setupTagServiceTest(t)
    _, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
    require.NoError(t, err)
    _, err = svc.Tag.Create(service.TagCreateInput{Name: "Defect"})
    require.NoError(t, err)

    // Seed a task
    task := sampleTask("CW-MERGE-001")
    require.NoError(t, store.CreateTask(task))
    require.NoError(t, store.SetTaskTags("CW-MERGE-001", []string{"bug"}))

    require.NoError(t, svc.Tag.Merge("bug", "defect"))

    _, err = svc.Tag.Get("bug")
    assert.Error(t, err)

    linked, err := store.ListTaskTags("CW-MERGE-001")
    require.NoError(t, err)
    require.Len(t, linked, 1)
    assert.Equal(t, "defect", linked[0].Slug)
}

func TestTagServiceMergeRejectsSame(t *testing.T) {
    svc, _ := setupTagServiceTest(t)
    _, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug"})
    require.NoError(t, err)

    err = svc.Tag.Merge("bug", "bug")
    assert.Error(t, err)
}

func TestResolveNamesAutoCreates(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    slugs, err := svc.Tag.ResolveNames([]string{"Bug", "UI"})
    require.NoError(t, err)
    require.Equal(t, []string{"bug", "ui"}, slugs)

    // Confirm tags were created with the raw input as the display name
    bug, err := svc.Tag.Get("bug")
    require.NoError(t, err)
    assert.Equal(t, "Bug", bug.Name)
    assert.Equal(t, "zinc", bug.Color)

    ui, err := svc.Tag.Get("ui")
    require.NoError(t, err)
    assert.Equal(t, "UI", ui.Name)
}

func TestResolveNamesPreservesExistingDisplayName(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    // Pre-create "bug" with a specific display name
    _, err := svc.Tag.Create(service.TagCreateInput{Name: "Bug Report", Slug: "bug", Color: "red"})
    require.NoError(t, err)

    // Resolve with a different casing / display name for the same slug
    slugs, err := svc.Tag.ResolveNames([]string{"bug", "BUG"})
    require.NoError(t, err)
    require.Equal(t, []string{"bug"}, slugs) // dedup

    // Existing display name and color are NOT overwritten
    tag, err := svc.Tag.Get("bug")
    require.NoError(t, err)
    assert.Equal(t, "Bug Report", tag.Name)
    assert.Equal(t, "red", tag.Color)
}

func TestResolveNamesSkipsEmptyInputs(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    slugs, err := svc.Tag.ResolveNames([]string{"Bug", "   ", "!!!", "UI"})
    require.NoError(t, err)
    require.Equal(t, []string{"bug", "ui"}, slugs)
}

func TestResolveNamesAllEmpty(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    slugs, err := svc.Tag.ResolveNames([]string{"   ", "!!!"})
    require.NoError(t, err)
    assert.Empty(t, slugs)
}

func TestResolveNamesPreservesOrder(t *testing.T) {
    svc, _ := setupTagServiceTest(t)

    slugs, err := svc.Tag.ResolveNames([]string{"zulu", "alpha", "mike"})
    require.NoError(t, err)
    require.Equal(t, []string{"zulu", "alpha", "mike"}, slugs)
}
```

Note: `sampleTask` is defined in `tasks_test.go` but lives in a separate `sqlstore_test` package. For the service tests, you'll need a local helper. Add this near the top of `tag_test.go` (after `setupTagServiceTest`):

```go
func sampleTask(id string) *sqlstore.TaskRecord {
    return &sqlstore.TaskRecord{
        ID:       id,
        Title:    "Test task",
        Status:   "todo",
        Priority: 2,
        Executor: "cli",
    }
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/service/... -run 'TestTagServiceMerge|TestResolveNames' -v`
Expected: compile errors — `Merge` and `ResolveNames` don't exist yet.

- [ ] **Step 3: Implement `Merge` and `ResolveNames`**

Append to `internal/service/tag.go`:

```go
// Merge folds source into destination. Both must exist; source ≠ dest.
// Rewrites all task_tags links and deletes the source tag.
func (s *TagService) Merge(sourceSlug, destSlug string) error {
    if sourceSlug == destSlug {
        return &ValidationError{Field: "into", Message: "merge source and destination cannot be the same"}
    }
    return s.store.MergeTags(sourceSlug, destSlug)
}

// ResolveNames takes a list of tag names (or existing slugs), normalizes each
// via strutil.Slugify, skips any that normalize to empty, deduplicates while
// preserving first-occurrence order, and auto-creates any missing tags with
// the raw input as the display name. Returns the slug list in input order.
func (s *TagService) ResolveNames(inputs []string) ([]string, error) {
    var result []string
    seen := make(map[string]bool)
    // Track the first-seen raw input for each new slug so we can use it as the name
    rawByFirstSlug := make(map[string]string)

    for _, raw := range inputs {
        trimmed := strings.TrimSpace(raw)
        if trimmed == "" {
            continue
        }
        slug := strutil.Slugify(trimmed)
        if slug == "" {
            continue
        }
        if seen[slug] {
            continue
        }
        seen[slug] = true
        result = append(result, slug)
        if _, exists := rawByFirstSlug[slug]; !exists {
            rawByFirstSlug[slug] = trimmed
        }
    }

    // Auto-create any that don't exist. Use INSERT OR IGNORE via a new store method
    // for atomicity, OR call GetTag + CreateTag as a best-effort sequence here.
    // We use the simpler sequence — race conditions are not a concern for single-node SQLite.
    for _, slug := range result {
        if _, err := s.store.GetTag(slug); err == nil {
            continue // already exists
        }
        name := rawByFirstSlug[slug]
        // Use the raw input as name; if it happens to not pass the 64-char check,
        // truncate it so we don't reject a task-create just because a tag name was long.
        if len(name) > 64 {
            name = name[:64]
        }
        rec := &sqlstore.TagRecord{
            Slug:  slug,
            Name:  name,
            Color: "zinc",
        }
        if err := s.store.CreateTag(rec); err != nil {
            return nil, err
        }
    }

    return result, nil
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./internal/service/... -run 'TestTagServiceMerge|TestResolveNames' -v`
Expected: PASS (7 tests).

- [ ] **Step 5: Run ALL tag service tests to confirm no regression**

Run: `go test ./internal/service/... -run TestTagService -v; go test ./internal/service/... -run TestResolveNames -v`
Expected: PASS for all.

- [ ] **Step 6: Commit**

```bash
git add internal/service/tag.go internal/service/tag_test.go
git commit -m "feat(service): add TagService.Merge and ResolveNames auto-create"
```

---

## Task 9: Remove tags column from task storage

**Files:**
- Modify: `internal/persistence/sqlstore/tasks.go`
- Modify: `internal/persistence/sqlstore/tasks_test.go`

- [ ] **Step 1: Remove `Tags` from `TaskRecord`**

In `internal/persistence/sqlstore/tasks.go`, find:

```go
type TaskRecord struct {
    ID               string
    Title            string
    Description      string
    Status           string
    Priority         int
    Tags             string
    Manual           bool
    // ...
}
```

Delete the `Tags string` line so the struct becomes:

```go
type TaskRecord struct {
    ID               string
    Title            string
    Description      string
    Status           string
    Priority         int
    Manual           bool
    Executor         string
    // ... rest unchanged
}
```

- [ ] **Step 2: Remove `Tags` from `TaskUpdate`**

In the same file, find the `TaskUpdate` struct:

```go
type TaskUpdate struct {
    Title             *string
    Description       *string
    Status            *string
    Priority          *int
    Tags              *string
    Manual            *bool
    // ...
}
```

Delete the `Tags *string` line.

- [ ] **Step 3: Remove `tags` from `taskSelectCols`, `scanTask`, CREATE SQL, and UPDATE SQL**

In `tasks.go`:

1. **`taskSelectCols` constant** — remove `tags` from the list:

```go
const taskSelectCols = `id, title, description, status, priority, manual,
    executor, agent_profile, working_dir, tools, permissions, environment,
    system_prompt, files, cost_budget, max_retries, max_duration_ms, token_budget,
    on_done, on_fail, on_review, escalation_chain, quality_gates, deliverables,
    deliverable_preset, on_done_merge, depends_on, blocked_reason, metadata,
    sprint_id, project_id, epic_id, created_at, updated_at`
```

2. **`scanTask` function** — remove `&t.Tags` from the Scan call so it reads:

```go
err := row.Scan(
    &t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &manual,
    &t.Executor, &t.AgentProfile, &t.WorkingDir, &t.Tools, &t.Permissions, &t.Environment,
    &t.SystemPrompt, &t.Files, &t.CostBudget, &t.MaxRetries, &t.MaxDurationMs, &t.TokenBudget,
    &t.OnDone, &t.OnFail, &t.OnReview, &t.EscalationChain, &t.QualityGates, &t.Deliverables,
    &t.DeliverablePreset, &t.OnDoneMerge, &t.DependsOn, &t.BlockedReason, &t.Metadata,
    &t.SprintID, &t.ProjectID, &t.EpicID, &t.CreatedAt, &t.UpdatedAt,
)
```

3. **`applyDefaults` function** — remove the `Tags` default:

Find and delete:
```go
if t.Tags == "" {
    t.Tags = "[]"
}
```

4. **`CreateTask` INSERT** — the INSERT statement uses 33 columns. Remove `tags` from both the column list and the value list:

```go
const q = `INSERT INTO tasks (
    id, title, description, status, priority, manual,
    executor, agent_profile, working_dir, tools, permissions, environment,
    system_prompt, files, cost_budget, max_retries, max_duration_ms, token_budget,
    on_done, on_fail, on_review, escalation_chain, quality_gates, deliverables,
    deliverable_preset, on_done_merge, depends_on, blocked_reason, metadata,
    sprint_id, project_id, epic_id
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

_, err := s.db.Exec(q,
    t.ID, t.Title, t.Description, t.Status, t.Priority, manual,
    t.Executor, t.AgentProfile, t.WorkingDir, t.Tools, t.Permissions, t.Environment,
    t.SystemPrompt, t.Files, t.CostBudget, t.MaxRetries, t.MaxDurationMs, t.TokenBudget,
    t.OnDone, t.OnFail, t.OnReview, t.EscalationChain, t.QualityGates, t.Deliverables,
    t.DeliverablePreset, t.OnDoneMerge, t.DependsOn, t.BlockedReason, t.Metadata,
    t.SprintID, t.ProjectID, t.EpicID,
)
```

Note: the `?` count changed from 33 to 32. Count placeholders carefully.

5. **`UpdateTask` function** — remove the Tags branch:

Find and delete:
```go
if u.Tags != nil {
    setClauses = append(setClauses, "tags = ?")
    args = append(args, *u.Tags)
}
```

- [ ] **Step 4: Update `tasks_test.go` to remove tags references**

In `internal/persistence/sqlstore/tasks_test.go`, find any use of `.Tags` on `TaskRecord` or `TaskUpdate` and delete it. Specifically search for:

- `Tags:` as a struct field in `sampleTask` or any test fixture
- `task.Tags` assertions
- `sqlstore.TaskUpdate{Tags: ...}` or similar

For example, if `sampleTask` contains `Tags: "[]"`, remove that line.

- [ ] **Step 5: Run ALL sqlstore tests and verify they pass**

Run: `go test ./internal/persistence/sqlstore/... -v`
Expected: PASS. If anything in `tasks_test.go` still references `.Tags`, fix it.

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/sqlstore/tasks.go internal/persistence/sqlstore/tasks_test.go
git commit -m "refactor(store): remove tags column from TaskRecord, drop from SQL"
```

---

## Task 10: Wire TagService into TaskService

**Files:**
- Modify: `internal/service/task.go`
- Modify: `internal/service/task_test.go`
- Modify: `internal/service/task_sprint_test.go` (if it references `Tags`)

- [ ] **Step 1: Update `TaskCreateInput` in `task.go`**

In `internal/service/task.go`, `TaskCreateInput` already has `Tags []string` — no change needed there. What DOES change: the Create function body no longer calls `marshalJSON(input.Tags)`.

Find this block in `TaskService.Create`:

```go
rec := &sqlstore.TaskRecord{
    ID:                id,
    Title:             input.Title,
    Description:       input.Description,
    Priority:          priority,
    Tags:              marshalJSON(input.Tags),  // DELETE THIS LINE
    Manual:            input.Manual,
    // ...
}
```

Delete the `Tags:` line so the struct literal no longer references it.

Then, after the `if err := s.store.CreateTask(rec); err != nil { return nil, err }` call, add the tag resolution and linking:

```go
if err := s.store.CreateTask(rec); err != nil {
    return nil, err
}

// Resolve tag names/slugs and link them to the task
if len(input.Tags) > 0 {
    slugs, err := s.tags.ResolveNames(input.Tags)
    if err != nil {
        return nil, err
    }
    if err := s.store.SetTaskTags(id, slugs); err != nil {
        return nil, err
    }
}

return s.store.GetTask(id)
```

- [ ] **Step 2: Introduce `TaskUpdateInput` and update `Update`**

Find the existing `Update` method:

```go
// Update applies a partial update to a task.
func (s *TaskService) Update(id string, update sqlstore.TaskUpdate) error {
    return s.store.UpdateTask(id, update)
}
```

Replace it with a new service-level input type and updated method. Add to `internal/service/task.go`:

```go
// TaskUpdateInput wraps the store-level TaskUpdate and adds a tags field.
// The store-level TaskUpdate no longer carries tags because they live in
// the task_tags link table, not a column on tasks.
type TaskUpdateInput struct {
    sqlstore.TaskUpdate
    Tags *[]string // nil = no change; non-nil = replace all linked tags
}

// Update applies a partial update to a task. If Tags is non-nil, linked tags
// are resolved and replaced.
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

- [ ] **Step 3: Remove the now-dead `marshalJSON` helper if unused**

Search for other callers of `marshalJSON` in `internal/service/`:

Run: `grep -rn marshalJSON internal/service/`
Expected: The helper is likely in `helpers.go`. If `Tags: marshalJSON(input.Tags)` was its only caller, delete the function. Otherwise leave it alone.

- [ ] **Step 4: Fix any callers of `TaskService.Update` that pass `sqlstore.TaskUpdate` directly**

Run: `grep -rn "svc.Task.Update\|s.Task.Update\|\.Task\.Update(" internal/`

Every call site now needs to pass `service.TaskUpdateInput{TaskUpdate: ...}` instead of `sqlstore.TaskUpdate{...}` directly. Update each one. The callers likely include:

- HTTP handler in `internal/httpserver/tasks.go` (`updateTask`)
- Any sprint service or approval flow that updates tasks

For now, wrap the existing store-level updates. Example for `httpserver/tasks.go:updateTask`:

```go
update := sqlstore.TaskUpdate{}
// ... populate update fields ...

if err := s.svc.Task.Update(id, service.TaskUpdateInput{TaskUpdate: update}); err != nil {
    // ...
}
```

- [ ] **Step 5: Update `task_test.go` and `task_sprint_test.go` if they reference `Tags` on the store-level TaskUpdate**

Run: `grep -n "TaskUpdate{" internal/service/task_test.go internal/service/task_sprint_test.go`

For any `sqlstore.TaskUpdate{Tags: ...}`, that's no longer valid. Update to use `service.TaskUpdateInput{Tags: &[]string{...}}`. For any call like `svc.Task.Update(id, sqlstore.TaskUpdate{...})`, wrap in `service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{...}}`.

- [ ] **Step 6: Run ALL service tests**

Run: `go test ./internal/service/... -v`
Expected: PASS for everything.

- [ ] **Step 7: Run ALL Go tests to catch any regressions elsewhere**

Run: `go test ./... 2>&1 | tail -50`
Expected: PASS. If `internal/httpserver/` or `internal/runtime/` fails to compile because of the `TaskUpdate` signature change, fix those call sites now. Most of those will be fixed in Task 11, but make compile-level callers happy.

- [ ] **Step 8: Commit**

```bash
git add internal/service/task.go internal/service/task_test.go internal/service/task_sprint_test.go internal/service/helpers.go internal/httpserver/tasks.go
git commit -m "refactor(service): TaskService uses TagService for tag resolution and linking"
```

---

## Task 11: HTTP handlers for tags

**Files:**
- Create: `internal/httpserver/tags.go`
- Modify: `internal/httpserver/server.go`
- Modify: `internal/httpserver/server_test.go`

- [ ] **Step 1: Write the failing handler tests**

Append to `internal/httpserver/server_test.go`:

```go
func TestCreateAndGetTag(t *testing.T) {
    ts := setupTestServer(t)

    body := `{"name":"Frontend Bug","color":"red","description":"UI issues"}`
    resp, err := http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(body))
    require.NoError(t, err)
    assert.Equal(t, http.StatusCreated, resp.StatusCode)

    var created map[string]interface{}
    require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
    assert.Equal(t, "frontend-bug", created["slug"])
    assert.Equal(t, "Frontend Bug", created["name"])
    assert.Equal(t, "red", created["color"])

    // GET it back
    resp2, err := http.Get(ts.URL + "/api/v1/tags/frontend-bug")
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

func TestCreateTagValidationErrors(t *testing.T) {
    ts := setupTestServer(t)

    // Missing name
    resp, _ := http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{}`))
    assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

    // Invalid color
    resp, _ = http.Post(ts.URL+"/api/v1/tags", "application/json",
        bytes.NewBufferString(`{"name":"Bug","color":"turquoise"}`))
    assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

    // Malformed JSON
    resp, _ = http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{not json`))
    assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestGetTagNotFound(t *testing.T) {
    ts := setupTestServer(t)

    resp, err := http.Get(ts.URL + "/api/v1/tags/nonexistent")
    require.NoError(t, err)
    assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestListTags(t *testing.T) {
    ts := setupTestServer(t)

    http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Bug","color":"red"}`))
    http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"UI","color":"blue"}`))

    resp, err := http.Get(ts.URL + "/api/v1/tags")
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp.StatusCode)

    var out map[string]interface{}
    require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
    tags, ok := out["tags"].([]interface{})
    require.True(t, ok)
    assert.Len(t, tags, 2)
}

func TestPatchTag(t *testing.T) {
    ts := setupTestServer(t)
    http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Bug","color":"red"}`))

    req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tags/bug", bytes.NewBufferString(`{"color":"orange"}`))
    req.Header.Set("Content-Type", "application/json")
    resp, err := http.DefaultClient.Do(req)
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp.StatusCode)

    var updated map[string]interface{}
    require.NoError(t, json.NewDecoder(resp.Body).Decode(&updated))
    assert.Equal(t, "orange", updated["color"])
}

func TestDeleteTag(t *testing.T) {
    ts := setupTestServer(t)
    http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Bug","color":"red"}`))

    req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/tags/bug", nil)
    resp, err := http.DefaultClient.Do(req)
    require.NoError(t, err)
    assert.Equal(t, http.StatusNoContent, resp.StatusCode)

    // GET returns 404 now
    resp2, _ := http.Get(ts.URL + "/api/v1/tags/bug")
    assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestMergeTags(t *testing.T) {
    ts := setupTestServer(t)
    http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Bug","color":"red"}`))
    http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Defect","color":"red"}`))

    resp, err := http.Post(ts.URL+"/api/v1/tags/bug/merge", "application/json",
        bytes.NewBufferString(`{"into":"defect"}`))
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp.StatusCode)

    var result map[string]interface{}
    require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
    assert.Equal(t, "defect", result["slug"])

    // Source tag is gone
    resp2, _ := http.Get(ts.URL + "/api/v1/tags/bug")
    assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/httpserver/... -run 'TestCreateAndGetTag|TestCreateTagValidation|TestGetTagNotFound|TestListTags|TestPatchTag|TestDeleteTag|TestMergeTags' -v`
Expected: all fail with `404 Not Found` or similar because the routes don't exist yet.

- [ ] **Step 3: Create `tags.go` handler file**

Create `internal/httpserver/tags.go`:

```go
package httpserver

import (
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/hollis-labs/torque/internal/persistence/sqlstore"
    "github.com/hollis-labs/torque/internal/service"
)

// tagJSON converts a TagRecord to a JSON-friendly map.
func tagJSON(t *sqlstore.TagRecord) map[string]interface{} {
    return map[string]interface{}{
        "slug":        t.Slug,
        "name":        t.Name,
        "description": t.Description,
        "color":       t.Color,
        "created_at":  t.CreatedAt.Format(time.RFC3339),
        "updated_at":  t.UpdatedAt.Format(time.RFC3339),
    }
}

func tagsJSON(tags []sqlstore.TagRecord) []map[string]interface{} {
    out := make([]map[string]interface{}, len(tags))
    for i := range tags {
        out[i] = tagJSON(&tags[i])
    }
    return out
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
    tags, err := s.svc.Tag.List()
    if err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    if tags == nil {
        tags = []sqlstore.TagRecord{}
    }
    writeJSON(w, http.StatusOK, map[string]interface{}{"tags": tagsJSON(tags)})
}

func (s *Server) getTag(w http.ResponseWriter, r *http.Request) {
    slug := chi.URLParam(r, "slug")
    tag, err := s.svc.Tag.Get(slug)
    if err != nil {
        writeError(w, http.StatusNotFound, err.Error())
        return
    }
    writeJSON(w, http.StatusOK, tagJSON(tag))
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Name        string `json:"name"`
        Slug        string `json:"slug"`
        Description string `json:"description"`
        Color       string `json:"color"`
    }
    if err := readJSON(r, &req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
        return
    }

    tag, err := s.svc.Tag.Create(service.TagCreateInput{
        Name:        req.Name,
        Slug:        req.Slug,
        Description: req.Description,
        Color:       req.Color,
    })
    if err != nil {
        if _, ok := err.(*service.ValidationError); ok {
            writeError(w, http.StatusUnprocessableEntity, err.Error())
            return
        }
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    writeJSON(w, http.StatusCreated, tagJSON(tag))
}

func (s *Server) updateTag(w http.ResponseWriter, r *http.Request) {
    slug := chi.URLParam(r, "slug")
    var req struct {
        Name        *string `json:"name"`
        Description *string `json:"description"`
        Color       *string `json:"color"`
    }
    if err := readJSON(r, &req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
        return
    }

    tag, err := s.svc.Tag.Update(slug, service.TagUpdateInput{
        Name:        req.Name,
        Description: req.Description,
        Color:       req.Color,
    })
    if err != nil {
        if _, ok := err.(*service.ValidationError); ok {
            writeError(w, http.StatusUnprocessableEntity, err.Error())
            return
        }
        // Store layer returns "tag X not found" — translate to 404
        writeError(w, http.StatusNotFound, err.Error())
        return
    }
    writeJSON(w, http.StatusOK, tagJSON(tag))
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
    slug := chi.URLParam(r, "slug")
    if err := s.svc.Tag.Delete(slug); err != nil {
        writeError(w, http.StatusNotFound, err.Error())
        return
    }
    w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mergeTags(w http.ResponseWriter, r *http.Request) {
    sourceSlug := chi.URLParam(r, "slug")
    var req struct {
        Into string `json:"into"`
    }
    if err := readJSON(r, &req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
        return
    }
    if req.Into == "" {
        writeError(w, http.StatusUnprocessableEntity, "'into' is required")
        return
    }

    if err := s.svc.Tag.Merge(sourceSlug, req.Into); err != nil {
        if _, ok := err.(*service.ValidationError); ok {
            writeError(w, http.StatusUnprocessableEntity, err.Error())
            return
        }
        writeError(w, http.StatusNotFound, err.Error())
        return
    }

    dest, err := s.svc.Tag.Get(req.Into)
    if err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    writeJSON(w, http.StatusOK, tagJSON(dest))
}
```

- [ ] **Step 4: Register the routes in `server.go`**

In `internal/httpserver/server.go`, find where other routes are registered (look for `r.Get("/api/v1/tasks"`, etc.) and add the tag routes alongside them:

```go
r.Route("/api/v1/tags", func(r chi.Router) {
    r.Get("/", s.listTags)
    r.Post("/", s.createTag)
    r.Get("/{slug}", s.getTag)
    r.Patch("/{slug}", s.updateTag)
    r.Delete("/{slug}", s.deleteTag)
    r.Post("/{slug}/merge", s.mergeTags)
})
```

(Adjust the exact structure to match how existing routes are defined in this file — follow the established pattern.)

- [ ] **Step 5: Run handler tests and verify they pass**

Run: `go test ./internal/httpserver/... -run 'TestCreateAndGetTag|TestCreateTagValidation|TestGetTagNotFound|TestListTags|TestPatchTag|TestDeleteTag|TestMergeTags' -v`
Expected: PASS (7 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/httpserver/tags.go internal/httpserver/server.go internal/httpserver/server_test.go
git commit -m "feat(http): add /api/v1/tags CRUD and merge endpoints"
```

---

## Task 12: Update task HTTP handlers to use structured tags

**Files:**
- Modify: `internal/httpserver/tasks.go`
- Modify: `internal/httpserver/server_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/httpserver/server_test.go`:

```go
func TestCreateTaskWithTags(t *testing.T) {
    ts := setupTestServer(t)

    body := `{"title":"Test","description":"x","priority":1,"executor":"cli","tags":["Bug","UI"]}`
    resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
    require.NoError(t, err)
    assert.Equal(t, http.StatusCreated, resp.StatusCode)

    var created map[string]interface{}
    require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

    tags, ok := created["tags"].([]interface{})
    require.True(t, ok)
    require.Len(t, tags, 2)

    tag0 := tags[0].(map[string]interface{})
    assert.Equal(t, "bug", tag0["slug"])
    assert.Equal(t, "Bug", tag0["name"])
    assert.Equal(t, "zinc", tag0["color"])

    tag1 := tags[1].(map[string]interface{})
    assert.Equal(t, "ui", tag1["slug"])
}

func TestUpdateTaskTags(t *testing.T) {
    ts := setupTestServer(t)

    // Create with one tag
    body := `{"title":"Test","description":"x","priority":1,"executor":"cli","tags":["bug"]}`
    resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
    var created map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&created)
    id := created["id"].(string)

    // Replace tags
    req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(`{"tags":["ui","frontend"]}`))
    req.Header.Set("Content-Type", "application/json")
    resp2, err := http.DefaultClient.Do(req)
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp2.StatusCode)

    var updated map[string]interface{}
    require.NoError(t, json.NewDecoder(resp2.Body).Decode(&updated))
    tags := updated["tags"].([]interface{})
    require.Len(t, tags, 2)
    assert.Equal(t, "ui", tags[0].(map[string]interface{})["slug"])
    assert.Equal(t, "frontend", tags[1].(map[string]interface{})["slug"])
}

func TestGetTaskIncludesTags(t *testing.T) {
    ts := setupTestServer(t)

    body := `{"title":"Test","description":"x","priority":1,"executor":"cli","tags":["bug"]}`
    resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
    var created map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&created)
    id := created["id"].(string)

    resp2, err := http.Get(ts.URL + "/api/v1/tasks/" + id)
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp2.StatusCode)

    var fetched map[string]interface{}
    require.NoError(t, json.NewDecoder(resp2.Body).Decode(&fetched))
    tags := fetched["tags"].([]interface{})
    require.Len(t, tags, 1)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/httpserver/... -run 'TestCreateTaskWithTags|TestUpdateTaskTags|TestGetTaskIncludesTags' -v`
Expected: fail because the `tags` field in responses is currently a string, not a `Tag[]`, OR the Create handler doesn't accept the `tags` field.

- [ ] **Step 3: Update `taskJSON` to include tags via `ListTaskTags`**

In `internal/httpserver/tasks.go`, find:

```go
func taskJSON(t *sqlstore.TaskRecord) map[string]interface{} {
    return map[string]interface{}{
        // ...
        "tags":            t.Tags,
        // ...
    }
}
```

This function needs access to the store to load tags. Options:
1. Pass the store through
2. Change the signature to `taskJSON(t *sqlstore.TaskRecord, tags []sqlstore.TagRecord) map[string]interface{}`
3. Make it a method on `*Server`

Use option 2 — cleanest. Update the function:

```go
// taskJSON converts a TaskRecord + its linked tags to a JSON-friendly map.
func taskJSON(t *sqlstore.TaskRecord, tags []sqlstore.TagRecord) map[string]interface{} {
    return map[string]interface{}{
        "id":              t.ID,
        "title":           t.Title,
        "description":     t.Description,
        "status":          t.Status,
        "priority":        t.Priority,
        "tags":            tagsJSON(tags),
        "manual":          t.Manual,
        "executor":        t.Executor,
        "agent_profile":   t.AgentProfile,
        "working_dir":     t.WorkingDir,
        "system_prompt":   t.SystemPrompt,
        "cost_budget":     nullFloat(t.CostBudget),
        "max_retries":     t.MaxRetries,
        "on_done":         t.OnDone,
        "on_fail":         t.OnFail,
        "on_review":       t.OnReview,
        "on_done_merge":   t.OnDoneMerge,
        "blocked_reason":  t.BlockedReason,
        "sprint_id":       nullStr(t.SprintID),
        "project_id":      nullStr(t.ProjectID),
        "epic_id":         nullStr(t.EpicID),
        "created_at":      t.CreatedAt,
        "updated_at":      t.UpdatedAt,
    }
}
```

Note: `tags` position in the map is moved to right after `priority` to match the existing field order.

- [ ] **Step 4: Update `tasksJSON` to batch-load tags**

In the same file:

```go
// tasksJSON converts a slice of TaskRecord to a JSON-friendly slice.
// Loads linked tags per-task (N+1 — acceptable at current scale).
func (s *Server) tasksJSON(tasks []sqlstore.TaskRecord) ([]map[string]interface{}, error) {
    out := make([]map[string]interface{}, len(tasks))
    for i := range tasks {
        tags, err := s.svc.Task.ListTags(tasks[i].ID)
        if err != nil {
            return nil, err
        }
        out[i] = taskJSON(&tasks[i], tags)
    }
    return out, nil
}
```

Note: this calls `s.svc.Task.ListTags(id)` — a new convenience method we need to add. Alternatively, call the store directly: `s.store.ListTaskTags(id)` if the server struct has a `store` field. Check what `s` exposes. If only `svc`, add a `ListTags` method on `TaskService`.

- [ ] **Step 5: Add `TaskService.ListTags` convenience method**

Append to `internal/service/task.go`:

```go
// ListTags returns the tags linked to a task.
func (s *TaskService) ListTags(taskID string) ([]sqlstore.TagRecord, error) {
    return s.store.ListTaskTags(taskID)
}
```

- [ ] **Step 6: Update all handlers in `tasks.go` that call `taskJSON` or `tasksJSON`**

Every call site needs to either pass the tags or use the Server-method version. Find each:

1. `listTasks` — uses `tasksJSON(tasks)`. Change to `s.tasksJSON(tasks)` (now a method) and handle the returned error:
   ```go
   out, err := s.tasksJSON(tasks)
   if err != nil {
       writeError(w, http.StatusInternalServerError, err.Error())
       return
   }
   writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out})
   ```

2. `getTask` — calls `taskJSON(task)`. Change to:
   ```go
   tags, err := s.svc.Task.ListTags(task.ID)
   if err != nil {
       writeError(w, http.StatusInternalServerError, err.Error())
       return
   }
   writeJSON(w, http.StatusOK, taskJSON(task, tags))
   ```

3. `createTask` — after successful create, load tags and return:
   ```go
   tags, _ := s.svc.Task.ListTags(task.ID)
   writeJSON(w, http.StatusCreated, taskJSON(task, tags))
   ```

4. `updateTask` — same pattern.

5. `transitionTask` — same pattern.

6. `searchTasks` — uses `tasksJSON`. Same as `listTasks`.

Do all six.

- [ ] **Step 7: Update the `createTask` handler to accept `tags: []string`**

Find the request struct in `createTask`:

```go
var req struct {
    Title       string   `json:"title"`
    Description string   `json:"description"`
    Priority    int      `json:"priority"`
    Tags        []string `json:"tags"`
    Executor    string   `json:"executor"`
    Manual      bool     `json:"manual"`
}
```

The `Tags` field is already `[]string` — good. The service-layer `TaskCreateInput.Tags` is also `[]string`. No shape change here; the plumbing in Task 10 already handles the auto-resolve on the service side.

- [ ] **Step 8: Update the `updateTask` handler to accept `tags: []string`**

Find the current handler:

```go
func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    var req map[string]interface{}
    if err := readJSON(r, &req); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
        return
    }

    update := sqlstore.TaskUpdate{}
    if v, ok := req["title"].(string); ok {
        update.Title = &v
    }
    // ... existing fields ...
```

After existing field parsing, add tag parsing:

```go
    input := service.TaskUpdateInput{TaskUpdate: update}
    if raw, ok := req["tags"]; ok {
        if arr, ok := raw.([]interface{}); ok {
            slugs := make([]string, 0, len(arr))
            for _, item := range arr {
                if s, ok := item.(string); ok {
                    slugs = append(slugs, s)
                }
            }
            input.Tags = &slugs
        }
    }

    if err := s.svc.Task.Update(id, input); err != nil {
        // ...
    }
```

- [ ] **Step 9: Run the task handler tests**

Run: `go test ./internal/httpserver/... -run 'TestCreateTaskWithTags|TestUpdateTaskTags|TestGetTaskIncludesTags' -v`
Expected: PASS.

- [ ] **Step 10: Run ALL httpserver tests to catch regressions in existing task handler tests**

Run: `go test ./internal/httpserver/... -v`
Expected: PASS everywhere. If existing `TestCreateAndGetTask` (note: this is the task test, not the tag test) fails because it asserts `tags` is a string, update it to assert `tags` is an empty array `[]`.

- [ ] **Step 11: Commit**

```bash
git add internal/httpserver/tasks.go internal/service/task.go internal/httpserver/server_test.go
git commit -m "feat(http): task responses include structured Tag[]; accept tags []string on write"
```

---

## Task 13: Run full backend test suite

**Files:** none — verification only.

- [ ] **Step 1: Run the entire backend test suite**

Run: `go test ./... 2>&1 | tail -60`
Expected: PASS everywhere.

- [ ] **Step 2: Run `go vet`**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 3: Rebuild the API via cerberus**

Run: use the cerberus MCP tool `cerberus_rebuild` with `service: torque-api` (or equivalent shell command if MCP isn't available).

Expected: rebuild succeeds, service restarts, healthcheck passes.

- [ ] **Step 4: Smoke-test the tag API manually**

Run:
```bash
curl -s http://localhost:8990/api/v1/tags | jq
curl -s -X POST http://localhost:8990/api/v1/tags \
  -H 'content-type: application/json' \
  -d '{"name":"Smoke Test","color":"green"}' | jq
curl -s http://localhost:8990/api/v1/tags/smoke-test | jq
curl -s http://localhost:8990/api/v1/tasks \
  -X POST -H 'content-type: application/json' \
  -d '{"title":"Tagged task","description":"test","priority":2,"executor":"cli","tags":["Bug","UI"]}' | jq
```
Expected: all return 2xx with the expected shapes.

- [ ] **Step 5: Commit nothing — this is verification only**

If any step fails, fix it and re-run. No commit if nothing changed.

---

## Task 14: Frontend — add Tag types and color constants

**Files:**
- Modify: `apps/gui/src/lib/types.ts`
- Modify: `apps/gui/src/lib/constants.ts`

- [ ] **Step 1: Add `Tag` and `TagColor` to `types.ts`**

Open `apps/gui/src/lib/types.ts`. Add after the existing `TaskStatus` type and before `Task`:

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
```

Then find the `Task` interface and change its `tags` field:

```ts
export interface Task {
  id: string
  title: string
  description: string
  status: TaskStatus
  priority: number
  tags: Tag[]         // was: string
  manual: boolean
  // ... rest unchanged
}
```

- [ ] **Step 2: Add `TAG_COLOR_CLASSES` to `constants.ts`**

Open `apps/gui/src/lib/constants.ts`. Add an import of the new type at the top (add to existing import if one exists, or add a new line):

```ts
import type { TagColor } from './types'
```

Append at the bottom of the file:

```ts
export const TAG_COLOR_CLASSES: Record<TagColor, string> = {
  zinc:   'bg-zinc-900 text-zinc-400 border border-zinc-800',
  red:    'bg-red-950 text-red-400 border border-red-900',
  orange: 'bg-orange-950 text-orange-400 border border-orange-900',
  amber:  'bg-amber-950 text-amber-400 border border-amber-900',
  green:  'bg-green-950 text-green-400 border border-green-900',
  teal:   'bg-teal-950 text-teal-400 border border-teal-900',
  blue:   'bg-blue-950 text-blue-400 border border-blue-900',
  violet: 'bg-violet-950 text-violet-400 border border-violet-900',
  pink:   'bg-pink-950 text-pink-400 border border-pink-900',
}
```

- [ ] **Step 3: Verify TypeScript compiles**

Run: `cd apps/gui && npx tsc --noEmit`
Expected: several errors in `task-row.tsx`, `TaskDetailPage.tsx`, `api.ts`, `utils.ts` because they still reference `task.tags` as a string or use `parseTags`. Do NOT fix them yet — they're addressed in Tasks 15–18.

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/lib/types.ts apps/gui/src/lib/constants.ts
git commit -m "feat(types): add Tag, TagColor, and TAG_COLOR_CLASSES palette"
```

---

## Task 15: Frontend — TagChip component

**Files:**
- Create: `apps/gui/src/components/domain/tag-chip.tsx`

- [ ] **Step 1: Create the TagChip component**

Create `apps/gui/src/components/domain/tag-chip.tsx`:

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

- [ ] **Step 2: Verify it compiles in isolation**

Run: `cd apps/gui && npx tsc --noEmit src/components/domain/tag-chip.tsx`
Expected: no errors in this file specifically. (The `--noEmit` whole-project check will still fail on other files until later tasks; that's fine.)

- [ ] **Step 3: Commit**

```bash
git add apps/gui/src/components/domain/tag-chip.tsx
git commit -m "feat(gui): add reusable TagChip component"
```

---

## Task 16: Frontend — update task row and task detail page

**Files:**
- Modify: `apps/gui/src/components/domain/task-row.tsx`
- Modify: `apps/gui/src/pages/TaskDetailPage.tsx`
- Modify: `apps/gui/src/lib/utils.ts`

- [ ] **Step 1: Update `task-row.tsx` to use `<TagChip>` with a 3-cap**

Open `apps/gui/src/components/domain/task-row.tsx`. It currently uses `parseTags(task.tags)` and renders inline spans. Replace with `<TagChip>` imports and usage. Note: the existing file may or may not render tags at all in the row cell — check the current content. If it doesn't, we just need to stop parsing.

Find the import block. Remove `parseTags` from the utils import and add `TagChip`:

```tsx
import { TagChip } from './tag-chip'
```

Find any line that reads `const tags = parseTags(task.tags)` and delete it.

If the row currently renders tag chips (based on the spec, tags are shown near the title), replace the rendering block with:

```tsx
{task.tags && task.tags.length > 0 && (
  <div className="mt-0.5 flex items-center gap-1">
    {task.tags.slice(0, 3).map((tag) => (
      <TagChip key={tag.slug} tag={tag} />
    ))}
    {task.tags.length > 3 && (
      <span className="text-[10px] text-zinc-500">
        +{task.tags.length - 3} more
      </span>
    )}
  </div>
)}
```

Place this block inside the "Title + executor subtitle" cell, below the existing executor subtitle. If the current row doesn't show tags at all (it only shows title + executor), that's fine — add the new block.

Reference: the cell currently looks like:
```tsx
<td className="px-3 py-1.5 text-left">
  <div className="min-w-0">
    <Link to={`/tasks/${task.id}`} ...>
      {task.title}
    </Link>
    {task.executor && (
      <span className="text-[10px] text-zinc-600">{task.executor}</span>
    )}
  </div>
</td>
```

After this change, the div should include the tags block below the executor span.

- [ ] **Step 2: Update `TaskDetailPage.tsx` to use `<TagChip>` without a cap**

Open `apps/gui/src/pages/TaskDetailPage.tsx`. Find:

```tsx
const tags = parseTags(task.tags)
```

Delete this line.

Find the tag rendering block:

```tsx
{tags.map((tag) => (
  <span key={tag} className="rounded px-1.5 py-0.5 text-xs bg-muted text-muted-foreground font-mono">
    {tag}
  </span>
))}
```

Replace with:

```tsx
{task.tags.map((tag) => (
  <TagChip key={tag.slug} tag={tag} />
))}
```

Add the import at the top:

```tsx
import { TagChip } from '@/components/domain/tag-chip'
```

Remove `parseTags` from any import on this page.

- [ ] **Step 3: Remove `parseTags` from `utils.ts`**

Open `apps/gui/src/lib/utils.ts`. Find the `parseTags` function and delete it entirely. Also delete any helper test/docs.

- [ ] **Step 4: Verify all references to `parseTags` are gone**

Run: `grep -rn parseTags apps/gui/src/`
Expected: no output (empty).

If any result appears, update that file to not use `parseTags`.

- [ ] **Step 5: Run TypeScript check**

Run: `cd apps/gui && npx tsc --noEmit`
Expected: no errors related to `tags`, `parseTags`, or `TagChip`. The API client (updated in Task 17) may still show errors on task create/update calls — that's expected.

- [ ] **Step 6: Commit**

```bash
git add apps/gui/src/components/domain/task-row.tsx apps/gui/src/pages/TaskDetailPage.tsx apps/gui/src/lib/utils.ts
git commit -m "feat(gui): task row and detail page use TagChip; remove parseTags"
```

---

## Task 17: Frontend — API client methods for tags

**Files:**
- Modify: `apps/gui/src/lib/api.ts`

- [ ] **Step 1: Add `Tag`, `TagColor` to imports**

Open `apps/gui/src/lib/api.ts`. Find the existing type imports block and add `Tag`, `TagColor`:

```ts
import type {
  Task,
  TaskFilter,
  Run,
  Artifact,
  Comment,
  SSEEvent,
  FeatureFlags,
  Project,
  Sprint,
  Epic,
  Tag,
  TagColor,
} from './types'
```

- [ ] **Step 2: Add tag CRUD methods to `TorqueApiClient`**

Find the comment marker `// Tasks` or the end of the Epics block and add a new section:

```ts
  // -------------------------
  // Tags
  // -------------------------

  async listTags(): Promise<{ tags: Tag[] }> {
    return this.get<{ tags: Tag[] }>('/tags')
  }

  async getTag(slug: string): Promise<Tag> {
    return this.get<Tag>(`/tags/${slug}`)
  }

  async createTag(data: {
    name: string
    slug?: string
    description?: string
    color?: TagColor
  }): Promise<Tag> {
    return this.post<Tag>('/tags', data)
  }

  async updateTag(slug: string, data: {
    name?: string
    description?: string
    color?: TagColor
  }): Promise<Tag> {
    return this.patch<Tag>(`/tags/${slug}`, data)
  }

  async deleteTag(slug: string): Promise<void> {
    return this.delete<void>(`/tags/${slug}`)
  }

  async mergeTags(sourceSlug: string, into: string): Promise<Tag> {
    return this.post<Tag>(`/tags/${sourceSlug}/merge`, { into })
  }
```

- [ ] **Step 3: Update `createTask` and `updateTask` signatures so `tags` is `string[]` on write**

Find:

```ts
async createTask(data: Partial<Task>): Promise<Task> {
  return this.post<Task>('/tasks', data)
}

async updateTask(id: string, data: Partial<Task>): Promise<Task> {
  return this.patch<Task>(`/tasks/${id}`, data)
}
```

Replace with:

```ts
async createTask(data: Partial<Omit<Task, 'tags'>> & { tags?: string[] }): Promise<Task> {
  return this.post<Task>('/tasks', data)
}

async updateTask(id: string, data: Partial<Omit<Task, 'tags'>> & { tags?: string[] }): Promise<Task> {
  return this.patch<Task>(`/tasks/${id}`, data)
}
```

- [ ] **Step 4: Run TypeScript check**

Run: `cd apps/gui && npx tsc --noEmit`
Expected: zero errors across the project.

If any callers of `createTask` / `updateTask` were passing `tags` as a string, update them to pass a string array instead. Grep for these:

Run: `grep -rn 'createTask\|updateTask' apps/gui/src/`

- [ ] **Step 5: Commit**

```bash
git add apps/gui/src/lib/api.ts
git commit -m "feat(gui): add tag API client methods; update task create/update signatures"
```

---

## Task 18: Smoke test the full stack

**Files:** none — verification only.

- [ ] **Step 1: Run the frontend typecheck once more**

Run: `cd apps/gui && npx tsc --noEmit`
Expected: clean.

- [ ] **Step 2: Run the frontend lint if configured**

Run: `cd apps/gui && npm run lint` (if the script exists)
Expected: clean or pre-existing warnings only.

- [ ] **Step 3: Restart the frontend dev server via cerberus**

Use the cerberus MCP tool `cerberus_restart` with `service: torque-frontend`.

Expected: the frontend restarts; watch the cerberus log for errors.

- [ ] **Step 4: Visual verification in the browser**

Open the Torque GUI (typically at `http://localhost:5175`):

1. **Tasks board** — navigate to the board. Create a new task via the API (or use the UI if there's a form) with tags `["Bug", "UI", "Frontend", "High Priority"]`. Verify:
   - The row shows 3 tag chips with color styling (all zinc by default)
   - A `+1 more` indicator appears at the end
   - Each chip has the correct text

2. **Task detail page** — click into the task. Verify:
   - All 4 tags render as chips
   - No cap applied
   - Colors match (all zinc since we didn't specify)

3. **Change colors** — use curl to update one of the tags to a different color:
   ```bash
   curl -s -X PATCH http://localhost:8990/api/v1/tags/bug \
     -H 'content-type: application/json' \
     -d '{"color":"red"}' | jq
   ```
   Refresh the board and detail view. The `Bug` chip should now render red.

4. **Merge** — create another task tagged `["bug"]` and a tag called `defect`:
   ```bash
   curl -s -X POST http://localhost:8990/api/v1/tags \
     -H 'content-type: application/json' \
     -d '{"name":"Defect","color":"red"}' | jq
   curl -s -X POST http://localhost:8990/api/v1/tags/bug/merge \
     -H 'content-type: application/json' \
     -d '{"into":"defect"}' | jq
   ```
   Refresh the board. Previously-bugged tasks should now show `defect`.

- [ ] **Step 5: No commit — verification only**

If any visual issue surfaces, fix in a targeted commit with a clear message. Most issues will be styling tweaks in `tag-chip.tsx` or `constants.ts`.

---

## Success criteria (from spec Section 15)

- Migration 005 applies cleanly on a fresh database ✓
- `go test ./...` passes ✓
- POST /api/v1/tasks with `"tags": ["Bug", "UI"]` returns a task with `Tag[]` ✓
- GET /api/v1/tags returns both tags ✓
- POST /api/v1/tags/bug/merge with `{"into": "defect"}` works ✓
- Tasks board caps tag chips at 3 with `+N more` ✓
- TaskDetailPage renders all tags uncapped ✓
- No `parseTags` references remain in `apps/gui/` ✓

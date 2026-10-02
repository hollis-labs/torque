# Task title ordering: CW-20261001-0679

Title is an allowed task sort on HTTP `/tasks`, `/tasks/search` and MCP `torque_task_list`. SQLite uses `title COLLATE NOCASE`; PostgreSQL uses ASCII `translate(title, 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz') COLLATE "C"` so case folding and ordering do not depend on the database locale. Non-ASCII characters retain SQLite NOCASE's existing case-sensitive behavior. Both ascending and descending title order use `id ASC` for ties. Cursors retain the original title and bind to the field/direction; the store compares the same key for the cursor and ORDER BY. HTTP explicit offset mode continues to work; MCP retains its cursor-only argument contract.

## Index decision

No migration is added. At the requested current scale of 4,300 synthetic tasks, unindexed full-projection task pages take about 2–4 ms on this host. A fixture-only title index improves this by a few milliseconds but adds maintenance/storage overhead for an optional sort, without evidence of user-visible latency at this scale. Revisit with measured production query latency or a larger cohort rather than adding an index speculatively.

The probe uses a temporary file-backed SQLite database with current migrations, transactionally inserts 4,300 automatic todo tasks with mixed-case title ties and priorities, then creates a candidate index **only in that fixture**. Each timing calls the real `Store.ListTasks` with 51 full records (the public 50-row page plus continuation probe), default internal visibility, and title ascending. Five warmups precede 100 timed reads. Title cursors and first pages are measured separately. Reader schema is refreshed after adding the candidate before EXPLAIN.

| Fixture | Query | EXPLAIN QUERY PLAN | Median | p95 |
|---|---|---|---:|---:|
| Existing indexes | First page | SCAN tasks; USE TEMP B-TREE FOR ORDER BY | 2.13 ms | 4.03 ms |
| Existing indexes | Cursor page | SCAN tasks; USE TEMP B-TREE FOR ORDER BY | 2.75 ms | 3.95 ms |
| Candidate `(title COLLATE NOCASE, id)` | First page | SCAN tasks USING INDEX fixture_title | 0.77 ms | 0.91 ms |
| Candidate `(title COLLATE NOCASE, id)` | Cursor page | SCAN tasks USING INDEX fixture_title | 1.00 ms | 1.77 ms |

EXPLAIN uses `SELECT * FROM tasks WHERE kind != 'internal' ORDER BY title COLLATE NOCASE ASC, id ASC LIMIT 51`; the cursor form adds `(title COLLATE NOCASE > ? OR (title COLLATE NOCASE = ? AND id > ?))`. Timings use the actual store projection, not the EXPLAIN projection. These are warm local fixture measurements, not production or PostgreSQL latency claims.

The authored [probe source](task-title-sort-probe.go.txt) is retained as an artifact outside the compiled packages. To reproduce from a disposable worktree, copy it into a temporary `internal/title_sort_probe/main.go`, run `GOMAXPROCS=2 GOFLAGS=-p=2 go run ./internal/title_sort_probe`, then remove that temporary source directory. The probe removes its fixture database on exit. It never opens live Torque data.

## Verification

`TestTaskTitleSortHTTPMCP` inserts deliberately scrambled IDs/titles (including empty and mixed-case titles), checks both directions, continues one row per cursor across case-insensitive ties, verifies sort/direction mismatch rejection, verifies list/search/MCP row/continuation/total parity, and checks explicit HTTP offsets 0 and 2. The Board regression test verifies title sort resets cursor paging and passes `sort_by=title` to the server.

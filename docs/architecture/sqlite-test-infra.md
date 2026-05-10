# SQLite Test Infra

This note records the Phase 0 test-infra change for `CW-20260510-0111`.

## Problem

Several integration fixtures used `:memory:` SQLite plus a blanket `SetMaxOpenConns(1)`.
That kept tests green, but it also hid the production failure mode: pooled SQLite writes contending on the single writer lock and surfacing `SQLITE_BUSY`.

## New Default

Shared store fixtures should use `internal/testutil/sqlitetest`:

- `sqlitetest.OpenStore(t)` opens a temp-file-backed SQLite database in WAL mode.
- The default pool is the normal `database/sql` pooled behavior.
- `sqlitetest.WithMaxOpenConns(1)` is the explicit opt-in for tests that truly need a single connection.
- `sqlitetest.WithBusyTimeout(...)` exists for stress tests that need a tighter timeout window.

The Phase 0 audit moved the old blanket single-conn fixtures in:

- `cmd/clockwork/smoke_templates_test.go`
- `internal/mcpadapter/scheduler_tools_test.go`
- `internal/persistence/sqlstore/tasks_test.go` (`setupTestStore`)
- `internal/runtime/agent/session_lifecycle_hook_test.go`
- `internal/runtime/scheduler/*` integration fixtures that previously hard-coded `SetMaxOpenConns(1)`
- `internal/worktree/manager_test.go`
- `internal/worktree/resolver_test.go`

No audited call site still needs a forced single-connection pool today. The opt-in remains for future migration/admin-only tests where exclusive access is the behavior under test.

## Phase 1 Gate

`internal/persistence/sqlstore/concurrency_test.go` is intentionally tagged out of the default test run until `CW-20260510-0112` lands:

```sh
go test -tags=sqlite_concurrency ./internal/persistence/sqlstore -run TestConcurrentWritePaths_NoSQLITEBusy -count=1
```

That test hammers `AppendRunEvent` and `CreateSession` concurrently on a pooled SQLite handle. On current main it fails with `SQLITE_BUSY`; after the single-writer/read-pool split it should become a required green gate.

# SQLite Concurrency Pattern

This is the current Clockwork pattern for running SQLite under concurrent API,
scheduler, worker, and telemetry traffic. It is the canonical reference for
Nanite, Vanta, Hadron, and other local-first apps that want SQLite without
`SQLITE_BUSY` becoming normal runtime noise.

## Problem

SQLite allows many concurrent readers, but it still has one writer. WAL mode
keeps readers from blocking writers in the common case; it does not make
multiple write transactions safe to race through a pooled `*sql.DB`.

Clockwork previously had several independent write paths:

- scheduler dispatch and lifecycle state changes
- agent/session state changes
- run event append traffic
- cost ledger writes
- comments created through HTTP/MCP loopback

Those paths could all reach the same SQLite file at the same time. The fix was
not to hide the issue with `SetMaxOpenConns(1)` everywhere; that preserves
correctness by disabling concurrency. The fix was to make write ownership
explicit.

## Clockwork Shape

Clockwork now uses three layers:

- A SQLite store with a dedicated single-connection writer pool and a separate
  read pool.
- An in-memory serialized state writer for correctness-critical writes that
  callers need to observe synchronously.
- A durable queue-backed telemetry writer for hot append-only or low-priority
  writes.

The runtime still has direct fallbacks for tests and narrow call sites, but the
server path wires the queued writers at bootstrap.

## SQLite DSN And Pools

All SQLite handles should be opened through `appdb.SQLiteDSN`. It applies the
same pragmas everywhere and handles relative paths correctly:

```go
dsn := appdb.SQLiteDSN("clockwork.db", appdb.SQLiteDSNOptions{
	BusyTimeoutMs:    appdb.DefaultSQLiteBusyTimeoutMs,
	IncludeCacheSize: true,
	TxLock:           "immediate",
})
db, err := sql.Open("sqlite", dsn)
```

The relative-path detail matters. Relative SQLite URIs must use the
`file:clockwork.db?...` form, not `file://clockwork.db?...`.

`sqlstore.New` turns the bootstrap DB into the runtime shape:

```go
writeDB, err := sql.Open("sqlite", appdb.SQLiteDSN(path, appdb.SQLiteDSNOptions{
	BusyTimeoutMs:    busyTimeoutMs,
	IncludeCacheSize: true,
	TxLock:           "immediate",
}))
writeDB.SetMaxOpenConns(1)
writeDB.SetMaxIdleConns(1)

readDB, err := sql.Open("sqlite", appdb.SQLiteDSN(path, appdb.SQLiteDSNOptions{
	BusyTimeoutMs: busyTimeoutMs,
}))
readDB.SetMaxOpenConns(appdb.DefaultSQLiteMaxReadConns)
readDB.SetMaxIdleConns(appdb.DefaultSQLiteMaxReadConns)
```

The writer pool is intentionally single-connection and uses
`_txlock=immediate`, so write lock acquisition happens at transaction start
instead of after partial work. The read pool stays pooled, so HTTP and scheduler
reads do not serialize behind state writes.

Migrations still run before `sqlstore.New`, using the bootstrap connection.
That keeps startup/admin work separate from the steady-state runtime pools.

## Serialized State Writes

State writes are writes where the caller needs a committed result before
continuing: task transitions, run creation/completion, session state, retry
counters, and similar scheduler decisions.

Clockwork routes those through `internal/runtime/writeq`:

```go
stateWriter := writeq.New(store, writeq.Options{})
sched.SetStateWriter(stateWriter)

go func() {
	if err := stateWriter.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("[serve] state write queue stopped: %v", err)
	}
}()

agentDeps, closeAgentDeps, err := bootstrap.AgentDeps(
	store,
	profiles,
	svc,
	tools,
	sched.EventBus(),
	stateWriter,
)
```

`writeq.Queue.Submit` blocks until the operation has been committed or failed:

```go
err := stateWriter.Submit(ctx, "scheduler_dispatch", func(tx *sqlstore.WriteTx) error {
	if err := tx.TransitionTask(task.ID, "doing"); err != nil {
		return err
	}
	runID, err := tx.CreateRun(&sqlstore.RunRecord{
		TaskID:   task.ID,
		Executor: task.Executor,
		Status:   "running",
	})
	if err != nil {
		return err
	}
	_, err = tx.AppendRunEvent(&sqlstore.RunEventRecord{
		RunID:  sql.NullInt64{Int64: runID, Valid: true},
		TaskID: task.ID,
		Type:   "run_started",
	})
	return err
})
```

The queue worker batches compatible operations into one write transaction. Each
operation runs under a savepoint, so one failed operation does not poison the
whole batch. After-commit hooks are held until the outer transaction commits.

Use this layer when:

- the caller needs the database mutation to be durable before it proceeds
- the write changes scheduler-visible state
- ordering matters across related writes
- failure should be returned to the caller immediately

Do not use it for fire-and-forget event streams where caller progress should
not depend on SQLite latency.

## Durable Telemetry Queue

Telemetry-class writes are high-volume writes that should not contend with
scheduler state transitions: run events, cost ledger rows, and comments.

Clockwork uses `internal/persistence/writequeue` for that layer:

```go
telemetryDB, err := writequeue.OpenDB(filepath.Join(cfg.DataDir, "queue.db"))
if err != nil {
	return err
}
telemetryWriter, err := writequeue.New(store, telemetryDB, writequeue.DefaultConfig())
if err != nil {
	return err
}

svc.Comment.SetTelemetryWriter(telemetryWriter)
sched.SetTelemetryWriter(telemetryWriter)

go func() {
	if err := telemetryWriter.Start(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("telemetry writequeue: %v", err)
	}
}()
```

The queue database is a separate SQLite file. It is still opened with the same
WAL and busy-timeout pragmas, but it absorbs bursts before draining into the
main store.

Most telemetry methods enqueue and return:

```go
err := telemetryWriter.AppendRunEvent(ctx, &sqlstore.RunEventRecord{
	RunID:  sql.NullInt64{Int64: runID, Valid: true},
	TaskID: taskID,
	Type:   "log",
})
```

Comments are the exception because HTTP/MCP callers need the persisted `id` and
`created_at`. `CommentService.Add` therefore waits on the telemetry writer with
a bounded timeout:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if err := s.telemetryWriter().AddComment(ctx, rec); err != nil {
	return nil, err
}
```

Use this layer when:

- the write is append-only telemetry or audit-like data
- burst absorption matters more than immediate query visibility
- the caller can tolerate async persistence, or can wait with a bounded timeout

## Testing Rules

Concurrency tests should use temp file-backed SQLite databases in WAL mode.
Avoid `:memory:` for store-level concurrency tests because each connection gets
a different database unless special shared-cache handling is used. Avoid blanket
`SetMaxOpenConns(1)` in fixtures because it hides production contention.

Use `internal/testutil/sqlitetest` for Clockwork tests:

```go
store := sqlitetest.OpenStore(t)
```

The concurrency gate is:

```sh
go test -tags=sqlite_concurrency ./internal/persistence/sqlstore -run TestConcurrentWritePaths_NoSQLITEBusy -count=1
```

The useful smoke signal is not just "the test passes"; it is "the test passes
without `SQLITE_BUSY` while using a pooled file-backed DB."

## Shared Package Assessment

This is a candidate for a shared package, but not as a direct export of
Clockwork's current packages. The current implementation mixes reusable
SQLite mechanics with Clockwork-specific domain helpers.

Good shared-package candidates now:

- `sqlitekit`: DSN builder, relative-path handling, WAL/busy-timeout pragmas,
  writer/read pool opening, and connection defaults.
- `serialwrite`: a generic in-memory write serializer that accepts
  `func(ctx context.Context, tx *sql.Tx) error`, supports batching, savepoints,
  stats, and direct fallback mode.
- `sqlitequeue`: durable queue scaffolding around `go-queue`, where each app
  owns job types and handlers.

Keep app-specific:

- migrations and schema ownership
- typed transaction helpers such as `CreateRun`, `TransitionTask`, and
  `AppendRunEvent`
- telemetry payload schemas and acknowledgement semantics
- lifecycle/bootstrap wiring

Recommendation: extract `sqlitekit` first because it is low-risk and already
validated by Clockwork. Extract the generic serialized writer after one more app
adopts the pattern, so Nanite or Vanta can force the interface to be app-neutral
before it becomes shared API. Treat the durable telemetry queue as a reusable
scaffold, not a shared domain package.

## Older Concurrency Package

Clockwork still has an older `internal/concurrency` package with DB pool,
write serializer, and buffered-event prototypes. It is not the server's current
canonical concurrency layer. New work should copy the pattern documented here:
`appdb` + `sqlstore` read/write split + `runtime/writeq` +
`persistence/writequeue`.

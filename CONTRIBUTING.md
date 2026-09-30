# Contributing

How a change gets from your clone into `main`. This is deliberately short: most
of what you need is written somewhere closer to the thing it describes.

## Before your first change

`README.md` has the commands and configuration, and `docs/README.md` indexes
the current docs. `AGENTS.md` is the fastest orientation to the layout and the
boundaries that are not obvious from reading the code. Run with a scratch
database (`TORQUE_DB_PATH=/tmp/torque-dev.db`) while you develop; `torque
serve` binds loopback by default.

## The sequence

1. **Branch.** `<type>/<short-slug>`, where the type matches the change —
   `feat`, `fix`, `docs`, `chore`. Nothing enforces this; it is what the
   history does.
2. **Change one thing.** A branch carrying two unrelated changes costs the
   reviewer the ability to accept one and question the other.
3. **Run the checks** before you push:
   ```
   make lint && make test
   ```
   Use `make test-scheduler` for a fast loop on `internal/runtime/...`, and
   `make build-prod` if you touched the GUI in `apps/gui/`.
4. **Push and open a pull request.** A maintainer will review it.

Commit subjects follow the conventional-commit shape — a type, an optional
scope, a colon, then the summary.

## What a pull request should carry

The reviewer was not there when you made the decisions. State what the change
does, what it deliberately leaves alone, and the evidence that it works — the
commands you ran and what came back, not a claim that it passes.

Add a line to `CHANGELOG.md` under `[Unreleased]`.

## The one that cannot be undone

**Migrations.** Migrations under `internal/persistence/sqlstore/migrations/`
are keyed by filename and applied once, so editing one that has landed changes
nothing on an existing database, while renumbering one can break it. Add a new
numbered file, and test it against a copy of an older database, not only a fresh
one. Task history lives in that store.

## Things that surprise people

- **Task transitions are permissive by design.** Any status in
  `CanonicalStatuses` reaches any other; only a status outside the vocabulary
  and leaving `done`/`archived` are refused. `ForceTransition` does not bypass
  the vocabulary.
- **`CanonicalStatuses` and the GUI's `TaskStatus` union change together.**
- **Do not add a SQLite write path** or answer `SQLITE_BUSY` with
  `SetMaxOpenConns(1)`; write ownership is already handled in `sqlstore`.
- **MCP feature flags are read at process start.** Restart the MCP process
  after toggling one, or `tools/list` will not change.
- **`make profiles-lint` validates operator state** (`profiles.yaml`), so it can
  fail on a clean checkout.
- **Older docs cite paths that no longer exist.** A path in a document is not
  evidence the path is there.

## What this does not cover

- **Which change is worth making.** Open an issue to discuss larger features
  before building them.
- **Releases and deployment.** Maintainers handle those.

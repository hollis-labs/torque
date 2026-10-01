# Changelog

All notable changes to Torque are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Pre-1.0: minor bumps for additive surface, patch bumps for fixes — breaking changes can land in any minor. Backfilled from tags and `git log`; good-faith, not exhaustive. Torque was renamed from Clockwork, so some older entries' task IDs (`CW-…`) are historical.

## [Unreleased]

### Added

- `GET /api/v1/tasks/rollup?group_by=project_id|epic_id|sprint_id` counts
  tasks per scope and status in one query, and `GET /api/v1/tasks?fields=summary`
  leaves out each task's `description` and `system_prompt`. The GUI's
  Projects, Epics and Sprints pages use the rollup instead of paging every
  task (17 MB in 78 requests on a 4,097-task store, now one 3 KB request), and
  the scope detail pages use the summary list.
- Open-source project documents: `CHANGELOG.md`, `CONTRIBUTING.md`,
  `SECURITY.md`, `TRADEMARK.md`; MIT `LICENSE`.

### Changed

- `agentkit` v0.11.1: a child that prints its last lines and exits at once
  no longer has them dropped (jsonrpc-stdio, serve-http, PTY).
- `go-agent-wrapper` v0.14.0 and `agentkit` v0.11.0 (adds `go-permission`
  v0.1.0; `go-providers` stays v0.30.0). No behaviour change in Torque: the
  wrapper's new `PermissionPosture` answers Codex app-server approvals, but
  Torque runs codex on its own agentsessions path, not through the wrapper.
- GUI pages refresh on task events at most once per 1.5 s burst instead of
  on every event. The Ops Dashboard patches task status in place from
  transition events and refetches only the tasks named by other events. Scope
  detail pages show their tasks a page at a time, most recently updated
  first, with counts from the rollup. Task lists ask for 200-row pages instead
  of the server's default 50.
- The MCP server adopted `go-mcp` (official SDK), dropping `mark3labs/mcp-go`.
- Completed task tracking and design history archived out of the repository;
  README rewritten as a pre-release identity and stack-fit document.
- `go-queue` dependency moved off its retired `v0.1.1` tag.
- agentkit v0.10.0, go-providers v0.30.0 and go-agent-wrapper v0.13.0 (were
  v0.6.1, v0.26.0, v0.10.1); go-sandbox follows to v0.4.0. OpenCode runs now
  use `opencode run --format json`, so Torque receives its tool calls,
  per-step token usage and a done event per turn instead of plain-text lines.
  The OpenCode boot dir defines the agent in `agents/<name>.md` frontmatter
  and no longer plants `agents.json` or an `opencode.json` agent block.
- go-agent-wrapper v0.13.1: a turn's usage now arrives on its single terminal
  event instead of a second `turn.completed`. The wrapper event sink reads
  usage from `turn.completed` and `turn.failed` and still emits the turn's
  done event; without that, a wrapper-path ModeOneShot run timed out waiting
  for its turn to finish.

### Fixed

- A task can no longer be created or updated with an executor this Torque
  process has not registered: HTTP answers 422 and MCP `arg_invalid`,
  naming the registered executors (`api`, `cli`, `mock`). An empty executor
  still means the default, and rows already carrying an unregistered
  executor stay editable. `torque mcp` validates against the same names as
  `torque serve`; a process with no executor registry does not validate.
- Tests can no longer run a real agent CLI. The wrapper-boot e2e fixture was
  found only through `CLAUDE_CLI_PATH`, while the wrapper path resolves a bare
  `claude` through PATH, so `make test` ran the developer's real Claude Code
  (a paid model call per run) and a bootstrap test reached the real
  `opencode`. Every package that can reach a launcher now installs refusing
  shims for the agent CLIs first on PATH (`testenv.RunWithAgentShims`).
- `make test` passes on Linux. The `torque-apikey-helper` resolver tests ran
  against a fake keychain but were refused off macOS before reaching it; the
  macOS-only gate now sits on the real keychain accessor. Tests that slept a
  fixed time and then asserted on asynchronous work (per-run worktree
  dispatch, serve shutdown under `-race`, long-lived task deadlines) wait on
  the condition or allow Boot real headroom instead.
- On the go-agent-wrapper path, `Manager.Wait` after `Manager.Stop` waits for
  the run to end. Stop dropped the session's wrapper handle, so Wait returned
  at once and the session row could still read `running`.
- Dispatched workers are told the repository's configured remote is the
  only push target: never add, guess or repoint a remote, and stop with
  `torque_task_blocked` and the evidence on unrelated history or someone
  else's commits instead of resetting or force-pushing. The engine now
  snapshots the run's remotes and HEAD before the worker boots and parks a
  run in `blocked` when a remote was added, removed or repointed, or HEAD
  shares no history with where it started. A worker had inferred a remote
  from the project name and opened a PR that would have wiped an unrelated
  app.
- A manual task in `doing` no longer holds its project's scheduler slot. The
  picker never dispatches manual tasks, so one being worked outside the
  scheduler kept every dispatchable task in its project at `project_busy`.
- Stuck-task recovery no longer resets manual tasks. A manual task is never
  dispatched, so at `doing` it has no worker heartbeat; the health scan
  reported it as `task_doing_no_worker` every tick and re-queued it to `todo`
  once it aged past `TORQUE_SCHED_STUCK_GRACE`. Manual tasks are now neither
  reported nor recovered.
- `make build-prod` embeds the GUI on Linux: it copied `apps/gui/dist/`, which
  GNU cp nests as `dist/dist`, so the binary 404'd on `/`. `make install` now
  installs that GUI-embedded build, to an overridable `BINDIR`, and `make gui`
  installs with `npm ci` so a build no longer dirties the lockfile.
- Every HTTP messaging body takes an address the same way: `from`/`to` on
  `/broker/send`, `/broker/request` and `/messages`, and `recipient` on
  `/messages/{id}/consume`, accept the `msg://<kind>/<authority>/<id>` string
  or a `{"kind","authority","id"}` object. Any other shape is a 400 that names
  the field and both forms; an absent or null address is a 422.
- `go test` no longer depends on TORQUE_* in the shell it runs in: the
  config, agent and `cmd/torque` tests clear them first, so a dispatched
  worker or a dev shell with Torque settings exported gets the same results.
- MCP write paths reject a non-integer priority.
- Operator pause is recorded as cancellation; task deadlines are enforced for
  long-lived runs; parent-owned task review is allowed.
- Forced single-comment delete; artifact metadata and partial updates;
  initial project status on create; relative artifact paths resolve from the
  task workdir; session env and meta exposed over MCP.
- Scheduler tests updated for `CancelCauseFunc`.
- Long-lived run verification grades every runtime by one rule: commits pass;
  an uncommitted diff (not a Bash call) is "edits but no commits"; a clean
  worktree passes when the worker made tool calls or left comments or
  artifacts on its task. Read-only claude-code runs are no longer graded
  blocked or failed.
- Long-lived claude-code runs record token usage. claude reports usage only
  in a turn's final `result` event, and a worker ends its run with a tool
  call that moves its task to review, so the session was stopped before that
  event and the run recorded 0/0. A streaming-stdio session now gets up to
  30s to finish the open turn before it is stopped. Cost stays 0: no pricing
  is applied to these runs, as for codex.
- A long-lived run graded "edits but no commits" now parks its task in
  `blocked` instead of retrying under `on_fail`. The reason names the
  preserved worktree, how many paths are uncommitted, and the remedy: commit
  or discard them there, then re-queue. A retry used to re-dispatch at once
  into a fresh worktree off `origin/main`, stranding the diff and holding the
  project's slot.

## [0.3.0] - 2026-05-17

### Added

- The GUI is embedded into the binary: one binary, one port.
- Dispatch moved onto the extended launch engine (`go-agent-launch`), with a
  headless dispatch mode.

### Fixed

- Reverted a broken Collections page rewrite in the GUI.

## [0.2.0] - 2026-05-17

### Added

- Torque-controlled permission mode for spawned Claude agents; repo-aware
  per-run worktree placement with a git precheck; an agent execution
  environment contract.
- End-to-end fixes so Codex and Claude runs work under orchestration.

### Changed

- Dead multi-agent worktree subsystem removed; profile lint recognizes
  `agent_profile_aliases`; pre-release developer-experience fixes.

## [0.1.1] - 2026-05-09

### Added

- Orchestrator self-stop, per-session PID polling, role-aware MCP loopback for
  orchestrator-class roles, and MCP output sanitization middleware.

### Changed

- Version bump to 0.1.1.

## Pre-release history

- **April 2026.** Initial task FSM, persistent queue and scheduler, executors,
  SQLite store, HTTP and MCP surfaces, and the first GUI.

[Unreleased]: https://github.com/hollis-labs/torque/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/hollis-labs/torque/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/hollis-labs/torque/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/hollis-labs/torque/releases/tag/v0.1.1

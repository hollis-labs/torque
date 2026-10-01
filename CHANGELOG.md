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

- The MCP server adopted `go-mcp` (official SDK), dropping `mark3labs/mcp-go`.
- Completed task tracking and design history archived out of the repository;
  README rewritten as a pre-release identity and stack-fit document.
- `go-queue` dependency moved off its retired `v0.1.1` tag.

### Fixed

- `make build-prod` embeds the GUI on Linux: it copied `apps/gui/dist/`, which
  GNU cp nests as `dist/dist`, so the binary 404'd on `/`. `make install` now
  installs that GUI-embedded build, to an overridable `BINDIR`, and `make gui`
  installs with `npm ci` so a build no longer dirties the lockfile.
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

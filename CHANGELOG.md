# Changelog

All notable changes to Torque are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Pre-1.0: minor bumps for additive surface, patch bumps for fixes — breaking changes can land in any minor. Backfilled from tags and `git log`; good-faith, not exhaustive. Torque was renamed from Clockwork, so some older entries' task IDs (`CW-…`) are historical.

## [Unreleased]

### Added

- Open-source project documents: `CHANGELOG.md`, `CONTRIBUTING.md`,
  `SECURITY.md`, `TRADEMARK.md`; MIT `LICENSE`.

### Changed

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

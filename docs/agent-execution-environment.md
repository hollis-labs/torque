# Agent execution environment

This document is the contract for **how a Torque-orchestrated agent run is
set up** — the directories it gets, the git worktree it may run in, and the
permission posture it boots with. It exists because orchestrated runs
repeatedly hit a *new* environment / permission / worktree failure each time
(CW-20260517-0038); the fix was to make the environment predictable and
documented rather than patched per-incident.

Scope: agents dispatched by Torque's scheduler through `agent.Boot`. The
investigation and rationale behind each rule live in
[`artifacts/CW-20260517-0038/fix-plan.md`](../artifacts/CW-20260517-0038/fix-plan.md).

## Directory model

Every run resolves four roots — `repo_root`, `work_root`, `workspace_dir`,
`build_dir`. They are defined in [`workspace-model.md`](./workspace-model.md);
that document is authoritative for the model itself. This page only covers
the things that were unpredictable: **which `work_root` the agent gets**
(shared dir vs. per-run worktree), **how the agent learns where that is**, and
**the permission posture it boots with**.

### Spawn cwd vs. work_root — the `TORQUE_WORK_ROOT` contract

A spawned agent's process working directory is the **planted boot dir** — an
ephemeral directory (`.claude/settings.json`, `AGENTS.md`, MCP config, …) that
is reaped after the run. It is deliberately *not* the run's `work_root`: the
provider CLI auto-loads its planted config from cwd, so cwd must be the boot
dir.

Consequently a prompt that tells the agent to "create a file" with a
**relative** path writes into the boot dir and the output is lost when the dir
is reaped. To make the real target predictable, Torque injects two env vars
into every spawned agent (alongside `TORQUE_TASK_ID` / `TORQUE_RUN_ID`):

| Env var | Value |
|---|---|
| `TORQUE_WORK_ROOT` | the run's `work_root` — the per-run worktree in worktree mode, else the repo checkout. **Deliverables belong here.** |
| `TORQUE_REPO_ROOT` | the canonical repo checkout (`repo_root`). Equals `TORQUE_WORK_ROOT` in shared mode. |

Agents (and the prompts that drive them) should resolve output paths against
`$TORQUE_WORK_ROOT` rather than relying on cwd. codex binds the thread to
`work_root` (`thread/start.cwd` for `jsonrpc-stdio`; `--cd` for exec mode), so
its *effective* directory is the work_root. claude only receives
`--add-dir $work_root` (an access grant, not a cwd change), so for claude the
env var is the authoritative pointer.

## Planted task bundle

For scheduler-dispatched tasks, Torque also plants a task bundle into the
provider boot dir before the agent starts. This is intentionally local,
read-only boot context: workers should not burn first-turn MCP calls merely to
rediscover the task, run, project, or session IDs they were just booted with.

Current layout:

```text
tasks/
  README.md
  <safe-task-segment>/
    task.md
    task.json
    process.md
```

`task.md` is the human-readable task brief. `task.json` is the structured
copy. `process.md` is the minimal worker process and completion guidance.
`tasks/README.md` points to the assigned bundle.

The bundle includes the original `task_id`, title, description, task kind,
status-at-boot, priority, run id, local Torque session id, agent profile, role,
relationship IDs (`project_id`, `parent_id`, `sprint_id`, `epic_id`,
`depends_on`), `work_root`, `repo_root`, and the task-scoped loopback URL. When
project context is available, Torque plants a sanitized subset:
project identity, repo/agent paths, read/write/context paths, rules, and
artifact summaries. Arbitrary task metadata is not planted because native boot
files are persisted in the launch plan and must not carry secrets.

The directory name under `tasks/` is a safe path segment derived from the task
ID. Normal Torque IDs are used as-is; any task ID that is not safe as one
filename segment is replaced with a stable hash-based segment. The original
task ID remains inside `task.md`, `task.json`, and `process.md`.

Provider cwd matters:

- Claude and Codex resolve `tasks/README.md` relative to the boot dir during
  boot file loading.
- Opencode runs with process cwd set to the project dir, so use
  `$OPENCODE_CONFIG_DIR/tasks/` to inspect the planted bundle from tools or
  shell commands.

Fresh task state and mutations still go through the task-scoped
`torque_loopback` MCP server. The planted bundle is the boot-time assignment,
not a replacement for updates, checkpoints, summaries, review transitions, or
blocked transitions.

## Per-run worktree contract

By default an agent runs directly in `repo_root` (`work_root == repo_root`,
"shared mode"). Per-run git worktrees are **opt-in**.

| Env var | Default | Effect |
|---|---|---|
| `TORQUE_WORKTREE_PER_RUN` | `false` | When `true`, each run gets its own git worktree as `work_root`. |
| `TORQUE_WORKTREE_ROOT` | empty | Explicit parent dir for run worktrees. Empty = the placement rule below. |
| `TORQUE_WORKTREE_PRECHECK` | scheduler default (`block`) | Dispatch-time git-repo gate: `off` / `warn` / `block`. |
| `TORQUE_WORKTREE_KEEP_DAYS` | `7` | Retention for swept per-run worktrees. |

### Placement rule — sibling depth

A per-run worktree is placed as a **true sibling of the repo root, at the
same directory depth**:

```
<repoParent>/<repoName>                       ← repo_root
<repoParent>/<repoName>-worktrees-run-<runID>  ← per-run work_root
```

The depth matters. A relative `replace ../../libs/...` directive in `go.mod`
resolves from the location of the worktree's `go.mod`. If the worktree sits
*deeper* than the repo (the old `<repo>-worktrees/run-<id>` layout, or Claude
Code's `.claude/worktrees/` layout), `../../` resolves to the wrong tree and
the build breaks. A same-depth sibling makes every relative `replace` resolve
identically from the worktree and from the repo.

When `TORQUE_WORKTREE_ROOT` is set, that path is honored as-is
(`<root>/run-<runID>`) — the operator owns the depth in that case.

### Relative-replace guard

Before a worktree is used, Torque parses the repo's `go.mod`. If it contains
`replace` directives with **relative** (`../`, `./`) targets and the chosen
worktree placement would not preserve them — i.e. the worktree and the repo
root do not share a parent directory — dispatch **fails with a clear blocking
error** instead of silently mis-resolving. The default sibling placement
always passes; the guard mainly defends an off-depth `TORQUE_WORKTREE_ROOT`
override. Absolute `replace` targets are depth-independent and never trip the
guard.

### Git-repo precheck

When per-run worktrees are enabled, a dispatch-time precheck verifies the
task's working dir resolves to a git repo (`FindRepoRoot` succeeds) **before**
the run starts. A non-git working dir produces a clean blocked-task reason up
front rather than a silent mid-dispatch fallback. Mode is `block` by default
(a non-git dir with worktrees enabled is an unambiguous misconfiguration);
`warn` or `off` relax it via `TORQUE_WORKTREE_PRECHECK`.

### Base-ref selection

The per-run worktree is detached at the first ref that resolves, in order:
`origin/main` → `origin/HEAD` → local `HEAD`. When the repo has an `origin`
remote it is refreshed first (`git fetch origin`, best-effort). A repo with
**no `origin` remote**, an offline fetch, or a default branch that is not
`main` therefore changes only the *freshness* of the checkout — it never
abandons worktree isolation. Branching from local `HEAD` is the fallback, not
a silent degradation to shared mode. (A missing `origin/main` previously made
`SetupPerRun` fail outright and the run silently fell back to shared mode.)

This base-ref order is also why an **option-4 plan** (a shared long-lived
branch, PRs deferred to program end) freezes its children against a stale
`main`: each child detaches off the shared branch tip (local `HEAD`), so no
child ever observes `main` move. See
[plan-branch-strategies.md](plan-branch-strategies.md) for the resulting drift
and the mandatory terminal reconcile-and-build step.

## Permission-mode contract

A Torque-spawned `claude` runs as a streaming-stdio subprocess with **no
human at a TTY**. If it boots in Claude Code's `default` permission mode, the
first tool that needs approval raises an interactive prompt nobody can
answer, and the run hangs. Torque therefore plants a deterministic,
non-interactive permission mode for every run.

### The `permission_mode` profile field

`agent_profiles` entries in `profiles.yaml` accept an optional
`permission_mode`:

| Value | Posture |
|---|---|
| `default` | Claude Code's built-in interactive mode. **Not safe for orchestrated runs** — will hang on the first prompt. |
| `acceptEdits` | Auto-approve file edits; still gates genuinely destructive operations. **Default when the field is unset.** |
| `plan` | Plan-only; no mutating actions. |
| `bypassPermissions` | Approve everything. Equivalent to the legacy `--dangerously-skip-permissions` arg. |

The value is validated at profile load time — an unknown value is a
load-time error naming the valid set.

### How it is applied

Torque passes the resolved permission mode to the Claude adapter, which plants
`permissions.defaultMode` in `.claude/settings.json` alongside settings such as
`apiKeyHelper`. Torque explicitly passes that file with `--settings` when
launching Claude, so a fresh boot directory does not depend on an interactive
trust grant to load its configuration. Both the wrapper and legacy Claude launch
paths carry the profile's model and extra arguments into the provider command.

Codex's JSON-RPC runtime owns the `app-server` subcommand. Torque removes
that duplicate from the prepared argument tail, preserves the remaining
options, and passes the profile model as a `-c model=...` override. The planted
`CODEX_HOME` still supplies permissions and MCP configuration; `thread/start`
binds execution to the task worktree.

After materialization, Torque explicitly prepares Codex credentials from the
launch environment's original `CODEX_HOME/auth.json`, or `HOME/.codex/auth.json`
when `CODEX_HOME` is unset. It writes a private 0600 copy into the isolated boot
directory before spawning. Credentials stay out of plans, artifacts and logs;
normal session teardown removes the copy. A missing, empty or malformed cache
fails boot with an actionable error. This path requires a file-backed login;
it does not read OS keyrings or switch the account to API-key billing. Token
refreshes in the isolated copy are not written back to the operator's cache.

Scheduler launches persist `torque.run_id` in session metadata from the actual
run ID; caller metadata cannot override that link. On daemon startup, recovery
closes a running invocation as `killed` only when its explicitly linked session
is crashed and no other live session owns that run. It blocks the task for
inspection only if it is still automatic, `doing`, and has no newer run.
Manual tasks and explicit task decisions are preserved. Recovery never replays
interrupted work automatically. Legacy sessions without that link, and sessions
whose process ownership is unknown (including PID 0), require separate evidence.

Graceful shutdown drains worker results before stopping the state writer.
Daemon interruption is distinct from a task transition and does not trigger
retry hooks. A terminal failed Codex turn stops the long-lived invocation with
a blocked result and a failed session, preserving the reason even if the
app-server process exits cleanly afterward.

For long-lived worker tasks, a positive `max_duration_ms` is an absolute
dispatch deadline that includes boot. When it fires, Torque stops the live
session and records the run as failed; the task result still flows through the
task's normal failure policy (`on_fail`, retry budget, escalation, or block).

An operator pause is different from worker failure. Moving an active long-lived
task to `paused` stops the process, leaves the task `paused`, and records both
the stopped run and stopped session as `canceled` with an `operator_pause`
cause. The killed process is terminal evidence, not a resumable live session;
Torque makes no resume promise for that stopped session.

The legacy path is unchanged: a profile whose `args` carry
`--dangerously-skip-permissions` boots in full bypass (the adapter already
plants `bypassPermissions`), and the post-processing step does not downgrade
it. `permission_mode` is the way to get the **middle ground** the dev flag
never offered.

> `internal/permission` (the `Engine` / rules governing MCP tools Torque
> *hosts*) is a **separate system** from the spawned subprocess's permission
> mode. `permission_mode` configures the latter only; the two are
> deliberately disjoint.

## External constraints — not Torque's to fix

These behaviors are outside Torque's control. Agents and orchestrators
should not rely on them for Torque-orchestrated runs.

- **Torque does not spawn mux subagents.** Multi-agent work under Torque =
  multiple top-level scheduler runs, each its own `claude`/`codex`/`opencode`
  subprocess via `agent.Boot`. Torque has no `subagent_spawn` and no
  parent-ID threading; an agent inside a Torque run must **not** attempt
  `subagent_spawn` (it needs nanite-substrate parent IDs a Torque run does
  not have).
- **Claude Code's Agent-tool worktrees are external.** When an agent uses
  Claude Code's own `Task`/Agent tool, that tool creates worktrees under
  `.claude/worktrees/` — nested *inside* the repo, at the wrong depth for
  relative `go.mod replace`. Torque cannot change this. For worktree
  isolation in a Torque run, use Torque's per-run worktree
  (`TORQUE_WORKTREE_PER_RUN`), which places at the correct sibling depth.
- **Non-claude provider permission modes** (codex / opencode) have their own
  approval models; `permission_mode` currently governs `claude-code` only.
  Threading it to other providers is a tracked follow-up.

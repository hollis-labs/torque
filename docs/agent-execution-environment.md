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
the two things that were unpredictable: **which `work_root` the agent gets**
(shared dir vs. per-run worktree) and **the permission posture it boots
with**.

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

After go-agent-launch plants the boot dir, Torque post-processes the planted
`.claude/settings.json` and merges in `permissions.defaultMode` from the
profile's resolved `permission_mode` (other keys such as `apiKeyHelper` are
preserved). This runs only for `claude-code` boots.

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

# Workspace model — the four roots

Torque resolves four distinct directories for every agent run. Naming them
explicitly (rather than passing loose path strings around) keeps the
scheduler, the `agent.Boot` pipeline, and any future launch layer in lockstep.
The four-root vocabulary is shared with the wider agent-OS toolchain.

| Root            | What it is                                            | Torque source today                                  |
|-----------------|-------------------------------------------------------|-------------------------------------------------------|
| `repo_root`     | Canonical project checkout, read-mostly.              | `agent.Options.Workdir` (the project root).           |
| `work_root`     | The per-launch **writable** dir the agent executes in.| Shared mode: `== repo_root`. Worktree mode: the per-run git worktree. |
| `workspace_dir` | Per-session **state / logs / metadata** root.         | `agent.WorkspaceCreate` → `~/.torque/workspaces/<projectKey>/<sessID>/`. |
| `build_dir`     | The planted provider **boot directory**.              | `$TMPDIR/torque-boot/agent-sessions-boot-*` (planted by go-agent-sessions). |

## `WorkspaceLayout`

`internal/runtime/agent/workspace.go` defines `WorkspaceLayout` — the Torque-owned
struct that carries all four roots plus the per-session sub-paths:

```
RepoRoot     – canonical checkout
WorkRoot     – writable execution dir (repo_root or per-run worktree)
WorkspaceDir – ~/.torque/workspaces/<projectKey>/<sessID>
  PromptDir  – <WorkspaceDir>/prompts   (reserved)
  StateDir   – <WorkspaceDir>/state     (reserved)
  LogDir     – <WorkspaceDir>/logs
  LogPath    – <WorkspaceDir>/logs/session.log
BuildDirRoot – $TMPDIR/torque-boot      (named, not an inline literal)
BuildDir     – the concrete planted boot dir (filled post-Start)
```

`WorkspaceCreate(workspacesRoot, projectID, sessID, repoRoot, workRoot)`
materializes the durable `workspace_dir` tree and returns a populated
`WorkspaceLayout`. `BuildDir` is empty until `OnBootDirPlanted` fires (the lib
plants it synchronously inside `Manager.Start`).

## `work_root` selection — `worktree.Spec`

Whether a run gets its own git worktree is expressed by `worktree.Spec`
(`internal/worktree/spec.go`) rather than only the `TORQUE_WORKTREE_PER_RUN`
env var:

```go
type Spec struct {
    Mode     Mode   // "shared" | "worktree"
    Root     string // per-run worktree parent dir
    KeepDays int    // TTL for orphaned (dirty) worktrees
}

workRoot, wtPath, err := spec.Resolve(repoRoot, runID)
```

- **`ModeShared`** (and the zero value): `work_root == repo_root`, no worktree.
- **`ModeWorktree`**: `SetupPerRun` creates a detached worktree at
  `origin/main`; `work_root` is that path and `wtPath` is the cleanup handle.

The env var is still honoured — `SpecFromEnv` reads `TORQUE_WORKTREE_PER_RUN` /
`TORQUE_WORKTREE_ROOT` / `TORQUE_WORKTREE_KEEP_DAYS` as the default — but it is
now a *source* for a `Spec`, not the only switch. The scheduler builds its spec
via `Scheduler.worktreeSpec()` (config-driven, config itself env-driven).

## Ownership boundary: scheduler vs launch

Per-run worktree lifecycle is **scheduler-owned**, not launch-owned:

- The scheduler calls `Spec.Resolve` before dispatch, routes the executor into
  `work_root`, and on run completion calls `CleanupPerRun` (+ a startup
  `SweepPerRun`).
- `agent.Boot` runs in **shared mode** — it records the `work_root` it is
  handed via `opts.Workdir` (which the scheduler has already pointed at the
  worktree) but never creates or removes worktrees itself.

So `work_root` is decided *above* `Boot`; `Boot` only consumes it.

## Cleanup / preservation semantics

`CleanupPerRun` removes a per-run worktree only when it is **clean** — no
uncommitted changes and no commits ahead of `origin/main`. A **dirty** worktree
(agent left work) is **preserved** for human inspection. `SweepPerRun` applies
the same dirty-preservation rule to orphans older than `KeepDays`.

`workspace_dir` lives **outside** any worktree (under `~/.torque/workspaces/`),
so durable logs and state survive worktree cleanup unconditionally. `build_dir`
is ephemeral and removed by go-agent-sessions at terminal session state.

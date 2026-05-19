# Plan branch & PR strategies

A multi-task plan (`kind=plan`) has to decide two things before its first
child dispatches:

1. **Branch granularity** — does each child task get its own branch, or do all
   children commit to one shared branch?
2. **Integration cadence** — does each unit merge to `main` as it finishes, or
   are PRs/merges deferred to the end of the program?

Crossing those axes gives the strategy space below. The shared agent-OS
planning vocabulary numbers these strategies 1–4; this page is authoritative
for **option-4** specifically — the shared-branch, end-of-program-PR corner —
and the terminal step a plan using it MUST include. Numbering of the
lower-drift variants is descriptive only.

| # | Branch granularity | Integration cadence | Drift exposure |
|---|---|---|---|
| 1 | per-task branch | each PR merged before the next task starts | minimal — every task starts from current `main` |
| 2 | per-task branch | PRs opened at program end | per-task — each branch drifts only for its own (short) lifetime |
| 3 | shared branch | periodically merged/rebased onto `main` during the program | bounded — drift resets at each reconcile |
| 4 | shared branch | one set of PRs opened at program end | **unbounded — the branch drifts from `main` for the entire program** |

The gradient runs from option-1 (most integrated, most PR overhead) to
option-4 (least overhead, most drift). This page covers option-4.

## Why option-4 drifts

Option-4 is attractive: no PR gate between children, one continuous
orchestrator walk, a single review at program end instead of one per task. The
cost is structural. The shared branch is cut **once**, off `main`, at program
start, and nothing pulls `main` back in until the program ends.

Torque's per-run worktrees do not soften this — they sharpen it. A per-run
worktree detaches at the first ref that resolves: `origin/main` → `origin/HEAD`
→ local `HEAD` (see [agent-execution-environment.md](agent-execution-environment.md)
§"Base-ref selection"). On an option-4 plan the children run off the shared
branch tip — local `HEAD` — so **no child ever sees `main` move** either. The
whole program executes against a frozen snapshot of `main`.

Meanwhile `main` keeps moving: other programs land their PRs. By program end
the shared branch and `main` have diverged by the full duration of the
program.

## A clean textual merge is not a working merge

When an option-4 program ends and you `merge origin/main`, git runs a 3-way
**textual** merge. Git only knows about text. It raises a conflict when two
sides edited the same lines — and says nothing about:

- a symbol renamed on `main` and still called by its old name on the branch;
- a function/method signature changed on `main` and called the old way;
- a file deleted or moved on `main` and still imported by branch code;
- a type or interface narrowed on `main` that branch code no longer satisfies.

These are **semantic conflicts**. Every one of them merges textually clean and
breaks the build. A 3-way merge cannot see them; only a build can.

**Concrete case — Torque messaging program** (findings: *"Option-4 shared
branches drift from main"*). The messaging end PR (#75) was an option-4 shared
branch. Mid-program, PR #74 (the sysop-ui kit migration) landed on `main`.
Merging `origin/main` into the messaging branch, git flagged exactly **one**
textual conflict; after resolving it the merge was textually clean — and `tsc`
then reported **22 errors**. The kit migration had changed component APIs the
messaging branch still called the old way. Git's one flagged conflict was not
the scope of the problem; the build was.

> **The lesson, stated once: a clean textual merge is not a working merge.
> Always build after a merge — the build, not git, is the authority on whether
> the merge worked.**

## Mandatory: a terminal reconcile-and-build task

Any plan that uses option-4 MUST include an explicit **terminal
reconcile-and-build task** as its final plan task — after every feature/child
task and before any PR is opened. It is not optional, and it is not folded
into another task (a reconcile bundled into a feature task gets skipped under
time pressure and its build failures get misattributed).

The task performs, in order:

1. `git fetch origin`.
2. `git merge origin/main` into the shared branch.
3. **Resolve** every textual conflict git reports.
4. **Run the FULL build and the full test / type-check suite** for the whole
   tree — not an incremental build, not only the packages the program touched.
   Whatever "green" means for the repo: `go build ./...` + `go test ./...`,
   `npm run build` + `tsc --noEmit`, etc.
5. **Fix every semantic break the build surfaces** — renamed symbols, changed
   signatures, moved files, narrowed types. Treat the build, not git, as the
   merge's authority.
6. **Only once the full build is green**, open the program's PR(s).

Acceptance for this task is the green build output — not "the merge
completed". A reference template ships at
[`templates/reconcile-and-build.yaml`](templates/reconcile-and-build.yaml);
instantiate it as the last child of any option-4 plan.

## Periodic reconcile — bound the drift instead of paying it all at once

The terminal task is mandatory because it is the last line of defense. But a
single end-of-program reconcile pays the program's entire accumulated drift in
one task — large, hard to review, and squarely on the critical path to
shipping.

For any option-4 program longer than a couple of tasks, **also** schedule a
periodic reconcile: after any PR you know landed on `main` (or on a fixed
cadence — e.g. once per plan phase), run steps 1–5 above on the shared branch.
Each periodic reconcile is small, resets the drift to zero, and turns the
terminal reconcile into a near-noop.

This is, in fact, the only difference between option-4 and option-3 —
option-3 is "option-4 with the periodic reconcile made mandatory". A long
option-4 plan should adopt periodic reconciles and effectively become
option-3.

Either way the rule is identical for **every** reconcile, periodic or
terminal:

> **merge → resolve → full build → fix → only then PR.**

## See also

- [agent-execution-environment.md](agent-execution-environment.md) — per-run
  worktree base-ref selection (why option-4 children branch off the shared
  tip, not `origin/main`).
- [plans-v1.md](plans-v1.md) — the `kind=plan` model the terminal reconcile
  task plugs into as a final child.
- [templates/reconcile-and-build.yaml](templates/reconcile-and-build.yaml) —
  the reference task template for the mandatory terminal reconcile.

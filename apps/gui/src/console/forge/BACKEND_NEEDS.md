# Forge contract: interim status and backend needs (feeds G1, CW-20261016-0110)

The GUI codes to `ForgeClient` (`client.ts`) and the types in `types.ts`.
`MockForgeClient` (`mock.ts`) is the only implementation today. Everything here
is INTERIM until G1 adds a typed HTTP client and the backend exposes forge
operations. The GUI holds no network access and no secrets.

## Vocabulary

`RepoRef`, `CheckoutRef`, `ForgeState` follow the Cerberus forge vocabulary
(CW-20261004-0048; roadmap `apps/cerberus/docs/plans/cerberus-plugins-roadmap.md`
PL-4 "leave room for GitLab and Gitea"). `RepoRef.host` is an open string
(`github` | `gitlab` | `gitea`). Unions use an open-string helper so new values
from the backend never break typechecking. Per CW-20261004-0048, identifiers are
shared contracts with Stack Explorer and the git-ops app (never databases):
`repoKey()` (`host:owner/name`) and `prKey()` (`host:owner/name#n`) are the
stable string forms. Effect classes mirror the Cerberus connector classes
(`read`, `read_sensitive`, `write`, `lifecycle`, `destructive`, `exec`); the
per-method assignment lives in `FORGE_OP_EFFECTS` (`types.ts`) and the table
below. Vocabulary beyond the three names is derived from the roadmap and GitHub
REST shapes.

## What the existing plugin offers today

Source read: `plugins/cerberus-plugins/github` (v0.1.0, protocol 2). It is
read-only and exposes exactly three operations (all `read`, no ack):

| Plugin op | Output (`dto.go`) | GUI use |
|---|---|---|
| `status` (owner, repo) | `RepoStatus`: owner, repo, description, default_branch, private, stars, open_issues, updated_at | `ForgeClient.getRepo` -> `Repo` (all fields map 1:1, camelCased) |
| `list_releases` (owner, repo, limit) | `Release`: tag_name, name, draft, prerelease, published_at, html_url | not in the G0 contract; add `listReleases` later if needed |
| `list_workflow_runs` (owner, repo, limit) | `WorkflowRun`: id, name, status, conclusion, branch, event, created_at, html_url | partial source for `CheckRun` (status/conclusion/name/url map; per-PR association is missing, runs are per repo/branch) |

Notes: the plugin is an allow-list (ADR 0003): it drops owner/actor objects,
head commits, and permissions. Free text (descriptions, names, branches) is
labelled untrusted; the GUI must render it as text, never HTML. Credentials
live in Cerberus (`github/token`); the GUI never sees them. Errors surface as
Cerberus codes (`credential_missing`, `connector_unavailable`, `invalid_args`,
else `operation_failed`); `ForgeError.code` should be mapped from those
(`unauthorized`, `unavailable`, `invalid_args`/`not_found`, other).

## Operations the backend must provide

PR operations are part of the planned host-neutral forge plugin (roadmap PL-4,
piece 2) and are owned by the backend engineer.

| `ForgeClient` method | Needed plugin/backend op | Effect class | Status |
|---|---|---|---|
| `getRepo` | `status` | read | exists |
| `listPrs(query)` | `list_pull_requests` (state, author, assignee, query, cursor) | read | to build |
| `getPr` | `get_pull_request` incl. mergeable, reviews, check rollup | read | to build |
| `getDiff` | `get_pull_request_diff` -> per-file patches; paginate/truncate large files (`FileDiff.truncated`) | read | to build |
| `listCommits` | `list_pull_request_commits` | read | to build |
| `listComments` | `list_pull_request_comments` (issue + review comments merged, thread/resolved info) | read | to build |
| `addComment` | `create_pull_request_comment` (issue or inline: path/line/side/in_reply_to) | write | to build |
| `reviewPr` | `submit_pull_request_review` (verdict, body, inline comments) | write | to build |
| `assign` | `set_pull_request_assignees` (replacement set) | write | to build |
| `listPendingPrs` | aggregate across a configured repo set, or a Torque-side view | read | to build |

Check runs per PR need `list_check_runs` for a head sha (the current
`list_workflow_runs` is not PR-scoped).

Roadmap ops outside G0 (PR create/merge, issues, notifications, Actions
dispatch/rerun, release asset upload/download, deploy keys, webhooks) are not in
the GUI contract yet. Expected classes: create/comment `write`, merge
`lifecycle`, dispatch/rerun `lifecycle`, deploy-key and webhook removal
`destructive`. Add them to `ForgeClient` and `FORGE_OP_EFFECTS` when a GUI
feature needs them (additive).

## Cross-cutting backend needs

1. Writes (`addComment`, `reviewPr`, `assign`) require Cerberus acknowledgment
   and, per decision D2, IdentityConfirm step-up in the GUI before a review
   verdict or any change-authorising action. Attribution: the forge actor will
   be the token owner, not the Console user; backend should record the
   acting Console identity in the audit log and optionally append a footer.
2. A Torque-side HTTP surface (G1) in front of Cerberus: GUI -> Torque -> Cerberus
   plugin. No GUI-to-GitHub calls, no tokens in the browser.
3. `PullRequest.taskId` / `agentId` need a backend link table (PR <-> Torque
   task / agent session). Optional; the GUI tolerates absence.
4. Pagination via opaque `cursor` / `nextCursor`.
5. Rate limiting: GitHub 403/429 currently come back uncoded
   (`operation_failed`). Ask for a distinct retryable code.
6. Live updates: PR/check changes should emit events on the Console event bus
   (F1) so the pending-PR inbox (G4) need not poll.
7. Multi-forge: `host` selects the plugin; GitLab/Gitea map `merge request`
   onto `PullRequest` and `approvals` onto `Review`.
8. Mapping nuances: GitHub "draft" is both `PullRequest.draft` and
   `ForgeState: 'draft'` (use `draft` flag as authoritative, `state` as coarse
   state). `mergeable` is computed lazily on GitHub and may be `unknown`.

## Open questions

- Is the approved CW-20261004-0048 vocabulary field-compatible with `types.ts`?
- Should `listPendingPrs` be a plugin op, or computed in Torque from a watched
  repo set (and who owns "reasons")?
- Who is the forge actor for agent-authored comments (shared bot token vs.
  per-agent identity)?

## Forge refs (G2: PR / commit / repo chips, hovercards, drawer)

Code: `forge/refs/`. Reads only, through the installed `ForgeClient`.

1. `getCommit(repo, sha)` (effect: read) is missing from the contract. The
   commit resolver currently scans up to 25 PRs (`listPrs` + `listCommits`) to
   find a sha, so commits outside any PR show as not found, and cost is
   O(PRs). Needed: `get_commit` returning message, author, parents, and
   per-commit stats (files changed, additions, deletions, file list); the
   commit hovercard has no "files changed" until then.
2. Batch/cheap PR summary: a hovercard needs title, state, author, check
   rollup, review rollup, size. `getPr` returns checks/reviews in full; a
   `getPrSummary`/`getPrs(refs[])` batch would cut a page of chips from N calls
   to one. Check runs per PR (`list_check_runs` for head sha) are required for
   the checks line to be real (see the table above).
3. Repo list for shorthand detection: bare `owner/name` is linked only for
   known repos (`setKnownRepos`, seeded with hollis-labs/torque and
   go-runtime). Wanted: an endpoint listing the repos Cerberus/agents work
   in (watched repo set), so the GUI does not hard-code them.
4. Host registry: web domains are mapped to hosts in `parse.ts`
   (`registerForgeHost`; github.com, gitlab.com, codeberg.org built in).
   Self-hosted GitLab/Gitea domains should come from backend config.
5. Rate limiting and error codes: chips treat `not_found`/`invalid_args` as
   not-found and any other `ForgeError` as a retryable error; a distinct
   retryable `rate_limited` code would let the chip show a better message.

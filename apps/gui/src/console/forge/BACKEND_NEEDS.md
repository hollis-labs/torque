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

## Pending-PR inbox (G4: detect, triage, resolve, assign)

Code: `forge/inbox/`. Reads `ForgeClient.listPendingPrs()`; triage and
assignment records live in the StateStore; assignment is dispatched through the
ActionBus (`pr.assign`) behind IdentityConfirm. Extends, does not repeat, the
open questions above (`listPendingPrs` ownership and "reasons").

1. `listPendingPrs` semantics. Today computed from the mock only. Needed: the
   caller's view (review requested of me/my agents, authored with failing
   checks or changes requested, mentioned, stale), per watched repo set, with
   `reasons` filled in server-side. `stale` is also derived client-side from
   `updatedAt` (default 7 days, `staleMs` prop); the backend should own that
   threshold. A `mentioned` reason is missing from `PendingPrSummary.reasons`.
2. Live updates: emit `pr.review_requested`, `pr.checks_failed`,
   `pr.changes_requested`, `pr.conflicting`, `pr.stale` on the Console event bus
   (subject `{type:'pr', id:'<host>:<owner>/<name>#<n>'}`, payload `repo`,
   `number`, `title`, `author`, `checks`, `reasons`). Until then `PendingPrSource`
   polls and maps via a swappable `PendingPrEventMapper`; the last three kinds
   are inbox-defined and need agreeing with F1.
3. StateStore keys used (server-stored, readable by agents): `pr-inbox:<prKey>`
   (dismiss / snooze / settle `resolved`) and `pr-assign:<prKey>:<agentId>:<task>`
   (settled with resolution `assigned`, note JSON `{messageId, forgeAssigned}`).
   The store needs prefix queries for these and should de-dupe server-side.
4. `assign` needs an agent -> forge login mapping. `AgentRecord` has no forge
   identity, so the GUI assumes login = agent slug (`forgeLogin` override).
   Also the forge actor question above: assigning someone else is done with the
   token owner's credentials.
5. Assignment is two effects (forge assign + mail send) the GUI sequences
   itself; a single backend op `assign_pr_to_agent` (idempotent on the
   `dedupeKey` in the payload, atomic or compensating) would remove the partial
   failure case (assignee set, message failed).
6. Optional Torque task creation linked to the PR (`source_ref` =
   `github:owner/name#n`) is NOT done in the GUI: needs a task-create route that
   accepts `source_ref` and returns the id for the assignment payload.
7. Agents need an address: agents without `identity.urn` get a fallback
   `msg://agent/console/<id>`, which may not be deliverable.
8. G3 route: `prWorkspaceHref` in `forge/inbox/href.ts` is a stand-in; swap to
   G3's exported helper at merge.
## PR workspace (G3: diff viewer, inline threads, reviews)

Code: `forge/workspace/`. Reads use `getPr`, `getDiff`, `listCommits`,
`listComments`; writes dispatch on the ActionBus and BOTH sit behind
IdentityConfirm (owner decision): `forge.pr.comment` (scope `forge.write`;
one step-up per general/inline/reply comment) and `forge.pr.review` (scopes
`forge.write` plus `forge.review` for approve/request_changes). Queued
"Add to review" comments are sent inside the single review submission and are
covered by that one step-up (no per-comment prompt). The backend must verify
the `X-Identity-Confirm` token covers `forge.write` on `POST .../comments`,
not only on reviews, and keep tokens short-lived (2 minute TTL requested) since
each comment prompts separately. The IdentityConfirm token
reaches `HttpForgeClient` through `confirm-token.ts` (`currentConfirmToken`
is the factory default for the `confirmToken` option). Needs beyond
BACKEND_REQUIREMENTS.md:

1. Per-file lazy diff: `getDiff` returns every file with every hunk. For big
   PRs the GUI wants `GET .../files` (path, status, additions, deletions,
   binary, truncated; no hunks) and `GET .../files/{path}/patch` returning one
   `FileDiff`. The GUI already collapses files of 400+ lines and renders
   them in 300-row windows, so only the transport is missing.
2. Outdated detection: `ReviewComment.commitSha` must be the commit the
   comment was made on, and `PullRequest.headSha` the current head. The GUI
   marks a thread outdated when they differ and shows it detached from the
   line (line numbers refer to another commit). Preferably the backend sends
   `outdated: boolean` and the original `diffHunk`, so the GUI need not infer.
3. Resolve / unresolve a thread (`resolveThread(ref, threadId, resolved)`,
   effect write). `ReviewComment.resolved` is displayed but cannot be changed.
4. Merge: no `mergePr` op exists. The workspace shows merge state (mergeable,
   checks, reviews, draft) and links to the forge. Wanted: `mergePr(ref,
   { method: 'merge'|'squash'|'rebase' })`, effect `lifecycle`, behind
   IdentityConfirm scope `forge.merge`, plus branch-protection reasons in
   `PullRequest` (`mergeBlockers: string[]`) so the GUI need not infer them.
5. Pending (draft) reviews: the GUI batches inline comments client-side and
   sends them in one `reviewPr` call. A forge-side pending review is not
   needed, but `reviewPr` must be atomic (all comments or none) and return the
   created comment ids (or the client refetches, as it does now).
6. Edit / delete own comment (`updateComment`, `deleteComment`): not in the
   contract; not offered in the UI yet.
7. Reactions, suggested changes and multi-line comment ranges
   (`startLine`/`endLine` on `AddCommentInput`): not supported; single-line
   anchors only.
8. Viewer identity: optimistic comments need the acting user (login, agent
   flag). Today the placeholder author is `you`; wanted `GET /api/v1/me` or
   `ForgeClient.viewer()` for forge identity vs. the Console user.
9. Comment body format: the GUI renders bodies as plain text with autolink
   chips (no HTML). If the forge returns markdown, say so; a sanitised
   `bodyHtml` is optional and would be rendered only after sanitising.
10. Idempotency: comment posting is user-retryable after a failure; the
    `Idempotency-Key` header in BACKEND_REQUIREMENTS.md section 3.4 is
    needed so a retry after a timeout does not double-post.

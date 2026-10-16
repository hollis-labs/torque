# Forge backend requirements (handoff to the backend agent)

Task: CW-20261016-0110 (G1), epic EP-20261016-0002. Audience: the backend
engineer who builds the Torque forge API and the cerberus-github PR operations.
GUI side is done: `HttpForgeClient` (`http.ts`) implements `ForgeClient`
(`client.ts`) against exactly the surface below, tested with a mocked fetch
(`http.test.ts`). The types in `types.ts` ARE the JSON schema. `BACKEND_NEEDS.md`
(G0) holds the vocabulary background and open questions; this file is the spec.

The GUI never talks to GitHub and holds no tokens. Path: GUI -> Torque API
(`/api/v1/forge/...`) -> Cerberus -> forge plugin.

## 1. Conventions

- **Base path** `/api/v1/forge` (GUI-configurable; `VITE_FORGE_BASE_URL`).
  Same-origin, same auth as the rest of the Torque API.
- **JSON, camelCase**, field names and optionality exactly as `types.ts`.
  The GUI tolerates unknown extra fields and missing optional fields; it
  defaults `assignees`, `checks`, `reviews`, `reasons`, `hunks` to `[]` and
  `mergeable` to `"unknown"`. Required: everything not marked `?` in `types.ts`.
- **Enums are open strings.** New values are additive and never break the GUI.
- **Envelope.** Success: `{ "data": <payload> }`. A bare payload is also
  accepted, but send the envelope.
- **Host in path.** `{host}` is `github` | `gitlab` | `gitea` (`RepoRef.host`);
  `{owner}`, `{name}` URL-encoded. Stable string forms: `repoKey` =
  `host:owner/name`, `prKey` = `host:owner/name#n`.
- **Untrusted text.** Titles, bodies, branch names, comments are untrusted; the
  GUI renders them as text. Keep the plugin's allow-list DTO discipline (ADR
  0003); never pass raw forge payloads.
- **Timestamps** RFC 3339 UTC.
- **Pagination.** Lists that can be large use opaque `cursor`/`limit` query
  params and return `{ "data": { "items": [...], "nextCursor": "..."|null } }`.
  `limit` default 30, max 100. Cursor is opaque and valid only with unchanged
  filters. Non-paged lists (diff, commits, comments, pending) return
  `{ "data": { "items": [...] } }` (a bare array is also accepted); see the
  per-endpoint notes for size limits.

## 2. Endpoints

`P` = `/repos/{host}/{owner}/{name}/pulls/{number}`.

| ForgeClient method | HTTP | Effect | Request | Response `data` |
|---|---|---|---|---|
| `getRepo(repo)` | `GET /repos/{host}/{owner}/{name}` | read | none | `Repo` (host/owner/name optional in body; GUI fills from path) |
| `listPrs(q)` | `GET /repos/{host}/{owner}/{name}/pulls` or `GET /pulls` (all watched repos) | read | query: `state=open\|closed\|merged\|all` (default `open`), `author`, `assignee`, `q` (text), `limit`, `cursor` | `{ items: PullRequest[], nextCursor }`, newest `updatedAt` first |
| `getPr(ref)` | `GET P` | read | none | `PullRequest` incl. `checks` (check runs for head sha), `reviews`, `mergeable` |
| `getDiff(ref)` | `GET P/diff` | read | none | `{ items: FileDiff[] }` with `hunks` and numbered `DiffLine`s |
| `listCommits(ref)` | `GET P/commits` | read | none | `{ items: Commit[] }` oldest first |
| `listComments(ref)` | `GET P/comments` | read | none | `{ items: ForgeComment[] }` issue (`kind:"issue"`) and inline (`kind:"review"`) merged, oldest first; review comments carry `inReplyTo`, `resolved` |
| `addComment(ref, in)` | `POST P/comments` | write | `AddCommentInput`: `{ body, path?, line?, side?, inReplyTo? }`; omit `path` for a conversation comment | `ForgeComment` (201) |
| `reviewPr(ref, in)` | `POST P/reviews` | write, ack | `ReviewInput`: `{ verdict: approve\|request_changes\|comment, body?, comments?[] }` | `Review` (201) |
| `assign(ref, in)` | `PUT P/assignees` | write | `AssignInput`: `{ assignees: string[] }` full replacement set | updated `PullRequest` |
| `listPendingPrs()` | `GET /pulls/pending` | read | none (v1); optional later: `limit`, `cursor`, `reason` | `{ items: PendingPrSummary[] }` |

Effect classes are the Cerberus connector classes and must equal
`FORGE_OP_EFFECTS` in `types.ts`. Operations the GUI does not use yet (PR
create/merge, issues, Actions rerun, releases) are additive later: merge and
rerun are `lifecycle`, webhook/deploy-key removal `destructive`.

### Size and truncation

- `getDiff`: a file whose patch the forge omits or that exceeds a server cap
  (suggest 200 KB/file, 3000 lines total) is returned with `truncated: true`
  and `hunks: []`; binary files `binary: true`. Counts (`additions`,
  `deletions`) stay populated. If the PR has more than ~300 files, return the
  first 300 and add `"truncated": true` at `data` level (GUI tolerates it
  today; a paged form `?cursor` may be added later).
- `listComments` / `listCommits`: cap at 500 and flag `data.truncated`.

## 3. Auth and identity

1. **Authentication**: the same Torque session/bearer as other `/api/v1`
   routes (Caddy injects the token for same-origin tailnet requests today).
   The browser cannot send forge credentials; forge credentials stay in
   Cerberus (`github/token`).
2. **Read ops** need only an authenticated Torque user with access to the
   project that watches the repo.
3. **Write ops** (`addComment`, `reviewPr`, `assign`) require step-up identity
   (decision D2). The GUI runs `IdentityConfirm` (`console/seams/identity-confirm.ts`)
   first and sends the resulting opaque `ConfirmResult.token` as header
   **`X-Identity-Confirm: <token>`**. The backend verifies it covers scope
   `forge.write` (suggested; `reviewPr` with `approve`/`request_changes`:
   `forge.review`), is unexpired, and belongs to the calling user. Missing or
   invalid -> `401 {code:"unauthorized"}` (use `identity_required` in
   `error.code` to let the GUI re-prompt; it maps to `unauthorized`).
   The exact token format comes from the auth workstream; the GUI treats it as
   opaque.
4. **Idempotency**: write requests may carry `Idempotency-Key`; replaying the
   same key returns the original result rather than a duplicate comment/review.
5. **Cerberus acknowledgment**: every `write` is an acknowledged Cerberus
   operation (`acknowledged=true`) issued by Torque on behalf of the confirmed
   user; the GUI never sets it.
6. **Attribution / audit**: the forge actor is the token owner, not the
   Console user. Record the confirmed Console user (and the agent session, if
   any) in the Cerberus/Torque audit log, and optionally append a footer to
   bodies (e.g. `Posted via Torque Console by <user>`). Open question: shared
   bot token vs. per-agent identity (see G0 open questions).

## 4. Errors

Non-2xx responses: `{ "error": { "code": "<code>", "message": "<text>" } }`
(top-level `{code,message}` also accepted). `HttpForgeClient` maps to
`ForgeError.code`:

| Backend `error.code` (or HTTP status if no code) | GUI `ForgeError.code` | HTTP |
|---|---|---|
| `not_found` (repo/PR missing or invisible) | `not_found` | 404 |
| `invalid_args` | `invalid_args` | 400/422 |
| `unauthorized`, `credential_missing`, `identity_required`, `forbidden` | `unauthorized` | 401/403 |
| `conflict` (e.g. head moved, PR not open, already merged) | `conflict` | 409 |
| `rate_limited` (send `Retry-After`) | `rate_limited` | 429 |
| `unavailable`, `connector_unavailable`, `operation_failed`, 5xx, network failure, non-JSON body | `unavailable` | 502/503/500 |
| any other code | passed through verbatim (open union) | any |

The plugin reports GitHub 403/rate-limit as uncoded `operation_failed`;
please translate to a distinct retryable `rate_limited`. A repo the token
cannot see is `invalid_args` in the plugin today; surface it as `not_found`.

## 5. Mapping onto the cerberus-github plugin

Source: `hollis-labs/plugins`, `cerberus-plugins/github` v0.1.0, protocol 2.
Read-only; three ops today (all `read`, no ack): `status`, `list_releases`,
`list_workflow_runs`. No PR operations exist. They are to be built by the
backend engineer; this is plain requirements.

| Endpoint | Plugin op | State | Notes |
|---|---|---|---|
| `getRepo` | `status` | EXISTS | 1:1 (`default_branch`->`defaultBranch`, etc.) |
| `listPrs` | `list_pull_requests` | BUILD | args owner, repo, state, author, assignee, query, limit, cursor |
| `getPr` | `get_pull_request` | BUILD | include mergeable (lazy on GitHub; may be `unknown`), review rollup |
| (checks in `getPr`) | `list_check_runs` (by head sha) | BUILD | `list_workflow_runs` is per repo/branch, not PR-scoped; use it only as a fallback |
| `getDiff` | `get_pull_request_diff` | BUILD | per-file patches parsed to hunks/lines; truncation as above |
| `listCommits` | `list_pull_request_commits` | BUILD | |
| `listComments` | `list_pull_request_comments` | BUILD | merge issue + review comments; thread/resolved info (GraphQL on GitHub) |
| `addComment` | `create_pull_request_comment` | BUILD (write) | issue or inline (path/line/side/in_reply_to) |
| `reviewPr` | `submit_pull_request_review` | BUILD (write, ack) | verdict, body, inline comments |
| `assign` | `set_pull_request_assignees` | BUILD (write) | replacement semantics; Torque diffs against current |
| `listPendingPrs` | none; Torque aggregation | BUILD | see section 6 |

Token scope grows: PR read needs Pull requests (read) + Checks (read);
writes need Pull requests (write). A separate write token is recommended so
read-only deployments remain possible.

## 6. Pending-PR aggregation (`GET /pulls/pending`)

**Owner: Torque (backend), not the plugin and not the GUI.** The plugin
stays per-repo and stateless. Torque keeps a watched repo set (per project;
config, no UI yet), fans out `list_pull_requests`, computes `reasons`, and
caches. Reasons (open union): `review_requested`, `checks_failed`,
`changes_requested`, `conflicting`, `unassigned`, `stale` (suggest > 7 days no
update). Include a PR if open and at least one reason applies; sort by
`updatedAt` desc. "Review requested" is relative to the calling user or any
agent they own. `checksSummary`/`reviewSummary` are rollups. The GUI (G4)
shows them and calls `assign`. Staleness: cache TTL <= 60 s, and emit Console
events (below) so the inbox need not poll.

## 7. Events (Console event bus, F1 contract)

Emit on PR/check changes so G3/G4 update live: kinds proposed
`forge.pr.updated`, `forge.pr.merged`, `forge.pr.closed`,
`forge.check.completed`, `forge.review.submitted`, `forge.comment.created`,
with a `ConsoleRef` to the PR (`prKey`) and, where known, the owning
`taskId`/`agentId`. Fed by webhooks or polling in the backend; the GUI does not
care which. Align names with `events/CONTRACT.md`.

## 8. Linking PRs to Torque tasks and agents

`PullRequest.taskId` / `agentId` are optional. If the backend keeps a link
table (PR <-> task/agent session, e.g. from branch name or PR body
`CW-...` ref), populate them; otherwise omit. G2 hovercards and G4 assignment
use them when present.

## 9. GitLab / Gitea room

- `host` in the path selects the plugin; the same routes serve all hosts.
- Mapping: merge request -> `PullRequest`; MR approvals -> `Review`
  (`approved`); pipelines/jobs -> `CheckRun`; GitLab "discussions" -> thread
  via `ReviewComment.inReplyTo`; Gitea mirrors the GitHub shape.
- Host-specific concepts must not leak into the wire shape; carry extras as
  additive optional fields. Unsupported ops return `501`-style
  `{code:"unavailable"}` with a message; the GUI degrades.
- Numbers: `PrRef.number` is the PR number / GitLab MR `iid`.

## 10. Acceptance checklist for the backend

- [ ] All endpoints in section 2 respond with the envelope and `types.ts` shapes
- [ ] Write ops reject missing/invalid `X-Identity-Confirm`
- [ ] Error codes per section 4, incl. `rate_limited` with `Retry-After`
- [ ] `FORGE_OP_EFFECTS` equals the plugin op effect classes
- [ ] Pagination cursors on `listPrs`; truncation flags on diff
- [ ] `/pulls/pending` aggregation with documented reasons
- [ ] Contract test: replay recorded responses through `HttpForgeClient`
      (`http.test.ts` shows the harness: inject `fetch`)

## Using the client

```ts
import { createDefaultForgeClient, HttpForgeClient } from '@/console/forge'
const forge = createDefaultForgeClient() // mock unless ?forge=http or VITE_FORGE_SOURCE=http
const live = new HttpForgeClient({ confirmToken: () => lastConfirm?.token })
```
Dev switch: `?forge=mock|http` (query wins), then `VITE_FORGE_SOURCE`, default
`mock`. Mirrors `events/mode.ts` (`?console=fixture|live`) but defaults to mock
because the backend does not exist yet.

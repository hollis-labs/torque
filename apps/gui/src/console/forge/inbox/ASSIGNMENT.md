# PR assignment message (`console.pr_assignment/v1`)

Sent by the Console pending-PR inbox when an operator assigns a PR to an agent.
It is an ordinary mail message (intent `message`, template `pr_assignment`):

- Subject: `[PR] <Task label>: <owner>/<repo>#<n>`
- Body: readable text (PR, URL, Console workspace route, why it is pending,
  checks/reviews rollup, what to do, optional operator note) followed by one
  fenced block tagged `console-pr-assignment` holding the JSON payload below.

```json
{
  "schema": "console.pr_assignment/v1",
  "task": "review | fix_checks | address_comments | resolve_conflicts",
  "dedupeKey": "pr-assign:<host>:<owner>/<repo>#<n>:<agentId>:<task>",
  "pr": { "host": "github", "owner": "", "repo": "", "number": 0, "title": "", "url": "", "workspace": "/prs/...", "author": "" },
  "reasons": ["checks_failed"],
  "checks": { "passed": 0, "failed": 0, "pending": 0 },
  "reviews": { "approved": 0, "changesRequested": 0 },
  "mergeable": "mergeable | conflicting | blocked | unknown",
  "instructions": ["..."],
  "note": "optional operator text",
  "assignedBy": "operator id"
}
```

Receiving agent: parse the block (`parseAssignmentBody` in `assign.ts`), do the
task on the PR branch (never merge, never open a new PR for fixes), and reply on
the same thread with a one-line result. `dedupeKey` is the idempotency key: the
same key means the same assignment; treat a repeat as a reminder, not new work.
The Console de-dupes by that key and only re-sends on an explicit "Assign again".

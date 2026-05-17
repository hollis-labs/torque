# CW-20260517-0011 — Dispatch DX fixes (edges 5, 7, 8)

Root cause across all three edges: Torque has exactly ONE dispatch gate — the
per-task `manual` flag (`scheduler.Picker` only picks `manual=false` tasks) —
but the surfaces around it never explain that, so callers hit silent dead ends.

## Edge 5 — no obvious "start the sprint" action for approve_sprint
Finding: `approval_mode` is pure metadata; the scheduler/picker never reads it.
`torque_sprint_approve` only moves review->done, so a fresh sprint returns
"0 tasks approved" and nothing begins dispatch.
Change: added `torque_sprint_start` MCP tool + `SprintService.Start`. Starting
a sprint = bulk-promoting every parked (manual=true) task to manual=false.
Idempotent; per-task failures collected in Skipped. Response message states
the mental model. Chosen over overloading sprint_approve.
Files: internal/service/sprint.go, internal/mcpadapter/sprint_tools.go.

## Edge 7 — project repo_path drift
Finding: Create/Update only checked repo_path non-empty; a missing directory
persisted silently.
Change: new internal/service/repopath.go — RepoPathError (typed/named),
expandUserPath (proper ~ expansion), validateRepoPath, CheckRepoPath
(exported, NON-mutating doctor helper). Create+Update now reject a repo_path
that doesn't resolve to an existing directory. mapServiceError maps
*RepoPathError -> arg_invalid, field=repo_path. No DB data rewritten.
Files: internal/service/repopath.go, internal/service/project.go,
internal/mcpadapter/errors.go, internal/mcpadapter/project_tools.go.

## Edge 8 — tasks force-created manual=true
Finding: torque_task_create force-sets manual=true but response never says so.
Change: kept the safety default; added a `dispatch_notice` block to the
create response (new createdTaskResult shape; taskResult unchanged). Notice
has manual, dispatchable, message, and promote_with (the literal
torque_task_update call with the real task ID interpolated).
Files: internal/mcpadapter/task_tools.go.

## Verification
go build ./... — pass. go test ./internal/mcpadapter/... ./internal/service/...
— pass. go vet — clean. New tests: dispatch_dx_test.go, TestSprintStart* in
sprint_test.go, TestProjectCreateRejects*/TestProjectUpdateRejects*/
TestCheckRepoPath in project_test.go.

## Decisions locked
- One dispatch gate: approval_mode is metadata; manual flag is the gate.
  sprint_start operationalizes the sprint gate as the cohort of manual tasks.
  No new scheduler-level approval gate (out of scope).
- Hard validation for repo_path, not a warning.
- Safety default preserved for edge 8; only discoverability added.

## Known limitations
- service.Task.Create AND the MCP layer both force manual=true (two overrides
  for one policy). Left as-is, out of scope.
- repo_path validation is create/update-time only; no dispatch-time recheck,
  no sweep of existing stale rows. CheckRepoPath available but not wired in.

## Follow-up candidates
- internal/httpserver/issues_test.go:22 (OUT OF OWNERSHIP) hardcodes
  repo_path:"/tmp/issues"; TestHTTP_IssueCreateAndList now fails because
  create correctly rejects the missing dir. httpserver owner should swap the
  literal for a t.TempDir(). Only known out-of-scope breakage.
- Wire service.CheckRepoPath into a torque_project_doctor/health tool.
- Consider dispatch-time repo_path re-validation in the scheduler.

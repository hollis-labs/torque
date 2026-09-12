# Investigation draft: consumer queries and Torque dogfooding

Review draft for Chrispian; not a canonical Tesseract record. This is historical evidence from session `session-20260911-f7e5ab5b`, not current operational state. Re-read Torque tasks, `git status`/`git log`, and Cerberus resource status before continuing.

**Boot context:** Torque, sprint `SP-20260911-0003`, audit `CW-20260911-0079`. Code integration reviewed at `b6b93a2c8436616995ac526735160fb530e3e1ae` on `task/torque-dogfood-launch`. All application changes stayed in Torque. Consumers own field names, tags and priority meaning; this work supplies mechanics. The custom-field decision and plugin discussion remain separate.

## Evidence

| Source | Observation and pointer |
|---|---|
| Initial live query probes | Ascending `updated_at` repeatedly returned `CW-20260819-0005` with the same cursor. Duplicate tag predicates returned no matches. HTTP ignored several MCP filters and used page length as total. Original audit comment 4461 records the failures. |
| Query implementation | Tasks 0080–0088 and 0090 contain exact commits, worker verification and parent live checks. Final query integration is b6b93a2; all identifiers here with short task numbers mean `CW-20260911-<number>`. |
| Run 1077/1078 | Codex first received duplicated `app-server`; corrected argv then exposed missing isolated credentials and terminal failure handling. Task 0103 records both failures and fixes. |
| Run 1079 | Authenticated worker implemented interruption recovery. Worker commit acc2304 became c603ed5; docs fff4ee2. Task 0101 records tests and bounded recovery semantics. |
| Run 1087 | Operator implementation stalled: log stopped around 02:47Z; at 03:27Z the process/task still appeared active despite a 30-minute assignment ceiling. Parent paused via live HTTP at 03:28Z, verified process exit, rescued changes and finished verification. Task 0088 comment 4645 records the rescue. Cause of the wall-clock gap is unproven. |
| Run 1088 | Adjacent query worker completed in review with exit 0 at 03:58Z. Worker a9a3fff became b6b93a2. Comments 4656/4657 and AAR 695 carry its verification. |
| Live adjacent probes | Fresh artifact MCP and HTTP agreed on ordered IDs and continuation for projects 30/31, sprints 16, epics 4, issues 13, comments 16 and comment-search 13 at 04:01–04:02Z. Independent legacy/task-row baselines prevented agreement on an accidentally empty cohort. No probe mutations. |
| Live tag/operator probes | Tag traversal covered 2267 rows over 62 pages at limit 37. Nine operator cases independently compared whole sprint rows, ordered traversal, totals and facets. Counts are historical observations, never test constants. |
| Operational activation | Cerberus built/synced successfully but launchctl bootstrap returned exit 5/I/O failure. Resource status followed by resource apply activated the artifact. This recurred; no Cerberus source/operator edits were made. |
| Imported reports | Thirteen agent-friction reports have current manual review notes in the sprint. No general application-bug sweep was added. Cost reporting has separate task `CW-20260912-0003`. |

## Root causes and changes

1. Timestamp cursor keys lost precision or compared unlike SQLite text formats. Query ordering and cursor boundaries now use a canonical UTC fractional representation with nine-digit precision; stable ID ties and legacy mixed formats have regression coverage. Floating epoch conversion was avoided because it can collapse nanosecond distinctions. Commit 229bcfc, task 0080.
2. Repeated tag inputs inflated the requested conjunction count beyond the number of distinct matching links. Normalize a copied filter by trimming, dropping blanks and deduplicating case-sensitively; compare distinct linked tags with distinct requested slugs. Commit 31e2fc1, task 0081.
3. Lenient adapter conversions erased malformed inputs, while separate HTTP/MCP paths drifted. Strict public parsers and a shared TaskQuery normalization/service path now define filters, validation, sorting, continuation and optional whole-cohort counts. Existing internal cheap list calls remain available. Tasks 0082–0084.
4. MCP response projection exposed SQL nullable wrappers and JSON text. Opt-in `format=typed` emits native values, retains explicit zero/null distinctions and reports malformed stored JSON through `decode_errors`. This is a read contract: input decoding can still round a large JSON number before storage. Task 0085.
5. Missing aggregate/catalog/operator surfaces forced consumers to fetch rows or use direct SQL. Task facets count the whole filtered cohort; tag discovery includes unused catalog entries; priority bounds, tag any/none and whitelisted missing/present predicates reuse the same query filters. No consumer naming policy was introduced. Tasks 0086–0088.
6. Adjacent entity list paths had mismatched options and validation. Shared normalizers now cover projects, sprints, epics, issues and comments, with opt-in HTTP cursor envelopes preserving legacy simple-call shapes. Advanced envelopes omit whole-cohort totals; legacy issue search retains its historical page-length total. Task 0090.
7. Torque launch preparation duplicated provider-owned argv and redirected CODEX_HOME before preparing credentials. Torque now supplies provider-compatible model/argv settings and prepares a private file-backed auth copy from the original composed environment. Source credentials are not rewritten or logged. Tasks 0096/0103.
8. Lost workers and terminal provider failures could leave task/run state inconsistent. Protected run linkage, conditional reconciliation, cancellation causes, write-queue draining and guarded terminal writes now prevent unsafe overwrites and reconcile explicitly linked interrupted runs. Non-lossy terminal failure delivery prevents a failed Codex turn from becoming ordinary activity. Task 0101.
9. Live steering was blocked by zero-address subscription/routing behavior. Torque-local subscription and fan-out changes enabled delivery; task-scoped comments also accept optional attribution. Tasks 0098/0099.

## Replay

Run these from the Torque checkout. Read-only production probes used the freshly deployed artifact rather than a long-lived shared stdio proxy.

```sh
git status --short
git log -20 --oneline
go test ./internal/mcpadapter -run 'Adjacent|TaskList_HTTPMCPParity' -count=1
make test
make lint
curl -fsS 'http://127.0.0.1:8990/api/v1/scheduler/status'
curl -fsS 'http://127.0.0.1:8990/api/v1/tasks?sprint_id=SP-20260911-0003&limit=7'
curl -fsS 'http://127.0.0.1:8990/api/v1/tasks/facets?sprint_id=SP-20260911-0003&dimensions=status,priority,tags'
```

Tool replay: `torque_task_get` for the audit and child threads; `torque_run_get` for 1079/1087/1088; `cerberus_resource_status(resource_id="torque-api-service")` for deployment state. New MCP schemas require a fresh process; do not silently restart another session's shared proxy. The terminal/scheduler history is evidence, not permission to kill arbitrary processes.

## Code refs

- `internal/persistence/sqlstore/tasks.go:50`: fractional timestamp layout; `:609`: count-aware paging; `:809`: shared predicate construction; `:899`: distinct tag conjunction.
- `internal/service/task_query.go:145`: shared public query; `:189`: whole-cohort facets; `:419`: presence-field whitelist.
- `internal/mcpadapter/task_typed.go:75`: strict stored-JSON decoding; `:227`: typed full record.
- `internal/mcpadapter/tag_tools.go:29`: catalog tool registration.
- `internal/service/adjacent_query.go`: adjacent normalizers; `internal/mcpadapter/adjacent_query_parity_test.go`: same-store HTTP/MCP traversal and malformed-input coverage.
- `internal/runtime/agent/codex_auth.go:19`: credential preparation; `internal/runtime/agent/codex_events.go:82`: terminal failure parsing.
- `internal/runtime/agent/manager.go:658`: explicit interrupted-run reconciliation; `cmd/torque/serve.go:244`: startup call after writer availability.
- `internal/runtime/agent/executor_longlived.go:308`: completion wait and failure handling.

## Dead ends and limits

- The cursor failure was not an ID tie-breaker alone: different stored timestamp representations and fractional precision caused repeat boundaries. Truncating seconds/fractions was not an acceptable fix.
- Treating every SQLITE_BUSY symptom as a connection-count problem would bypass the existing serialized writer. No second write path or deep SQLite tuning was introduced.
- Codex authentication failure was not evidence of a billing-mode change. The isolated home lacked prepared credentials. The fix supports file-backed auth; keyring and refresh writeback were not added.
- Typed reads cannot restore precision already lost on writes. Broad JSON input-schema work remains a reviewed agent-friction report.
- Current-row `updated_after` and facet counts do not answer transition-history or multi-table monitoring questions. The historical seven-tool monitor proposal was not implicitly approved.
- Existing PostgreSQL support does not prove query portability: parent review found existing raw pgx paths alongside SQLite-style placeholders/collations. No live PG validation or storage rewrite was performed.
- Run 1075 lacked the protected run linkage needed for safe reconciliation. It was deliberately not repaired by guessing from task identity or by raw production SQL.
- Fresh heartbeat/PID did not prove useful progress in run 1087. Machine suspension, provider delay and runtime timing remain hypotheses. A parent wall-clock guard improved supervision; it is not a persisted timeout fix.
- Per-run worktrees start from origin/main, so unpublished integration must be selected explicitly before source reads. A stale planning-only marker also caused run 1080 to plan rather than implement; later releases replaced obsolete markers before dispatch.
- Full backend and targeted checks passed. GUI lint retained 58 pre-existing errors across 34 unchanged files; changed frontend files passed. Two transient backend failures subsequently passed, without a proven root cause: wrapper boot completion and per-run worktree cleanup.
- Run-level cost 0 and aggregate estimates disagree, and cached tokens were not correctly reflected in the estimate. These numbers are not invoice or account-spend evidence; see `CW-20260912-0003`.
- Tesseract event recall failed during final capture preparation with `no such column: r.origin`, including chronological fallback. No Tesseract/mux repository or database repair was attempted.

## Where to start

Read the live sprint and its review comments. Review the consumer-field proposal on 0089 before choosing any storage model; the research child is `CW-20260912-0014`. Narrow each old agent-friction report to its surviving gap instead of reimplementing historical checklists. Continue supervised serial execution until worktree-base selection and long wall-clock stalls have a dependable operational contract. Revisit the parked plugin task after this review.

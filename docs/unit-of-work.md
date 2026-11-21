# Unit of work: dispatch a task as a Tether team

A unit of work is a Torque task that carries an execution plan. Torque (no
agent) launches a small Tether team for it, the team does the work, and the run
dissolves when the orchestrator reports its result. Torque is the dispatch
spine; Tether runs the team.

## The team

One orchestrator, one to three workers, one reviewer: three to five agents.

| Slot | Model | Talks to |
| --- | --- | --- |
| orchestrator | Astra (`team-orchestrator-astra`) | the owner seat (Torque); owns status and upward communication |
| workers (1-3) | Sol (`team-worker-sol`) or Agy (`team-worker-agy-w1`) | the orchestrator only |
| reviewer | Astra (`team-reviewer-merger-astra`) | the orchestrator only; merges on green CI |

Models come from the launch each slot's definition pin resolves to in Tether's
`teams-policy.yaml`. Torque only picks the pin. Default pins are in
`service.DefaultUnitPins`; override with `TORQUE_UNIT_PINS` (JSON).

## Worker placement

Sol workers default to `spread`: choose the configured host with the fewest
reserved workers, counting forming, active, closing and unconfirmed runs. Sorted
host names break ties deterministically. Each unit selects one worker pin, so
all workers in a multi-worker unit share that host; orchestrator and reviewer
placement stays with their existing catalog launches. This is unit balancing,
not a host health check or runtime capacity scheduler. Reservations use each
unit_run.workers count (one for historical records without a positive count);
closing and unconfirmed obligations keep their load until confirmed ended.

`unit_plan.placement` may name `worker-1` through `worker-6` or `build-1` to
select that host explicitly. `catalog` uses the original `worker_sol` pin. Agy
continues to use its catalog pin and rejects an explicit Sol host. There is no
fallback when a configured worker is unavailable: Tether refuses the formation.

Default pins use `team-worker-sol-w1` through `team-worker-sol-w6` and
`team-worker-sol-build1`. Provision their exact definition pins, launch workers
and Tether policy entries before enabling these defaults. Keep the original
`team-worker-sol` definition for compatibility. Only include eligible hosts in
`TORQUE_UNIT_PINS.worker_sol_placements`, an object mapping host names to
`{id, revision, digest}` pins. Supply the usual role pins in that same JSON
object. Explicit custom pin configurations without this map retain their base
Sol pin; an explicit host absent from the map is refused.

Before the first formation call, Torque persists the selected host and complete
formation request in `metadata.unit_run.placement` and `.formation`. The logical
workstream key and project are also sealed on the attempt. Ambiguous
retries, including after restart, reuse that request despite subsequent pin,
pool or plan changes. Pre-existing forming records without a snapshot retain
the original base-pin formation path. Existing global `TORQUE_UNIT_MAX_ACTIVE`
configuration is independent of placement and is unchanged.

## Brief routing guidance

Every generated brief names `@workers` (plural even with one worker) and
`@reviewer`. Both `tether_team_delegate` and `tether_team_address` require a
non-empty `body` and a caller-scoped idempotency `key`:

```json
{"key":"<run_id>-implementation-v1","request":{"run_id":"<run_id>","address":"@workers","body":"Implement the assigned slice and report the result."}}
```

Use this shape with `tether_team_delegate` to hand off work whose recipient
answers with `tether_team_report_result`. For a plain message, use
`tether_team_address` with its own key, address and body. Retry the same unchanged
request with the same key; a new request needs a new key. Neither tool is a
read-only roster operation. Do not guess member names or use `@all` for discovery.

### Read-only roster discovery

Use authenticated `GET /teams/roster?run_id=<run_id>` for the supplied team run ID.
This route was verified on agent-os with an existing session credential: HTTP
200, `runs[].roster_version` and `runs[].members` containing canonical `slot`,
`id`, `actor`, `status` and `kind` fields. The example uses the verified agent-os
daemon socket; other environments must use their configured daemon endpoint.
It reads the existing session credential into memory and never prints it or
passes it in command arguments. Do not substitute operator credentials.

Replace `<run_id>` with the team run ID supplied in the brief:

```bash
python3 - <<'PYTHON'
import http.client
import json
import os
import socket
from pathlib import Path
from urllib.parse import urlencode

run_id = "<run_id>"
socket_path = "/var/lib/fabric/tether/run/tetherd.sock"

class UnixHTTPConnection(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(10)
        self.sock.connect(socket_path)

token = Path(os.environ["HOLLIS_AUTH_TOKEN_FILE_TETHER"]).read_text().strip()
connection = UnixHTTPConnection("localhost")
try:
    connection.request(
        "GET",
        "/teams/roster?" + urlencode({"run_id": run_id}),
        headers={"Authorization": "Bearer " + token},
    )
    response = connection.getresponse()
    if response.status != 200:
        raise SystemExit(f"Roster request failed: HTTP {response.status}")
    payload = json.load(response)
    for roster in payload["runs"]:
        print("run", roster["run"]["id"], "roster_version", roster["roster_version"])
        for member in roster["members"]:
            print(member["slot"], member["id"], member["actor"],
                  member["status"], member["kind"], sep="\t")
finally:
    connection.close()
PYTHON
```

A discovered member is not automatically an authorized messaging target.
Continue to use the brief's canonical addresses and unchanged routing grants.
If the configured endpoint or session credential is unavailable, use the known
slot addresses for assigned work and report the discovery limitation; do not
probe guessed names or broaden permissions.

## The execution plan

Stored on the task as `metadata.unit_plan`, validated on every task create and
update (an invalid plan is refused with the field named).

| Field | Required | Default | Meaning |
| --- | --- | --- | --- |
| `workstream_id` | yes | | logical Registry ensure key / Ops workstream ID |
| `goal` | yes | | what to achieve |
| `done_when` | yes | | list (or newline-separated string) of criteria |
| `scope.in`, `scope.out` | no | | what the team may and may not touch |
| `slices` | no | | suggested order of work |
| `workers` | no | 1 | 1-3. More than 1 needs registered pool identities (`TORQUE_UNIT_WORKER_POOL`) |
| `worker_runtime` | no | `sol` | `sol` or `agy` |
| `placement` | no | `spread` | Sol host: `worker-1`..`worker-6`, `build-1`; `catalog` keeps the base pin |
| `escalate_to` | no | `@chief` | who the orchestrator escalates to, only when blocked |
| `evidence` | no | | what the result must show (CI link, PR, test output) |
| `merge_policy` | no | `reviewer_merges_on_green` | or `pr_only`, `none` |
| `release`, `deploy` | no | false | the unit includes a release or deploy (production deploys back up the database first) |
| `risk_tier` | no | `normal` | `low`, `normal`, `high` |
| `timeout` | no | 4h | run lifetime, at most 12h (Tether's ceiling) |

Unknown fields are refused.

## Create one

Create the template once (operator action):

`torque_template_create` with the contents of `docs/templates/unit-of-work.yaml`
(the same shape as `service.UnitOfWorkTemplate`). Then:

```
torque_task_create_from_template  template_id=unit-of-work  title="..."
  vars={workstream_id, goal, done_when}
```

The task is created `manual=true` (the usual rule). Approving it is one step:
set `manual=false` (`torque_task_update`) so the scheduler may dispatch it, or
dispatch it directly.

## Dispatch

On demand, scheduler on or off:

- MCP: `torque_unit_dispatch {task_id, workers?, worker_runtime?}`
- REST: `POST /api/v1/units/dispatch {"task_id": "...", "workers": 1, "worker_runtime": "sol"}`

Through the scheduler (when enabled): a picked task with a plan is handed to the
dispatcher instead of an executor. The per-project cap
(`TORQUE_SCHED_MAX_PER_PROJECT`, default 6) counts `doing` tasks as usual;
explicit dispatch bypasses the cap.

What dispatch does:

1. Validates the plan (and that more than one worker has pool identities).
2. Records attempt N on the task (`metadata.unit_run`).
3. Forms the team: `POST /teams/form` with the operator token over the unix
   socket, `Idempotency-Key = torque-unit-<task id>-a<N>`.
4. Records the run id, then briefs `@orchestrator` through `POST /teams/delegate`
   (task id, workstream, goal, done-when, scope, escalation target, evidence,
   merge policy, risk, how to report).
5. Comments the run id on the task and moves it to `doing`.
6. Tracks the run (polls `GET /teams/roster?run_id=`, default every 15s).

Idempotent: a task whose `unit_run.state` is `active` returns that run
(`reused: true`) and forms nothing.

## Finish

The orchestrator adds a task comment starting `RESULT <run id>:` (outcome, PR
links, evidence; the run id is in its brief, and Torque ignores any RESULT
comment without it, since comment authorship is not verified), then calls `tether_team_report_result`; Tether dissolves the run.
When Torque sees the run ended:

- with a `RESULT:` comment since dispatch: the task moves per `on_done` as the
  executor path does (`review` -> review, `close` -> done, `notify` -> done plus
  a `task.notify` event) and Torque posts the result. `on_review` is not
  consulted: it governs a task already in review. If the comment stays while the
  run is still live for `ResultGrace` (2 minutes), Torque dissolves the run and
  finishes the unit; a result also wins over the deadline;
- without one: the task is `blocked` with the reason;
- past the deadline with no result: Torque dissolves the run and blocks the task.

Before changing anything Torque re-checks, under its lock, that the task is
still `doing` with this run active. If the task is moved away while its team is
live, or the `unit_run` record vanishes, or the tracker panics, Torque dissolves
the run and records why. `metadata.unit_run` is written only by the dispatcher
(atomically, in the same UPDATE); a caller's metadata edit keeps the stored value.

## Failure

Torque never leaves a session running. A refused formation (a clean 4xx), or a
plan the dispatcher refuses (invalid plan, several workers with no pool), leaves
the task `todo` with `blocked_reason` set and `manual`, so the scheduler does
not pick it again. A formation call that fails any other way (timeout, dropped
connection, 5xx) may have formed a team, so it is not treated as "nothing
formed": Torque retries with the SAME idempotency key to learn the run id, and if
it still cannot, keeps the attempt in state `forming` (task `todo`, manual). The
next dispatch replays that key and gets the same run, never a second team. Calls
to Tether run on a context of their own, not the request's or the tick's.
A run that could not be confirmed ended is flagged `unconfirmed`; the task cannot
be dispatched again until the run is dissolved or seen ended. If briefing (or any Torque-side step) fails after the team formed, the run
is dissolved first. Fix the cause and dispatch again (a new attempt, new key).
If a dissolve itself fails, the reason says so; dissolve the run by hand.

## Configuration

On the `torque serve` process (restart required):

| Variable | Meaning |
| --- | --- |
| `TORQUE_TETHER_URL` | `unix:///var/lib/fabric/tether/run/tetherd.sock` |
| `TORQUE_TETHER_OPERATOR_TOKEN` | the operator credential; without it dispatch reports it is not configured |
| `TORQUE_UNIT_POLL` | run-state poll interval (default 15s) |
| `TORQUE_UNIT_TIMEOUT` | default run lifetime (default 4h) |
| `TORQUE_UNIT_WORKER_POOL` | comma-separated pre-registered worker identities, for plans with more than one worker |
| `TORQUE_UNIT_PINS` | JSON role pins and optional `worker_sol_placements` host-to-pin map; custom configurations without the map keep base-pin placement |
| `TORQUE_UNIT_MAX_ACTIVE` | units in flight (forming + running) across all projects, default 4; enforced by every dispatch, including explicit ones |
| `TORQUE_SCHED_MAX_PER_PROJECT` | scheduler per-project concurrency of `doing` tasks. **Default is now 6** (was 1), on purpose, for every task kind, not just units |

After a restart Torque re-attaches to every `doing` task whose `unit_run` is
`active`.

## Landing

The unit path does not run the executor path's landing gate on its own: the
reviewer merges on green CI, before the orchestrator reports, as part of the
protocol in the brief. Torque does not verify the merge. Moving the task to
`done` (`on_done=close` or `notify`) still passes the task service's normal
`done` checks.

## Stuck formations

A formation whose outcome stays unknown is replayed with the same key at most
`MaxReplays` (5) times or for `FormingGiveUp` (10 minutes). Then Torque looks
for the team by its definition id (`unit-<task id>-a<N>`) in Tether's rosters
and dissolves it if found; otherwise the unit is `unconfirmed`: the task stays
`todo` and manual, `blocked_reason` names the idempotency key, and it counts
against `TORQUE_UNIT_MAX_ACTIVE` until an operator checks Tether and dispatches
with `clear_unconfirmed`.

## Known limits

- The result is the orchestrator's `RESULT <run id>:` task comment, because the
  `report_result` body lands in the owner seat's mailbox and Torque does not
  read it. The orchestrator needs Torque MCP access.
- Run end is read from the roster (run status terminal, or no active member).
  The exact status string of a dissolved run is not confirmed against a live
  Tether; the active-member check is the backstop.
- Workstream ids are supplied in the plan; Torque does not create them.

## Registry identity and status stream

Torque can ensure Registry workstreams and forward dispatch status to Tesseract.
Configure the **serve process** with endpoint URLs and credential **file paths**:

| Setting | Meaning |
| --- | --- |
| `TORQUE_REGISTRY_URL` | Registry HTTP origin (optional; enables identity preparation) |
| `TORQUE_REGISTRY_TOKEN_FILE` | Required with Registry; authority-mapped dispatcher credential file |
| `TORQUE_TESSERACT_URL` | Tesseract HTTP origin (optional; requires Registry configuration) |
| `TORQUE_TESSERACT_TOKEN_FILE` | Required with Tesseract; status writer credential file |
| `TORQUE_UNIT_DEFAULT_PROJECT` | Local Torque project ID assigned to unassigned unit tasks; configure `PRJ-20261120-0001` on agent-os |
| `TORQUE_UNIT_REGISTRY_LAUNCH_CONTEXT` | Default `false`; enable `launch.task_id` / `launch.workstream_id` only after the Tether 0030 contract is deployed |
| `TORQUE_STATUS_AUTHOR` | Event author agent ID; default `torque-dispatcher` |

Credentials are read for every request, so rotating a provisioned file does not
require a Torque restart. There are no inline-token settings for these clients.
Provisioning is separate: Registry needs `registry.write` and
`registry.workstream.ensure` plus the dispatcher's authority mapping; Tesseract
needs event-write access to the intended project status namespaces. Do not copy
credentials into a task, plan, environment token value, or repository.

The logical ensure key is `metadata.unit_plan.workstream_id` for units. For
executor dispatch it is `metadata.ops_id`, then `metadata.workstream_key`, then a
legacy noncanonical `metadata.workstream_id`, otherwise the task ID. The logical
key is retained after stamping; it never becomes a caller-minted Registry ID.
Registry receives `POST /v1/workstreams/ensure` with `{key, project_urns?}` and a
stable `Idempotency-Key` derived from the project and logical key. Torque does
not supply a name or alter authored Registry attributes. Registry owns the
canonical `workstream:<ULID>`; different tasks with the same key/project resolve
to the same Registry identity under the configured service authority. Torque
resolves `project_urns` from the local project's `external_authority=registry`
and `external_id=project:<slug>`; local `PRJ-*` storage IDs never enter this
Registry domain. The local ID, canonical Registry project and namespace slug
are stored separately. An unmapped nonempty project leaves a pending
`project_unmapped` mark and launches without minting an incorrectly scoped
workstream; background retries resolve it after the project reference is fixed.
The namespace uses that project slug (or a valid local project name slug), with
`agent-os` as the fallback. Configured unit defaults are assigned before ensure,
so agent-os's default project contributes `project:agent-os` to the membership.
An existing explicit project assignment wins over the unit default.

Torque stamps `metadata.workstream_id` and the dispatcher-owned
`metadata.dispatch_workstream` (`key`, `id`, `state`, `last_error`), preserves the
original logical key in the plan, and stamps `metadata.unit_run.workstream_id`
and executor run references. Metadata edits preserve the dispatcher's binding.
A definite `workstream_closed` refuses dispatch and leaves a sticky closed
marker; subsequent outage, credential failure or restart cannot reopen that
same binding. Choose a new logical key to start new work. No local fallback mint or
generic Registry create is used.

Before launch, one ensure attempt has a 500 ms deadline. An outage or unreadable
credential file leaves a durable `pending` marker and dispatch proceeds.
Background retries run every five seconds, with a bounded batch of 100 pending
bindings; other tasks do not wait on that task's network request. Recovery
stamps the task and its corresponding executor run records. Old binding keys
and event associations are retained, so changing a plan during an outage cannot
reattribute already-recorded events to a later workstream.

The status writer reads the existing transactional `event_outbox`, not the
in-process transition hooks. It projects executor dispatch/running, review,
blocked, done and failed transitions and unit lifecycle `dispatched`, `formed`,
`RESULT`, `dissolved`, and failure events to `POST /v1/event/write`. Lifecycle
events commit atomically with `metadata.unit_run`; unchanged saves and rolled
back changes produce no duplicate lifecycle entries. Task statuses remain
Torque's canonical vocabulary (`doing` projects to stream state `progress`);
`failed` is a stream/run state, not a new task status. The closed stream
vocabulary is `started | progress | blocked | review | done | failed |
cancelled | note`: dispatched/formed become `started`, running becomes
`progress`, dissolution becomes `note`, and RESULT becomes `done`, `failed`, or
`blocked`. Explicit result prefixes (`FAILED`, `BLOCKED`, `PARTIAL`) or JSON
state/status/outcome classify failures/blocks; legacy successful free text maps
to `done`. Classification affects telemetry; task `on_done` policy is preserved.
Repeated transitions keep distinct source event IDs and idempotency keys.

Each outbound event follows the Ops `eventBody` shape: namespace
`project/<task-project-or-fallback>/event/status`, canonical `workstream_id`,
`summary`, `body`, `author.agent_id`, `session_id`, and `consumer_state`
(`task_id`, `run_id`, `team_id` for teams, `state`, `workstream_key`, plus
compatibility aliases `subject_urn`, `task`, `run`). Its `data` includes `task_id`, `run_id`,
`workstream_id`, the original Torque event ID and the unit dispatch key when
applicable. While team formation is in flight, delivery waits for the real run ID before
sealing its body. A terminal failed formation with no run keeps that unavailable
ID empty; it never invents an execution session or JTI.
The exact outbound body is sealed in SQLite before sending, with an
idempotency key based on the original event ID. Restart, timeout, or loss of an
accepted response replays that same body/key. Each failed delivery remains in a
durable pending ledger. A separate scan cursor stages new pages while bounded
retry pages rotate by attempt count, so an unmapped project or in-flight team
does not starve other tasks, including beyond one page of pending events.
The delivery watermark stays before the oldest pending event; retention cannot
prune its source envelope, binding, or sealed body. Pending events and retry
turns survive restart. A pending Registry binding delays its own status delivery
but never the launched team's execution.

The reader starts at the outbox head on first enable; it does not backfill old
dispatch history. Subsequent starts resume its persisted `tesseract-status-v1`
cursor. SQLite's commit-ordered outbox is required. A pruned log or cursor beyond a restored log head is
reported as a cursor gap, never silently skipped. Disabling the writer does not
remove its cursor or pending records; retention remains pinned until an
operator explicitly retires that consumer or delivery resumes.

Launcher correlation registration belongs to CW-20261121-0030. The reconciliation
and compaction-lineage design is already documented in
[Tether PR #637](https://github.com/hollis-labs/tether/pull/637), merged as
`7acc42a3b8bbaa76b326eb7780aa895f466c6135`. That plan preserves historical joins
while new dispatches carry Registry references. Its historical Registry holds
are superseded by the live ensure contract from CW-20261120-0090; its Tether
store/lineage migration remains a separate implementation boundary. This Torque
client does not alter that store, deploy a service, or mint credentials.

### Tether binding boundary

The current audited Tether team HTTP contract rejects unknown launch fields.
The agreed CW-20261121-0030 extension accepts `launch.task_id` and
`launch.workstream_id` and propagates them to the team and member sessions.
Torque implements that exact wire shape behind
`TORQUE_UNIT_REGISTRY_LAUNCH_CONTEXT=true`; its default is off for compatibility
with old servers. Enable it only after the matching Tether deployment. The
canonical ID remains recorded on Torque's `unit_run` and in the brief regardless
of this flag. Missing Registry identity during an outage omits the workstream
selector while retaining the real task ID on the enabled path. Late session
binding after recovery is a Tether responsibility.

The existing Tether session assignment route references a local workstream row
and is not a substitute for Registry-owned identity. The merged reconciliation
plan remains PR #637. This PR's gated wire behavior is tested against a fake
Tether; production propagation awaits 0030. Formation snapshots, including the
flag's emitted fields, remain unchanged across ambiguous retries.

## Ops system of record (ops/v2)

Contract: `caster://ops/design.system-of-record-v2@3` (Registry `document:arch.ops-system-of-record`).

| Setting | Meaning |
| --- | --- |
| `TORQUE_OPS_URL` | Ops service origin (MCP at `/mcp`), e.g. `http://127.0.0.1:8994`; enables item linking |
| `TORQUE_OPS_TOKEN_FILE` | Required with the URL; Ops operator credential file, re-read on every call |
| `TORQUE_OPS_ACTOR` | Stated actor (`X-Actor`); default `msg://service/torque/dispatcher` |
| `TORQUE_OPS_AWAITING_DEFAULT` | Who an `assignment_expired` event waits on; default the Lead Orchestrator |

**Every task has an Ops item (G1, CW-20261121-0146).** `metadata.ops_id` (the
bare item slug) is the key field; `unit_plan.workstream_id`, `workstream_key` and
a non-canonical `metadata.workstream_id` are read as aliases. A task without one
is linked when it first moves to doing, is dispatched, or emits a status event:
inherit from the parent chain (plans included), then from its epic (the item its
other tasks are filed under), else the project's unfiled item
`unfiled-<project slug>` with `metadata.unfiled = true`. At create only
inheritance runs. `metadata.ops_link` records `source` (`self`, `parent`,
`plan`, `epic`, `unfiled`) and `from`. Creates are never refused.
The unfiled item is created once per project through Ops `item_intake`
(`intake_key = torque:unfiled:<project>`, `stage: in_flight`,
`source: torque_auto`); its id is deterministic, so linking never waits on Ops.
An intake that cannot complete waits in `ops_intake_pending` and retries with
backoff (10 s to about 10 min). With Ops on, the Registry ensure key is the
`ops_id`: Torque never mints a per-task Registry workstream.

**Lifecycle events carry the item (G2, CW-20261121-0147).** Torque keeps posting
directly to the status stream (above) and adds `ops_id` and
`kind: task_<state>` to `consumer_state` (and `ops_id` to `data`). Unit
`formed` now maps to `note`, so a unit dispatch produces one `started`.

**Assignments expire with their holder (G6, CW-20261121-0148).** The typed task
record (MCP `format=typed`, HTTP task responses) carries a top-level
`assignment` object, null when the task was never assigned:
`assignee_urn, assignee_name, tether_session_id, team_run_id, claim_id,
source (owner|doing|claim|dispatch), state (active|expired|released),
assigned_at, lease_expires_at, ended_at, end_reason
(session_ended|lease_lapsed|released|done)`. Torque records unit team runs
(lease = unit deadline), runs bound to a Tether session, and workstream claims;
`metadata.owner_urn` is projected as `source: owner`. Every 30 s it releases
assignments whose task finished and expires those whose team run failed,
whose session ended (Tether `GET /sessions/{id}`), or whose lease lapsed. An
expired row keeps its holder, gets a task comment, and is posted to the status
stream as `state: blocked`, `kind: assignment_expired`, with `assignee_urn`,
`tether_session_id`, `assigned_at` and `awaiting` in `consumer_state`.
`source: doing` (the caller who moved the task) needs the gateway to forward
caller identity and is not recorded yet.

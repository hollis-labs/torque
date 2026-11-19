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
| `workstream_id` | yes | | Tether workstream the unit runs under |
| `goal` | yes | | what to achieve |
| `done_when` | yes | | list (or newline-separated string) of criteria |
| `scope.in`, `scope.out` | no | | what the team may and may not touch |
| `slices` | no | | suggested order of work |
| `workers` | no | 1 | 1-3. More than 1 needs registered pool identities (`TORQUE_UNIT_WORKER_POOL`) |
| `worker_runtime` | no | `sol` | `sol` or `agy` |
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
| `TORQUE_UNIT_PINS` | JSON override of the role definition pins |
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

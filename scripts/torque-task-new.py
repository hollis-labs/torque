#!/usr/bin/env python3
"""
torque-task-new — create a Torque task with the standard worker-pipeline defaults.

The new task pipeline (PRs #76–#83 + worker/reviewer template upgrade) expects every
worker-dispatched task to flow through the same shape: long-lived worker → PR submit
→ Copilot poll → self-transition to review → reviewer end-agent audits + tags
`agent-closed` + merges PR + transitions to done.

This script is a thin wrapper around POST /api/v1/tasks that pins the defaults
matching that pipeline so the operator only supplies title + prompt + working_dir.

Force-override applies: every new task starts manual=true. The operator flips it
to false when ready (intentional gate during stabilization phase). The script
prints the flip command for convenience.

Usage:
    scripts/torque-task-new.py \\
        --title "Fix X in Y" \\
        --prompt "<the prompt the worker will receive as system_prompt>" \\
        --working-dir /Users/chrispian/dev/hollis-labs/apps/torque

Optional:
    --description    Long description; defaults to --prompt.
    --project-id     Defaults to PRJ-20260416-0001 (Torque).
    --sprint-id      e.g. SP-20260519-0001
    --parent-id      e.g. CW-20260519-0134 (plan id)
    --phase-id       e.g. ph-2 (only meaningful with --parent-id)
    --executor       cli (default) | opencode
    --profile        implementer-long (default) | implementer | etc.
    --priority       2 (default); lower number = higher priority
    --kind           agent (default) | issue | internal | parent | plan
    --tags           comma-separated, e.g. "stability,substrate"
    --api            http://localhost:8990 (default)
    --no-pipeline    Use on_done="close" + agent_profile="implementer" — for
                     bounded mechanical tasks that don't produce a PR. Skips
                     the reviewer-audit + PR pipeline.
    --print-payload  Print the JSON payload that would be POSTed, then exit.
                     (Dry run; nothing is sent to the daemon.)

Outputs the new task ID on stdout (suitable for chaining).
"""
import argparse
import json
import os
import sys
import urllib.error
import urllib.request


# Defaults pinned to the worker pipeline established by PRs #76–#83 + the
# default-worker / default-end-agent template upgrade. Changing these is a
# substrate-level decision — coordinate before editing.
PIPELINE_DEFAULTS = {
    "kind": "agent",
    "executor": "cli",
    "agent_profile": "implementer-long",
    "on_done": "review",  # reviewer end-agent fires on review → audits → done
    "on_fail": "retry",
    "on_review": "pause",  # field is currently inert but kept for forward-compat
    "on_checkpoint_response": "resume",
    "checkpoint_mode": "none",  # opt-in to blocking; default is non-blocking
    "max_retries": 3,
    "trust": "normal",
    "priority": 2,
    "source_type": "user",
}

# For bounded-mechanical tasks that don't produce a PR (e.g. one-shot
# config rolls, single-API-call tasks). Skips the reviewer pipeline.
NO_PIPELINE_OVERRIDES = {
    "agent_profile": "implementer",
    "on_done": "close",
}

DEFAULT_PROJECT_ID = "PRJ-20260416-0001"
DEFAULT_API = "http://localhost:8990"


def main() -> int:
    p = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    p.add_argument("--title", required=True)
    p.add_argument("--prompt", required=True, help="becomes task.system_prompt")
    p.add_argument("--description", help="defaults to --prompt")
    p.add_argument("--working-dir", required=True)
    p.add_argument("--project-id", default=DEFAULT_PROJECT_ID)
    p.add_argument("--sprint-id")
    p.add_argument("--parent-id")
    p.add_argument("--phase-id")
    # --executor and --profile default to None sentinels so we can distinguish
    # "user passed it" from "use the per-kind default". The API rejects a
    # non-empty executor for kind=issue / kind=external; we only include
    # executor in the payload when (a) the user passed --executor explicitly,
    # or (b) kind is one of the executable kinds (agent/internal). Same logic
    # protects --profile from being applied to non-executable kinds.
    p.add_argument("--executor", default=None)
    p.add_argument("--profile", default=None)
    p.add_argument("--priority", type=int, default=PIPELINE_DEFAULTS["priority"])
    p.add_argument("--kind", default=PIPELINE_DEFAULTS["kind"])
    p.add_argument("--tags", help="comma-separated tags")
    p.add_argument("--api", default=os.environ.get("TORQUE_API", DEFAULT_API))
    p.add_argument(
        "--no-pipeline",
        action="store_true",
        help="bounded mechanical task; skip reviewer audit + PR pipeline",
    )
    p.add_argument(
        "--print-payload",
        action="store_true",
        help="print the JSON payload that would be POSTed; do not send",
    )
    args = p.parse_args()

    payload = dict(PIPELINE_DEFAULTS)
    if args.no_pipeline:
        payload.update(NO_PIPELINE_OVERRIDES)

    # Executable kinds run a process; non-executable kinds (issue, plan,
    # parent, external, …) MUST NOT carry an executor/profile or the API
    # rejects them.
    is_executable_kind = args.kind in ("agent", "internal")

    # Resolve executor and profile: explicit --executor / --profile always
    # wins; otherwise use the per-kind default. For non-executable kinds
    # we omit both fields entirely.
    if args.executor is not None:
        resolved_executor = args.executor
    elif is_executable_kind:
        resolved_executor = payload["executor"]
    else:
        resolved_executor = None

    if args.profile is not None:
        resolved_profile = args.profile
    elif is_executable_kind:
        resolved_profile = payload["agent_profile"]
    else:
        resolved_profile = None

    payload.update({
        "title": args.title,
        "description": args.description or args.prompt,
        "system_prompt": args.prompt,
        "working_dir": args.working_dir,
        "priority": args.priority,
        "kind": args.kind,
        "project_id": args.project_id,
    })
    if resolved_executor is not None:
        payload["executor"] = resolved_executor
    else:
        payload.pop("executor", None)
    if resolved_profile is not None:
        payload["agent_profile"] = resolved_profile
    else:
        payload.pop("agent_profile", None)

    metadata = {}
    if args.phase_id:
        metadata["phase_id"] = args.phase_id
    if metadata:
        payload["metadata"] = metadata

    if args.sprint_id:
        payload["sprint_id"] = args.sprint_id
    if args.parent_id:
        payload["parent_id"] = args.parent_id

    if args.tags:
        payload["tags"] = [t.strip() for t in args.tags.split(",") if t.strip()]

    if args.print_payload:
        print(json.dumps(payload, indent=2))
        return 0

    url = args.api.rstrip("/") + "/api/v1/tasks"
    req = urllib.request.Request(
        url,
        method="POST",
        headers={"Content-Type": "application/json"},
        data=json.dumps(payload).encode("utf-8"),
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            body = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8", errors="replace")
        print(f"ERROR: HTTP {e.code} from {url}", file=sys.stderr)
        print(f"  {err_body}", file=sys.stderr)
        return 1
    except (urllib.error.URLError, OSError) as e:
        print(f"ERROR: cannot reach {url}: {e}", file=sys.stderr)
        return 2

    task_id = body.get("id")
    status = body.get("status")
    manual = body.get("manual")

    # Human-friendly summary on stderr; task id alone on stdout for chaining.
    print(f"created: {task_id}", file=sys.stderr)
    print(f"  title: {body.get('title')}", file=sys.stderr)
    print(f"  status: {status}  manual: {manual}", file=sys.stderr)
    print(f"  kind: {body.get('kind')}  agent_profile: {body.get('agent_profile')}", file=sys.stderr)
    print(f"  on_done: {body.get('on_done')}  on_fail: {body.get('on_fail')}", file=sys.stderr)
    print(f"  working_dir: {body.get('working_dir')}", file=sys.stderr)
    print(f"", file=sys.stderr)
    print(f"  flip to ready when you're ready to dispatch:", file=sys.stderr)
    print(
        f"    curl -s -X PUT -H 'Content-Type: application/json' \\\n"
        f"      -d '{{\"manual\": false}}' \\\n"
        f"      {args.api.rstrip('/')}/api/v1/tasks/{task_id}",
        file=sys.stderr,
    )
    print(task_id)
    return 0


if __name__ == "__main__":
    sys.exit(main())

# HITL Workflows

Human-in-the-loop workflows are typed checkpoint flows. The checkpoint row is
still the durable object; `checkpoints.type` is the workflow discriminator.

Canonical presets:

- `pr_review` — pull request review decision.
- `approval` — non-PR approval gate.
- `message` — message/acknowledgement flow.

Schema lookup:

- Go/API code: `internal/hitl.Lookup(type)`, `PayloadSchema(type)`, and `ResponseSchema(type)`.
- HTTP clients: `GET /api/v1/checkpoint-workflows` or `GET /api/v1/checkpoint-workflows/{type}`.
- GUI code: `apps/gui/src/lib/hitl-workflows.ts`.

Unknown workflow types are valid. Lookup returns `known=false` with permissive
object schemas for payload and response, so older clients can render and submit
opaque JSON instead of failing.

Task metadata contract is reserved, not enforced here. A task may declare:

```json
{
  "hitl": {
    "workflow_type": "approval",
    "requirements": { "response_required": true, "min_responders": 1 },
    "enforcement_mode": "advisory"
  }
}
```

The preferred required-workflow form nests the contract so it can coexist with
other HITL metadata:

```json
{
  "hitl": {
    "required_workflow": {
      "workflow_type": "approval",
      "requirements": {
        "response_required": true,
        "allowed_responder_source_types": ["user"],
        "min_responders": 1
      },
      "enforcement_mode": "required",
      "reason": "permission before restart"
    }
  }
}
```

Backend enforcement is intentionally split. The substrate validates policy
shape, rejects `required` policies it cannot enforce deterministically (for
example multi-responder quorum), and rejects required workflow responses from
disallowed responder source types before closing the checkpoint. Emitting the
checkpoint before a sensitive step, recognizing surprise situations that need
guidance, and any multi-party approval process remain process-level duties for
the caller/prompt until the scheduler grows deeper workflow semantics.

## PR Review

Create a blocking task that resumes after a checkpoint response:

```bash
curl -sS -X POST "$CW/api/v1/tasks" \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Review checkout fix",
    "description": "Review the PR and continue when accepted.",
    "kind": "decision",
    "manual": true,
    "checkpoint_mode": "blocking",
    "on_checkpoint_response": "resume",
    "metadata": {
      "hitl": {
        "workflow_type": "pr_review",
        "requirements": { "response_required": true, "min_responders": 1 },
        "enforcement_mode": "advisory"
      }
    }
  }'
```

When the task is `doing`, emit the checkpoint:

```bash
curl -sS -X POST "$CW/api/v1/checkpoints" \
  -H 'Content-Type: application/json' \
  -d '{
    "task_id": "CW-123",
    "type": "pr_review",
    "payload_json": "{\"pr_url\":\"https://github.com/acme/app/pull/42\",\"title\":\"Fix checkout\",\"summary\":\"Ready for review.\"}",
    "emitter_source_type": "agent"
  }'
```

The task parks in `review` with `blocked_reason` containing the emitted
`correlation_id`. Respond with a typed response:

```bash
curl -sS -X POST "$CW/api/v1/checkpoints/01HX.../respond" \
  -H 'Content-Type: application/json' \
  -d '{
    "response_json": "{\"decision\":\"approve\",\"summary\":\"Looks good.\"}",
    "responder_source_type": "user"
  }'
```

With `on_checkpoint_response=resume`, the service stores the decoded response
under `task.metadata.checkpoint_responses[correlation_id]`, clears the blocked
reason, and transitions the task back to `todo` for the next scheduler pass.

## Approval

Use `approval` for a non-PR gate:

```bash
curl -sS -X POST "$CW/api/v1/checkpoints" \
  -H 'Content-Type: application/json' \
  -d '{
    "task_id": "CW-456",
    "type": "approval",
    "payload_json": "{\"title\":\"Deploy staging\",\"prompt\":\"Approve staging deploy?\",\"context\":{\"env\":\"staging\"}}",
    "emitter_source_type": "agent"
  }'
```

Respond:

```bash
curl -sS -X POST "$CW/api/v1/checkpoints/01HY.../respond" \
  -H 'Content-Type: application/json' \
  -d '{
    "response_json": "{\"decision\":\"approved\",\"comment\":\"Deploy during the next window.\"}",
    "responder_source_type": "user"
  }'
```

The same resume path applies for blocking tasks with
`on_checkpoint_response=resume`. For `on_checkpoint_response=review`, the
response is stored but the task remains in `review`.

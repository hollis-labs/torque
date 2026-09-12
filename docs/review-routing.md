# Review Routing

Torque's default review behavior is unchanged: when a `kind=agent` task enters
`review`, the lifecycle hook enqueues one internal reviewer task with
`agent_profile=reviewer-end-agent`.

Tasks can opt out of that internal reviewer with task metadata:

```json
{
  "review": {
    "mode": "parent"
  }
}
```

`mode=parent` means the target task remains in `review` and Torque does not
enqueue a `reviewer-end-agent` child. A parent task, dogfood batch supervisor,
or human operator owns the review/disposition from there.

`mode=end_agent` is accepted as an explicit spelling of the default. Omitting
`metadata.review` is equivalent to `end_agent` for `kind=agent` tasks. Non-agent
kinds (`parent`, `plan`, `internal`, `wait`, `decision`, `issue`, `external`)
never enqueue internal reviewers, regardless of metadata.

Task reads expose the resolved behavior as `effective_review`:

```json
{
  "effective_review": {
    "mode": "parent",
    "enqueue_internal_reviewer": false
  }
}
```

The HTTP and MCP task create/update paths validate the explicit metadata
contract. Invalid values such as `{"review":{"mode":"claude"}}`, a non-object
`review`, or a missing/non-string `review.mode` are rejected as
`metadata.review` validation errors. If invalid metadata is somehow already
stored and a task reaches `review`, Torque refuses to enqueue the default
reviewer and records an observable `[system/end-agent] failed to enqueue`
comment instead of silently falling back.

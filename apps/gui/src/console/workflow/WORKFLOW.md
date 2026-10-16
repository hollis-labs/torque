# Workflow / steering cards (H2)

`import { ... } from '@/console'`. Needs `<ConsoleProvider>` > `<CardRuntimeProvider registry>` > `<WorkflowProvider client flows>`;
call `registerWorkflowKinds(registry)` once. Showcase: `WorkflowPreview` on `pages/WidgetPreviewPage`. Backend needs: `BACKEND_NEEDS.md`.

## Card kinds

| Kind | Purpose | Controls | Step-up (IdentityConfirm) |
|---|---|---|---|
| `workflow.progress` | phases, per-phase progress bar, gate chips | read only | - |
| `workflow.steer` | steer a run | pause, resume, redirect, escalate, priority | redirect, priority (`workflow:steer`) |
| `workflow.gate` | a human gate (Hadron gate/signal) | pass, fail, override (note required) | pass, override (`workflow:gate`) |
| `workflow.plan` | approve / edit / reject a proposed plan | untick phases, approve N of M, reject | approve (`workflow:plan:approve`) |
| `workflow.checkpoint` | answer a Torque task checkpoint | option and/or free text | none (answer only) |
| `workflow.decision` | one step of an authored flow (below) | the step's actions, optional text input | per step action (`confirmScope`) |

Rule of thumb: the safe direction (pause, fail a gate, reject, escalate) needs no step-up; anything that lets the agent
continue or changes what it does does. All controls call `controller.run(spec)`; specs are built by `steerAction`,
`gateAction`, `planAction`, `checkpointAction` (model.ts) and handled by `registerWorkflowHandlers` (ActionBus kinds in `WORKFLOW_ACTION`).

## Reacting to state and events

Run state lives behind `WorkflowClient` (mock: `MockWorkflowClient`). `WorkflowProvider` feeds every Console event to
`client.ingest?.(e)` (the mock folds it with `applyRunEvent`; a live client gets projections from the server) and to the flow runner.
`runToCards(run)` derives the cards; cards re-render as the run changes and close (phase `resolved`) when a gate/plan/checkpoint is decided.
Event kinds are placeholders in `WORKFLOW_EVENT_KINDS` (`phase.started|completed|failed`, `gate.evaluated`, `run.state_changed`,
`progress.checkpoint`, `step.executed`); the run id is `payload.runId`, a `run` subject, or the correlation id. Swap the table, not the components.

## Authoring a flow (JSON, no GUI code)

A `FlowDefinition` declares **trigger -> card -> action -> next**:

```json
{ "id": "agent-blocked", "version": 1, "title": "Unblock a stuck agent", "start": "alert",
  "steps": [
    { "id": "alert",
      "trigger": { "event": { "kinds": ["run.state_changed"], "payloadEquals": { "to": "blocked" } } },
      "card": { "title": "Agent blocked: {payload.reason}", "severity": "warning", "surfaces": ["toast", "tray"] },
      "actions": [ { "id": "triage", "label": "Triage", "intent": "primary", "next": "steer" },
                   { "id": "ignore", "label": "Ignore", "next": "end" } ],
      "timeoutMs": 1800000, "onTimeout": "end" },
    { "id": "steer", "trigger": { "immediate": true },
      "card": { "title": "Redirect the agent", "input": { "label": "Instruction", "required": true } },
      "actions": [ { "id": "send", "label": "Redirect", "confirmScope": ["workflow:steer"],
                     "dispatch": { "kind": "workflow.steer", "params": { "runId": "{runId}", "command": { "type": "redirect", "instruction": "{input}" } } },
                     "next": "done" } ] },
    { "id": "done", "trigger": { "event": { "kinds": ["run.state_changed"], "payloadEquals": { "to": "running" } } },
      "card": { "title": "Unblocked" }, "actions": [] } ] }
```

- **trigger**: `event` (kinds, kindPrefix, minSeverity, subjectType, payloadEquals) waits for a matching Console event; `immediate` shows the step as soon as the previous action completes. The start step must use `event`.
- **card**: rendered as `workflow.decision`. Templates: `{runId}`, `{payload.x}` (merged payload of the trigger events), `{input}` (the step's text field, actions only).
- **action**: `dispatch` runs any ActionBus kind first (the flow advances only if it returns ok; errors show in the card); `confirmScope` puts one IdentityConfirm in front of the dispatch; `next` is a step id or `end`.
- A step with no actions is a terminal notice. `timeoutMs` + `onTimeout` auto-advance an unanswered step (default `end`).
- `validateFlow(json)` returns `{ok, errors, warnings, def}` (unknown `next`, duplicate ids, missing start trigger; warns on unreachable steps and no terminal). Pass validated defs to `<WorkflowProvider flows>`.
- One live instance per (flow, run id): repeat start events while it runs are ignored.

`SAMPLE_BLOCKED_FLOW` is the end-to-end sample: agent blocks -> alert -> decision card -> steer (step-up) -> resolution when the run reports `running` again.
Test: `workflow.test.tsx` (client rules, event fold, flow engine, all cards, the end-to-end flow).

## Not built (follow-ups)

Multi-step wizard card and a standalone decision-task card (the `workflow.decision` flow step covers the decision case; a wizard is a flow of steps with inputs).

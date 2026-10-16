# Workflow / steering cards: backend needs (H2)

Frontend only. `MockWorkflowClient` (client.ts) is the only `WorkflowClient`; the live one maps onto the calls below behind the same interface.
Event kinds in `WORKFLOW_EVENT_KINDS` (model.ts) are placeholders for the data team's kinds (gate.evaluated, phase.*, <entity>.state_changed,
step.executed, progress.checkpoint); remap there.

1. **Run read model.** `list/get/subscribe` returning `WorkflowRun` (types.ts): state, priority, agent, phases with status and progress,
   gates (criteria, requiresHuman, status), pending plan, pending checkpoint, subjects, audit. The mock folds events itself (`ingest`);
   live should serve a server-side projection and push changed runs (or events carrying the run id) so every device converges.
2. **Steering** (`steer`): pause, resume, redirect(instruction), escalate(to, note?), priority. Wraps M1/Tether (message to the agent, session
   pause/resume); the server must be idempotent per `(run, command, confirmation token)` and reject on non-active runs. Redirect delivery and
   whether "pause" is cooperative or a hard stop is undecided.
3. **Gates** (`submitGate`): `hadron_workflow_gate_submit` with decision pass/fail/override + note. Override must require a reason server-side and be
   audited as an override, not a pass. A failed gate should block the run.
4. **Checkpoints** (`respondCheckpoint`): `torque_task_checkpoint_respond`; options come from the checkpoint payload. Pending list: `torque_task_checkpoints_pending`.
   No step-up on answer today; if a checkpoint can authorise a change the server should flag it so the GUI can require one.
5. **Plans** (`decidePlan`): approve with the included subset (an edit) or reject. Needs a plan-proposal event/record (who proposed, items, risk) and the
   edit semantics (dropped phases are skipped, not deleted). No such Torque/Hadron API was found.
6. **Identity step-up.** Redirect, priority, gate pass/override and plan approve forward `ConfirmResult.token`; the server verifies who, scope
   (`workflow:steer`, `workflow:gate`, `workflow:plan:approve`), expiry and writes the confirmation to the audit (never the token).
   Scope names are proposals for the auth workstream.
7. **State events.** Emit `run.state_changed{runId,to,reason}` after any accepted action (flows wait on e.g. `to: running` to resolve), plus `phase.*`,
   `gate.evaluated{runId,phaseId,gateId,label,passed,requiresHuman,criteria[]}`, `progress.checkpoint{runId,phaseId,done,total}`.
8. **Flow definitions.** Today `FlowDefinition` JSON is loaded by the app. Server-stored, versioned definitions (owner, enabled, per-flow audit)
   and server-side instance state would let flows survive reloads and run on every device; the GUI runner keeps instances in memory only.
   Flow dispatch uses ActionBus kinds, so a definition can only do what the GUI already registers; no server-side authority is implied.
9. **Loop guard.** A flow dispatch that emits events matching its own start trigger must not retrigger; the envelope's `corr.depth` should be honoured.

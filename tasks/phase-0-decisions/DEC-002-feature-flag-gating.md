# DEC-002 — Feature-flag gating vs always-on for Project/Epic/Sprint tools

**Phase:** 0 — Decisions
**Status:** todo
**Depends on:** none
**Blocks:** ENT-PROJECT, ENT-EPIC, ENT-SPRINT
**Source:** ADR-0004 §6 (Open questions)

## Summary

Project/Epic/Sprint MCP tools are currently registered only when their
feature flag is enabled (`registerOptInTools`). ADR-0004's stated goal is
that "an agent using Torque purely as a task/project tracker (no execution)
should find the surface just as natural as an agent that dispatches work
through it" — which implies these tools should arguably be available in
pure-tracking mode regardless of execution-related feature flags. The ADR
flags this explicitly as undecided and notes it "may have implications
beyond MCP (GUI, HTTP)."

## Decision to make

1. **Keep current gating** — Project/Epic/Sprint tools stay behind
   `registerOptInTools`/their feature flag, unchanged. Phase 4 entity work
   lands inside the existing gate.
2. **Always-on for pure tracking** — register these tools unconditionally,
   decoupling "task/project data management" from "execution enabled."
   Requires checking whether the same flag also gates non-MCP surfaces (GUI
   routes, HTTP endpoints) that would need matching treatment, or whether
   the decoupling is MCP-only for now.

## Acceptance criteria

- [ ] Decision recorded with rationale.
- [ ] If always-on is chosen: confirm scope — MCP-only, or does GUI/HTTP
      also need the same decoupling in this pass? (If GUI/HTTP is a bigger
      lift, it's fine to scope this decision to MCP only and flag GUI/HTTP
      as a separate future task — just say so explicitly, don't leave it
      ambiguous.)
- [ ] ENT-PROJECT, ENT-EPIC, ENT-SPRINT unblocked with a clear registration
      instruction.

## Out of scope

Any GUI/HTTP registration changes beyond what the decision explicitly opts
into — this is an MCP-scoped ADR (ADR-0004 §title).

## Outcome

_(fill in when resolved)_

# Torque vNext

**Status: exploration. Nothing here is decided or scheduled.**

This directory holds a target-architecture sketch produced in a design session
on 2026-09-07 and the context that produced it. It is not a contract, not a
plan, and not an implementation mandate. `AGENTS.md`, the code, and the docs
listed in [`../README.md`](../README.md) remain authoritative for how Torque
behaves today.

Read in this order:

- [context.md](context.md) — what was reviewed, what the neighbouring apps
  solved, which prior documents this supersedes or inherits from, and the
  session's operating constraints.
- [architecture.md](architecture.md) — the sketch itself, ending with the
  open questions a following session should work down.
- [plan.md](plan.md) — the phase plan against the sketch. Records created under
  plan CW-20260907-0047.
- [execution-vocabulary.md](execution-vocabulary.md) — the activation /
  evaluation / queue / assignment / worker / executor decomposition, checked
  against industry naming, with the hollis-labs library map. Portfolio-scoped;
  a candidate for promotion out of this repo.

## What a following session should know

Eight forks were settled in-session with Chrispian and are recorded in
[context.md](context.md#session-rulings). They are session rulings, not
ADRs — they constrain this sketch, and a following session may reopen any of
them, but should do so deliberately rather than by drifting.

The previous exercise of this kind produced
[`../work-coordination-direction.md`](../work-coordination-direction.md)
(2026-08-22, ~1,500 lines, status *draft*). That document was read in full
during this session and treated as information to validate, not as decisions
to inherit. Its boundary analysis holds up well and is quoted where it does;
its twenty closing questions are largely answered by the rulings below.

Torque has no external consumers and is not released. Backward compatibility
is not a constraint on this sketch. The shared `hollis-labs` libraries can be
changed to suit Torque's needs where that is the better seam.

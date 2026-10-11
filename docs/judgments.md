# Structured judgments (classification, tagging, enrichment)

A model such as JEV answers narrow questions about a piece of text: **noul** (the probability a
statement is true), **choice** (pick one of a fixed set) and **score** (place on ordered levels),
each with probabilities. Torque uses that to suggest tags and fields that help triage, manage and
search work. The model chooses among options Torque defines; it never writes text. Torque owns the
questions, the thresholds and what happens with an answer, and every answer is recorded in a
ledger. **Nothing here changes a task**: an answer is a suggestion until a person (or, later, a
measured rule) decides.

Code: `internal/judge` (sets, confidence, evaluation), `internal/service/judge.go`,
`internal/persistence/sqlstore/judgments.go` (migration 041), `cmd/torque/judge.go`.
Design and rationale: Tesseract workspace draft `jev_decision_provider_research_and_design_2026_10_11`.

## Question sets

Versioned YAML in `internal/judge/sets/`, embedded in the binary and validated on load (unknown keys
are errors; at most 255 options per choice, 2 to 10 levels per score). A set names the task fields
sent as state (with character limits), the questions, how an answer maps to a suggested tag, a
`suggest_min` confidence, and `label_tags`, the existing tags used to evaluate it. Bump `version`
when the wording changes: the ledger records the set's hash, so answers to old wording are never
mixed with new. Questions follow the vendor's guidance: short, one judgment each, state referenced
by backticked field name, contrastive option descriptions, an `other` option.

Sets today (from `docs/task-tagging-conventions.md`): `task-kind`, `task-decision-state`,
`task-layer`. Choice options are shuffled per request with a seed, because the model leans toward
the first option.

## Confidence is not correctness

Confidence measures how concentrated the answer is (formulas from docs.typesafe.ai/confidence;
a yes/no answer has none, so Torque uses |2p-1|). A confident answer can be wrong. Thresholds
come from evaluation on our own data, never from a vendor example.

## Evaluating a set

```
torque judge sets
torque judge eval --set task-kind [--n 200] [--seed 1] [--no-flips] [--decider baseline]
```

Labels come from the tags tasks already carry (real but noisy: applied by many agents and people).
A choice needs exactly one of its label tags; a yes/no is positive when the task carries the tag and
negative only when it carries other tags but not this one (an untagged task says nothing). A choice
samples the natural mix; a yes/no samples balanced positives and negatives. The report gives per
question: accuracy; accuracy by confidence band; what acting above each threshold would cover and
get right; option-order flips; the most confident disagreements (review them, the old tag may be
the noisy side); and for yes/no, the positive and negative label counts with a **warning when there
are fewer than 20 positives** (accuracy is then mostly the rate of saying no).

The `baseline` decider is bag-of-words: no model, no network. It is the floor a real model has to
beat. The `tether` decider asks Tether's Decide operation (`POST /ai/decide`), which routes to JEV.

## The ledger

`judgments` (migration 041): one row per question answered about one subject by one version of a
set: answer, probabilities, confidence, the tags it would suggest, a hash of the state (never the
text), provider, versioned model, tokens, latency, and a disposition. Idempotent on (subject, set
wording, question, state hash); when a task's text or the set's wording changes, suggestions still
open are marked `superseded`; a decided row (`accepted`, `rejected`, `auto_applied`) never changes.

```
torque judge enrich --set task-kind --task CW-… [--record]   # dry run unless --record
torque judge ledger [--set …] [--subject …] [--disposition …]
```

## Adding a set

Add a YAML file under `internal/judge/sets/`, run `go test ./internal/judge/` (it validates every
embedded set), then `torque judge eval` it before it suggests anything. Set `suggest_min` from the
evaluation.

## Running the live evaluation

Needs Tether's `Decide` operation and the JEV provider (CW-20261010-0041), the owner's key stored
in Tether, and `TORQUE_TETHER_URL` / `TORQUE_TETHER_TOKEN` in the environment. Torque never sees the
provider key.

```
torque judge eval --set task-kind --decider tether --estimate     # calls and cost, calls nothing
torque judge eval --set task-kind --decider tether --yes --concurrency 8 --max-cost 0.50 --out kind.json
```

A decider that spends money prints its estimate and stops unless you pass `--yes`; `--max-calls`
and `--max-cost` stop a run before it passes a limit and the report says it was truncated. State is
scrubbed of recognisable credentials (tokens, keys, PEM blocks) before it is sent, and a token or
task text never appears in an error. Calls are retried only on 429 and overload statuses.
Estimates (2026-10-11, 300 labeled per question, every example asked twice for the option-order
check): task-kind about 600 calls and $0.03; task-layer about 1,160 calls and $0.04;
task-decision-state about 1,010 calls and $0.04, at $0.042 per million input tokens (the provider
adds about 250 input tokens of fixed overhead to every request).

### What counts as a go (proposed before seeing any numbers)

Per question, on the evaluation, compared with the baseline on the same sample:

- It must clearly beat the baseline: at least ten points more accurate overall.
- A suggestion threshold exists: some confidence at or above which at least 80% of the answers
  agree with the existing tag AND at least 30% of the labeled examples are covered. That threshold
  becomes `suggest_min`. Review its most confident disagreements by hand first: when the old tag
  is the wrong one, the model is not.
- Option-order flips on a choice stay under 10%; a higher rate means the options need rewording.
- A yes/no question needs at least 20 positive labels, or hand labels, before it is judged.

Anything short of that stays on the idea list or gets reworded and re-evaluated. A go means
"suggest to a person", never "apply".

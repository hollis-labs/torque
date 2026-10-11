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
beat. The `jev` decider arrives with Tether's Decide operation (CW-20261010-0041).

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

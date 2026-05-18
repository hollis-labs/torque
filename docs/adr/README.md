# Architecture Decision Records

This directory holds Torque's Architecture Decision Records (ADRs): durable,
numbered records of significant design decisions and the contracts they lock.

An ADR is written when a decision constrains future work — a contract other
tasks build against, an interface other code must satisfy, or a tradeoff that
should not be silently re-litigated. ADRs are append-only: a later ADR may
supersede an earlier one, but the earlier file stays in place as history.

Files are numbered sequentially (`NNNN-short-slug.md`). Each ADR carries a
`Status` (`Proposed` / `Accepted` / `Superseded`) and a date.

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-torque-messaging-design-lock.md) | Torque Messaging — design lock (steering + federation contracts) | Accepted |

# PostgreSQL migration overrides

The root numbered SQL files are SQLite migrations. PostgreSQL reads the same
filename from this directory and records the root filename in
`schema_migrations`, preserving the applied-once version key across stores.
Missing PostgreSQL overrides fail before executing SQL; SQLite rebuilds and
PRAGMA statements must never be used as a PostgreSQL fallback.

Migration 035 supports upgrades of a populated PostgreSQL schema already at
034. Historical PostgreSQL bootstrap is tracked separately in
CW-20261001-0622; this directory does not make those earlier migrations portable.

To run the PostgreSQL migration tests, supply
`TORQUE_MIGRATION_TEST_POSTGRES_DSN` for a disposable PostgreSQL database. Tests
create and remove their own schema and do not read `TORQUE_POSTGRES_DSN`.

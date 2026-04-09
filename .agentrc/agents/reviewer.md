# Reviewer Context — Clockwork Manifold

> Project-specific review conventions. Loaded by the reviewer agent role.

## Stack

- **Backend:** Go 1.26, SQLite
- **Frontend:** React 19, Vite, Tailwind CSS 4, shadcn/ui, TypeScript
- **Testing:** Go test (backend), no frontend tests yet

## Review Focus

- Go: error handling, race conditions, SQL injection, interface compliance
- Frontend: component boundaries, accessibility, Tailwind conventions, TypeScript strictness
- Cross-cutting: API contract consistency between daemon and GUI

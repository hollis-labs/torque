# Clockwork Manifold

Standalone task orchestration and execution engine.

## Stack
- **Backend:** Go 1.26, SQLite
- **Frontend:** React 19, Vite, Tailwind CSS 4, shadcn/ui, TypeScript (apps/gui/)
- **CLI:** `cmd/clockwork/` (client), `cmd/clockworkd/` (daemon)

## agentrc
- If `.agentrc/boot-prompt.md` exists, read it first for session context.
- If the user says "Boot <agent>", look up the agent in `.agentrc/config.yaml` under `agents:`. Load each role file from `~/.agentrc/roles/` (using the `file:` path from `~/.agentrc/config.yaml` role definitions), load the listed skills, and read the project context file from `.agentrc/` if specified.
- If the user says "Boot <role>" and no agent matches, fall back to loading that single role from `~/.agentrc/roles/` by type directory (domain/, stack/, meta/).
- After context compaction, re-read the active role and project context files to restore conventions.
- Do not guess when uncertain. Stop and ask for clarification.
- Prefer focused, minimal output. No trailing summaries.
- Sub-agent output stays in the sub-agent. Main context gets one-line confirmations.

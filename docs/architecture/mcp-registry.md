# MCP Registry — Tiamat Service Inventory

This document catalogues all MCP (Model Context Protocol) servers in the Tiamat
ecosystem, their registration status in `~/.claude.json`, and the tools they
expose.

---

## Registered MCP Servers

### 1. Volon

| Field | Value |
|---|---|
| Binary | `volon mcp` |
| Transport | stdio |
| Status | **Registered** |

**Tools (21):**

| Tool | Description |
|---|---|
| `volon_health` | Adapter health/status |
| `volon_projects_list` | List all projects |
| `volon_tasks_list` | List tasks with filters (status, sprint, project) |
| `volon_task_get` | Get task by ID |
| `volon_task_create` | Create a task with full context fields |
| `volon_task_update` | Update task fields |
| `volon_task_delete` | Delete a task |
| `volon_task_transition` | Change task status |
| `volon_task_approve` | Approve a task for execution |
| `volon_sprints_list` | List sprints |
| `volon_sprint_get` | Get sprint by ID |
| `volon_sprint_create` | Create a sprint |
| `volon_sprint_update` | Update sprint fields |
| `volon_sprint_approve` | Approve a sprint |
| `volon_sprint_approve_all` | Approve all tasks in a sprint |
| `volon_backlog_list` | List backlog items |
| `volon_backlog_capture` | Create a backlog item |
| `volon_backlog_promote` | Promote backlog item to task |
| `volon_comment_add` | Add a comment to a task, backlog item, or sprint |
| `volon_comments_list` | List comments for a task, backlog item, or sprint |
| `volon_session_handoff` | Create session handoff for agent context transfer |

**Implementation:** `internal/mcpadapter/` — uses `mcp-go` library, serves over
stdio, backed by SQLite via `sqlstore.Store`.

---

### 2. Hadron

| Field | Value |
|---|---|
| Binary | `hadrond mcp` |
| Transport | stdio |
| Status | **Registered** |

Blueprint runner and scheduler. Exposes tools for blueprint management, pipeline
orchestration, run tracking, scheduling, and workspace management.

---

### 3. Vanta Conduit

| Field | Value |
|---|---|
| Binary | `contextd mcp` |
| Transport | stdio |
| Status | **Registered** |

Context broker. Exposes tools for namespace management, context read/write,
typed views, promotion workflows, and audit.

---

### 4. Cerberus

| Field | Value |
|---|---|
| Binary | `cerberus mcp` |
| Transport | stdio |
| Status | **Registered** |

Service manager. Exposes tools for starting, stopping, and monitoring Tiamat
services (health checks, logs, status).

---

## Unregistered Services

### 5. Carrier

| Field | Value |
|---|---|
| Repo | `~/Projects-apps/carrier/` |
| Language | Python 3.9+ |
| MCP Server | **None** |

Carrier is a content operations platform (ingest, generate, route). It has no
MCP server implementation. Design documents exist for future Volon/Vanta Conduit MCP
integration as an MCP *client* (calling into Volon and Vanta Conduit), but Carrier does
not expose its own operations as MCP tools.

**Recommendation:** An MCP server for Carrier could expose:
- `carrier_ingest` — trigger artifact ingestion
- `carrier_generate` — run content generation
- `carrier_sources_list` — list configured external sources
- `carrier_artifacts_list` / `carrier_artifact_get` — query artifacts

---

### 6. Nanite

| Field | Value |
|---|---|
| Repo | `~/Projects-apps/nanite/` |
| Language | Go |
| MCP Server | **None** |

Nanite is a task/note management app with a desktop GUI (Wails), HTTP API, CLI,
and Claude AI chat integration using Anthropic's native tool_use protocol. It
does not implement MCP. It references Volon/Vanta Conduit/Hadron as external MCP
dependencies but does not expose itself as a server.

**Recommendation:** An MCP server for Nanite could expose:
- `nanite_inbox_push` — push items to inbox
- `nanite_search` — search across vaults
- `nanite_item_get` — retrieve a specific item
- `nanite_vaults_list` — list available vaults

---

## Summary

| Service | MCP Server | Registered | Tools |
|---|---|---|---|
| Volon | Yes | Yes | 21 |
| Hadron | Yes | Yes | 50+ |
| Vanta Conduit | Yes | Yes | 15+ |
| Cerberus | Yes | Yes | 6 |
| Carrier | No | N/A | — |
| Nanite | No | N/A | — |

All four existing MCP servers (Volon, Hadron, Vanta Conduit, Cerberus) are registered
in `~/.claude.json`. Carrier and Nanite do not have MCP servers and would need
implementation before registration.

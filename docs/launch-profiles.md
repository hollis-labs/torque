# Launch Profiles

Torque tasks and agent profiles can **opt into** a shared
[go-agent-launch](https://github.com/hollis-labs/go-agent-launch) launch
profile. This is optional. The default — and the behavior when no launch
profile is referenced — is exactly the pure agent-profile path Torque has
always used; a launch profile only changes the *base* of the launch plan
Torque compiles.

Tether is **not** required. Launch profiles resolve standalone from a
local file path or an inline payload. No Tether daemon and no Tether
catalog is involved.

## When to use a launch profile

Use a launch profile when you want a Torque task to inherit a
provider / runtime / workspace-mode definition that is shared across
tools (for example, the same launch definition Tether or another
go-agent-launch consumer uses). Keep the pure agent-profile setup when
Torque is the only thing launching the agent — it is simpler and is the
default.

## Referencing a launch profile

A launch profile can be referenced from two places. Both are optional.

### From an agent profile (`profiles.yaml`)

Add a `launch_profile` field to an entry in `agent_profiles`:

```yaml
agent_profiles:
  codex-launch:
    executor: cli
    provider: codex
    model: gpt-5.4
    launch_profile: /etc/torque/catalog#backend-launch
```

Every task that resolves to this agent profile boots through the
referenced launch profile.

> **Profile naming.** Since CW-20260517-0011 (edge 3), `agent_profiles`
> keys are *provider-honest* — the provider is the first token of the
> name (`codex-long`, `opencode-default`, `default`). Names no longer
> imply a project or stack binding, because Torque agent profiles carry
> none. The bare `claude` provider was retired on 2026-05-16; use
> `claude-code` (streaming-stdio). Legacy names (`torque-backend`,
> `nanite-backend`, …) still resolve via `agent_profile_aliases` — see
> the header of `profiles.yaml`. This `agent_profiles` registry is the
> *only* one `torque_session_launch`'s `agent_profile` arg accepts; it
> does not accept Tether catalog boot-profile ids or catalog agent
> names. See `artifacts/CW-20260517-0011/profile-registry-design.md`.

### From a Boot request (per-task override)

`agent.Options` carries two optional fields:

- `LaunchProfile string` — same value forms as the profile field;
  **wins over** the agent profile's `launch_profile`.
- `LaunchProfileInline []byte` — a self-contained launch-profile YAML
  body, for standalone usage with no catalog file on disk; **wins over**
  both reference forms.

### Resolution precedence

Highest wins:

1. `Options.LaunchProfileInline` (inline payload)
2. `Options.LaunchProfile` (per-Boot reference)
3. `config.AgentProfile.LaunchProfile` (profiles.yaml reference)

When none are set, Torque uses the pure-inline launch-plan path — the
default.

## Catalog path vs inline payload

### Catalog path

A `launch_profile` / `LaunchProfile` value is a filesystem path with an
optional `#<launchID>` suffix:

- `"<path>"` — a single launch YAML file, **or** a catalog directory /
  `global.yaml` carrying exactly one launch entry. When a catalog has
  more than one launch entry you must disambiguate with a suffix.
- `"<path>#<launchID>"` — a catalog directory / `global.yaml`; the named
  launch entry is resolved.

A catalog directory follows the go-agent-launch layout (`global.yaml`
plus `projects/`, `agents/`, `providers/`, `launches/` subdirectories).
A single self-contained YAML file may also carry the inline
`projects:` / `agents:` / `providers:` / `launches:` lists directly.

> A bare single-launch file that only carries project/agent/provider
> **IDs** (with no sibling entries to resolve them against) cannot be
> resolved standalone — use a catalog directory or an inline payload
> that carries the referenced entries. Torque surfaces a clear error in
> this case rather than failing obscurely.

### Inline payload

`Options.LaunchProfileInline` takes a self-contained launch-profile YAML
— an inline `GlobalCatalog` carrying exactly one launch entry plus the
project / agent / provider entries it references:

```yaml
version: "0.1.0"
projects:
  - id: demo-project
    repo_root: /srv/demo-project
agents:
  - id: demo-agent
providers:
  - id: claude
    runtime_kind: subprocess
launches:
  - id: demo-launch
    project: demo-project
    agent: demo-agent
    provider: claude
    workspace:
      mode: persistent
```

## Precedence: what the launch profile contributes vs. what Torque always overlays

A launch profile contributes **base values only**. Torque always
overlays the runtime-critical fields it owns. A launch profile can
**never** break MCP loopback authorization, workspace ownership, or
boot-prompt planting.

**Contributed by the launch profile (base values):**

- Provider id — used when the Torque agent profile does not name a
  provider.
- Runtime kind — used only if Torque resolved no explicit kind (in
  practice Torque's per-provider matrix always returns a concrete kind,
  so Torque's runtime kind is authoritative).
- Workspace **mode** (the `shared` / `temp` / `fresh` / `persistent`
  token).
- Launch mode (`interactive` / `background` / `ephemeral`).
- MCP allowlist.
- Metadata labels and annotations.

**Always overlaid by Torque (the launch profile cannot override these):**

| Field | Why Torque owns it |
| --- | --- |
| Boot prompt + kickoff body | Torque composes the system prompt (role + agent-file + project context) and the kickoff markdown. The launch profile's `boot_profile` reference is dropped. |
| Workspace dirs (`Workdir`, `WorkspaceDir`, `TempPrefix`) | Workspace ownership stays with Torque's `WorkspaceLayout`. |
| MCP loopback URL | Torque constructs and authorizes the task-scoped MCP loopback. |
| Project id + root | Torque's project and workdir. |
| Agent id / name / role-file | Torque's agent profile identity and agent-file provenance. |
| Provider model override | Torque's `profile.model`. |
| Provider flags | Forced empty — Torque rebuilds argv per turn. |
| Native-file injection | Torque's agent-file projection. |

The **resolved runtime kind** follows Torque's normal chain:
`Options.RuntimeKindOverride` → `profile.runtime_kind` → the
per-provider default matrix. The launch profile's runtime does not
override it.

### Secrets

Secrets are never read from a launch profile. The base plan's
`injection.content` overlay and `overrides.env` are deliberately
discarded during the overlay, because launch profiles are persisted at
rest. Route provider credentials through Torque's existing env / api-key
helper paths, not through a launch profile.

## Migrating from a pure agent-profile setup

A pure agent-profile setup needs no launch profile and should stay as-is
unless you have a reason to share the launch definition. To migrate:

1. **Start from the agent profile.** Note its `provider`, `model`, and
   `runtime_kind`.

2. **Author a launch profile** carrying the same provider (and, if you
   use a catalog, the project / agent entries). Example single-file
   catalog:

   ```yaml
   version: "0.1.0"
   projects:
     - id: my-project
       repo_root: /srv/my-project
   agents:
     - id: my-agent
   providers:
     - id: claude
       runtime_kind: subprocess
   launches:
     - id: my-launch
       project: my-project
       agent: my-agent
       provider: claude
       workspace:
         mode: persistent
   ```

3. **Reference it** from the agent profile:

   ```yaml
   agent_profiles:
     codex-launch:
       executor: cli
       provider: codex
       model: gpt-5.4
       launch_profile: /srv/catalogs/my-catalog.yaml#my-launch
   ```

4. **Keep `provider` / `model` / `runtime_kind` on the agent profile.**
   They still apply — Torque's runtime-critical fields (provider model,
   resolved runtime kind) are overlaid on top of the launch profile.
   The launch profile does not replace them; it supplies the *base*.

5. **Verify.** Boot a task. Behavior — boot prompt, workspace dirs, MCP
   loopback — is unchanged; only the launch plan's base now comes from
   the shared profile. A misconfigured path or payload fails cleanly
   with an `agent: launch profile` error, not a panic.

To roll back, remove the `launch_profile` field. Torque returns to the
pure-inline path with no other change.

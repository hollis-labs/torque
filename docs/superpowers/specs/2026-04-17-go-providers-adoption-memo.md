---
task: CW-20260417-0080
status: proposal (research memo — implementation is a follow-up task)
owner: runner subsystem
date: 2026-04-17
supersedes: executor-api stub `Provider` interface at `plugins/executor-api/conversation.go:11`
---

# go-providers Adoption Memo

## Context

`github.com/chrispian/go-providers` (standalone repo at `~/Projects-apps/go-providers`)
is a unified LLM provider abstraction — the same package Nanite uses in
production. The core Torque design spec
(`2026-04-07-torque-design.md:71`) already identifies it as
**executor-plugin territory, not core**. This memo translates that
pre-approved direction into a concrete adoption plan for `executor-api`.

## What go-providers Gives Us

- One interface (`Provider`) covers eight HTTP backends (Anthropic,
  OpenAI, Gemini, Mistral, Azure OpenAI, OpenRouter, OpenZen, Ollama)
  plus eight CLI-bridge adapters (Claude Code, Codex, Gemini, Aider,
  Copilot, Junie, Kiro, Qwen) via PTY/subprocess.
- Thread-safe `Registry` with `Register/Get/Has/Names`
  (`provider/registry.go:1-62`).
- Optional capability interfaces: `Embedder`, `CacheableProvider`
  (Anthropic prompt caching), `APIKeySetter` (dynamic credential
  injection).
- Decorator pipeline (`EventReactionPipeline`) for retry, circuit
  breaker, cost/loop monitoring, scope guard.
- `context.Context` wired throughout; streaming via `<-chan StreamEvent`.
- Production call-site reference: Nanite
  (`~/Projects-apps/nanite/cmd/nanite/main.go:1092-1150`) — a ~60-line
  idiomatic setup we can crib from.

Maturity: **v0.2.0 beta**; README advertises the core `Provider`
interface as stable. Tests are pure-Go with `httptest` fakes — no
external service dependency. Dependency footprint is minimal
(`creack/pty`, `otel`).

## Torque's Current State (baseline)

| Area | File | Status |
|---|---|---|
| Executor core interface | `internal/runtime/executor/executor.go:1` | stable, pluggable |
| CLI executor plugin | `plugins/executor-cli/plugin.go:80` | production; spawns subprocess, parses `TORQUE_*` markers |
| API executor plugin | `plugins/executor-api/plugin.go:73` | wired into registry |
| **Stub** `Provider` interface | `plugins/executor-api/conversation.go:11` | `Complete(ctx, req) (*CompletionResponse, error)` — **zero implementations** |
| go-providers import | — | not yet present in `go.mod` |

The stub `Provider` in executor-api is a placeholder. No production
adopter depends on its shape. It's safe to replace outright.

## Proposed Adoption

### Scope

- **Adopt in `executor-api` only.** Replace the local stub `Provider`
  interface with a thin adapter around `go-providers.Provider`.
- **Do NOT fold `executor-cli` into go-providers' CLI bridges.** The two
  models are incompatible: `executor-cli` runs task-bound subprocesses
  and parses `TORQUE_*` markers from stdout; go-providers' PTY
  bridges model interactive chat sessions streaming `StreamEvent`s.
  Re-homing `executor-cli` would rewrite its semantics, not just its
  plumbing.
- Future work (separate proposal): a new `executor-pty` plugin that
  uses `go-providers` PTY bridges for interactive agent sessions. Out
  of scope here.

### Integration shape

```
executor-api plugin
├── APIExecutor (unchanged interface to core)
├── adapters/
│   └── providers.go        # thin shim: go-providers.Provider → executor-api calls
├── conversation.go         # refactored to stream StreamEvents → EventCallback
└── registry_bootstrap.go   # constructs provider.Registry at plugin init
```

- The plugin owns a `*provider.Registry`. Build it once at
  `APIExecutor.Start()` from credentials + config; pick per-job by
  `job.Provider` name.
- Credentials come from Torque's settings table (existing
  `settings_get/save` MCP tools) — we pass them into `APIKeySetter`
  at registry-construction time. go-providers does not manage
  secrets; we do.
- Non-Anthropic providers are wrapped in `EventReactionPipeline`
  (retry + circuit breaker) at registration time. Anthropic ships
  its own retry wiring — do not double-wrap.

### Streaming path

Swap `runConversation` from `provider.Complete(...)` to
`provider.StreamChat(...)`. Pipe `StreamEvent`s into the existing
`EventCallback`:

- `delta` → `executor.LogEvent(...)` (live stdout proxy)
- `usage` → `executor.TokenEvent(...)`
- `tool_use` → future Plan-4 tool routing (stub for now; `ToolDefinition`
  at `conversation.go:31` is already earmarked)
- `done` → terminal result; apply `ParseLine` to accumulated content
  for `TORQUE_*` marker extraction exactly as today
- `error` → `result.Status = "failed"` + `result.Reason`

This preserves the existing `TORQUE_*` parser model while unlocking
live event emission during long completions.

### Versioning + risk

- Pin to `go-providers v0.2.0` explicitly in `go.mod`.
- v0.2.0 is beta; changelog is sparse. Mitigation: keep the shim layer
  narrow (see interface sketch below) so a future breaking upgrade in
  go-providers is a single-file adjustment.
- We do not use the Embedder surface in executor-api today; ignore it
  unless/until we wire embeddings.

## Proposed Torque-side interface sketch

Keep Torque's own thin interface inside executor-api. Do not
re-export `go-providers.Provider` to core. This isolates the upstream
dependency to one file and lets us tune the shape to our callback
model without fighting go-providers' chat-oriented API.

```go
// plugins/executor-api/conversation.go

// Provider is executor-api's narrow contract. It is satisfied by a
// thin adapter around *go-providers*.Provider; see adapters/providers.go.
type Provider interface {
    // Stream runs one turn and emits events as they arrive. The
    // returned final result carries accumulated content for
    // TORQUE_* marker extraction by the existing parser.
    Stream(ctx context.Context, req CompletionRequest, emit EventSink) (*CompletionResponse, error)

    // Name returns the provider's registered name (anthropic, openai, ollama, ...).
    Name() string
}

// EventSink is the narrow event channel executor-api cares about.
// Implemented by a closure that adapts to executor.EventCallback.
type EventSink interface {
    OnDelta(text string)
    OnTokenUsage(prompt, completion int, cost float64)
    OnToolUse(call ToolCall) // stub surface for Plan 4
    OnError(err error)
}
```

Adapter sketch:

```go
// plugins/executor-api/adapters/providers.go

type goProvidersAdapter struct {
    name string
    p    provider.Provider // from github.com/chrispian/go-providers
}

func (a *goProvidersAdapter) Name() string { return a.name }

func (a *goProvidersAdapter) Stream(
    ctx context.Context,
    req executorapi.CompletionRequest,
    emit executorapi.EventSink,
) (*executorapi.CompletionResponse, error) {
    chatReq := translateRequest(req) // CompletionRequest → provider.ChatRequest
    events, err := a.p.StreamChat(ctx, chatReq)
    if err != nil {
        return nil, err
    }
    var buf strings.Builder
    var usage executor.TokenUsage
    for ev := range events {
        switch ev.Type {
        case provider.EventDelta:
            buf.WriteString(ev.Content)
            emit.OnDelta(ev.Content)
        case provider.EventUsage:
            usage = translateUsage(ev.Usage)
            emit.OnTokenUsage(usage.PromptTokens, usage.CompletionTokens, usage.Cost)
        case provider.EventToolUse:
            emit.OnToolUse(translateToolCall(ev.ToolUse))
        case provider.EventError:
            emit.OnError(ev.Err)
            return nil, ev.Err
        case provider.EventDone:
            return &executorapi.CompletionResponse{
                Content: buf.String(),
                Tokens:  usage,
                StopReason: ev.StopReason,
            }, nil
        }
    }
    return nil, io.ErrUnexpectedEOF
}
```

Registry bootstrap:

```go
// plugins/executor-api/registry_bootstrap.go

func buildRegistry(cfg Config) (*provider.Registry, error) {
    reg := provider.NewRegistry()

    if k := cfg.AnthropicKey; k != "" {
        p := provider.NewAnthropic()
        if s, ok := p.(provider.APIKeySetter); ok { s.SetAPIKey(k) }
        reg.Register("anthropic", p) // anthropic ships its own retry
    }
    if k := cfg.OpenAIKey; k != "" {
        p := provider.NewOpenAI()
        if s, ok := p.(provider.APIKeySetter); ok { s.SetAPIKey(k) }
        reg.Register("openai", wrapResilience(p)) // retry + circuit breaker
    }
    if cfg.OllamaEnabled {
        reg.Register("ollama", wrapResilience(provider.NewOllama()))
    }
    // ... others as needed
    return reg, nil
}
```

## Adoption checklist (for the follow-up implementation task)

1. Add `github.com/chrispian/go-providers v0.2.0` to `go.mod`.
2. Replace `plugins/executor-api/conversation.go` stub `Provider`
   interface with the narrow `Provider`/`EventSink` shape above.
3. Add `plugins/executor-api/adapters/providers.go` with
   `goProvidersAdapter` translating request/event shapes.
4. Add `plugins/executor-api/registry_bootstrap.go` that reads
   credentials from Torque settings and constructs
   `*provider.Registry`.
5. Refactor `runConversation` to call `Stream(...)` instead of
   `Complete(...)`. Preserve `executor.ParseLine` marker extraction
   on the accumulated final content.
6. Wire plugin bootstrap in `plugins/executor-api/plugin.go` to build
   the registry at `APIExecutor.Start()`.
7. Tests: fake `provider.Provider` impl with canned
   `<-chan StreamEvent` — verify emission ordering, error paths,
   marker extraction invariance vs. today's behavior.
8. Document provider registration + credential flow in
   `docs/architecture/`.

## Open questions for follow-up task

1. **Settings schema**: do we keep API keys as plain rows in the
   settings table, or do we introduce a typed `secrets` surface? (Same
   question Nanite already solved — crib their answer.)
2. **Per-task provider overrides**: does `ExecutionJob` need a
   `provider_overrides` field for A/B testing, or do we keep one
   registered provider per job.executor?
3. **PTY executor as a follow-on**: worth a separate memo once we
   have a concrete use case. Out of scope here.

## Summary

go-providers is the right abstraction, already pre-approved by the
design spec, and its Nanite call sites prove ergonomic adoption. Scope
the adoption to `executor-api` and preserve Torque's own narrow
`Provider` interface as a shim — one file changes if upstream breaks.
`executor-cli` stays as-is.

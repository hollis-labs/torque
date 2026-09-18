package mcpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hollis-labs/go-mcp/sanitize"
	gomcp "github.com/hollis-labs/go-mcp/server"
	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprop "go.opentelemetry.io/otel/propagation"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/service"
)

// Adapter wires the service layer to an MCP server.
//
// loopbackTaskID is non-empty only for adapters built via NewLoopback. It
// pins every interpretive tool call (summary, blocked, review, comment_add,
// artifact_create, subtodo_*) to a specific task, so the spawned agent can
// only act on its own task. Regular adapters (built via New) leave it empty
// and do not register the loopback tool subset.
type Adapter struct {
	svc            *service.Service
	sched          *scheduler.Scheduler
	server         *gomcp.Server
	loopbackTaskID string
	// sessions wires the long-lived agent session manager (CW-20260503-0014).
	// Nil disables torque_session_* tools — they reply with a domain error
	// rather than panicking. Mirrors the sched=nil contract.
	sessions *agent.Manager
	// broker wires the typed envelope dispatcher (CW-20260503-0013, S1.3).
	// Nil disables torque_broker_* tools the same way.
	broker *broker.Broker
	// pollRegistry wires the opt-in inbox-poll registry (CW-20260518-0042)
	// shared with the steering bridge. Nil disables the opt-in side of
	// torque_inbox_poll — the tool then reports polling as unavailable on
	// this MCP host, mirroring the nil-broker contract.
	pollRegistry *steering.PollRegistry
	// reminderRegistry wires the turn-boundary reminder registry
	// (CW-20260519-0065) shared with the steering bridge. When set,
	// torque_steering_dismiss can ack injected envelopes so the runtime
	// stops re-surfacing them at turn boundaries. Nil disables the
	// dismiss tool's effect — it then reports the dismiss as unavailable
	// (no envelopes to ack against on this host).
	reminderRegistry *steering.ReminderRegistry
	// Logger receives go-mcp-sanitize warn telemetry when the middleware
	// auto-cleans malformed agent tool-call XML in free-text params (see
	// CW-20260509-0033, mirrors vanta-conduit's Pattern A install). Nil
	// falls back to slog.Default(); production wiring routes this to stderr
	// in cmd/torque/mcp.go because stdio MCP reserves stdout for the
	// JSON-RPC protocol stream.
	Logger *slog.Logger
	// sanitizeOnce guards the one-time install of go-mcp's sanitize
	// middleware on first Server() call. See Server()'s doc comment.
	sanitizeOnce sync.Once
}

// New creates an Adapter, registers all tools, and returns it. sched may be
// nil — scheduler tools will respond with a not-available error in that case,
// mirroring httpserver.New's nil-sched → 503 contract. The stdio mcp
// subcommand runs in a separate process from serve and passes nil; tests and
// any future in-process wiring can pass a live *scheduler.Scheduler.
func New(svc *service.Service, sched *scheduler.Scheduler) *Adapter {
	s := gomcp.NewServer("Torque", "0.1.0")
	a := &Adapter{svc: svc, sched: sched, server: s}
	a.registerCoreTools()
	a.registerOptInTools()
	return a
}

// Server returns the underlying go-mcp Server. Installs go-mcp's sanitize
// middleware (the former go-mcp-sanitize module, folded into go-mcp) at the
// protocol level on first call, replacing the old per-tool
// mcpsanitize.Middleware(logger)(h) wrap that addTool used to apply
// individually. Deferred to first Server() call (rather than New()) so a
// WithLogger call made after New() — the documented contract, e.g.
// cmd/torque/mcp.go's mcpadapter.New(...).WithSessions(...).WithLogger(...)
// chain — is honored: stdio MCP reserves stdout for the JSON-RPC stream, so
// routing sanitize's warn telemetry to the wrong logger (slog.Default(),
// typically stdout) instead of the caller's stderr logger would corrupt the
// protocol stream, not just misroute a log line.
func (a *Adapter) Server() *gomcp.Server {
	a.sanitizeOnce.Do(func() {
		logger := a.Logger
		if logger == nil {
			logger = slog.Default()
		}
		a.server.SDKServer().AddReceivingMiddleware(sanitize.Middleware(logger))
	})
	return a.server
}

// WithSessions attaches the unified agent session manager so the
// torque_session_* tools surface real data. Must be called before any
// MCP requests are served (not goroutine-safe with respect to live calls).
//
// Renamed from WithSessionMgr (CW-20260508-0001) when sessionmgr was folded
// into the agent package.
func (a *Adapter) WithSessions(mgr *agent.Manager) *Adapter {
	a.sessions = mgr
	return a
}

// WithBroker attaches the typed envelope broker so the torque_broker_*
// tools surface real data. Same pre-flight contract as WithSessionMgr.
func (a *Adapter) WithBroker(b *broker.Broker) *Adapter {
	a.broker = b
	return a
}

// WithPollRegistry attaches the opt-in inbox-poll registry (CW-20260518-0042)
// so torque_inbox_poll can record an agent's polling opt-in and the
// steering bridge can observe it. Same pre-flight contract as WithBroker
// (must be set before any MCP requests are served). Optional — leaving it
// nil makes torque_inbox_poll report polling unavailable on this host.
func (a *Adapter) WithPollRegistry(reg *steering.PollRegistry) *Adapter {
	a.pollRegistry = reg
	return a
}

// WithReminderRegistry attaches the turn-boundary reminder registry
// (CW-20260519-0065) so torque_steering_dismiss can mark injected
// envelopes ack'd and stop the runtime's re-surfacing pass. Same
// pre-flight contract as WithPollRegistry. Optional — leaving it nil
// makes torque_steering_dismiss report the dismiss as unavailable.
func (a *Adapter) WithReminderRegistry(reg *steering.ReminderRegistry) *Adapter {
	a.reminderRegistry = reg
	return a
}

// WithLogger attaches a slog.Logger for go-mcp-sanitize warn telemetry. Same
// pre-flight contract as WithSessions / WithBroker (must be set before any
// MCP requests are served; not goroutine-safe with respect to live calls).
// Optional — leaving Logger nil falls back to slog.Default() in addTool.
func (a *Adapter) WithLogger(l *slog.Logger) *Adapter {
	a.Logger = l
	return a
}

// addTool wraps every MCP tool handler with middleware and registers it
// against the underlying go-mcp server:
//
//  1. The OTel tracing wrapper: extracts MCP-encoded trace context from the
//     call args (_traceparent / _tracestate set by Hollis-app callers via
//     propagation.InjectMCP) so inbound calls continue the same trace, and
//     opens a torque.mcp.call span for the handler's duration with
//     hollis.tool.name attached.
//  2. The unknown-argument guard (CW-20260907-0060): an argument the tool's
//     own schema does not declare is rejected rather than silently dropped.
//     See unknownArgRejector for why a blanket guard is the right scope.
//
// go-mcp's own sanitize middleware (the former go-mcp-sanitize module) is
// installed once, at the protocol level, on first Server() call — see
// Server()'s doc comment — rather than wrapped per tool here.
//
// All registerXxx helpers (and NewLoopback's registerLoopbackTools) must call
// a.addTool(...) instead of a.server.RegisterTool(...) directly so the trace
// and unknown-arg protection stay uniform across the 100+ tool surface, and
// so every tool gets its annotation hints from toolAnnotations (see
// tool_annotations.go) rather than a hand-copied literal per call site.
func (a *Adapter) addTool(spec toolSpec, h gomcp.ToolHandler) {
	toolName := spec.Name
	hints, ok := toolAnnotations[toolName]
	if !ok {
		panic("mcpadapter: no annotation hints for tool " + toolName + " — add an entry to toolAnnotations in tool_annotations.go")
	}
	rejectUnknown := unknownArgRejector(spec)
	traced := func(ctx context.Context, req map[string]any) (result any, err error) {
		// Extract MCP-encoded trace context (_traceparent / _tracestate) INTO
		// the inbound ctx — do NOT use propagation.ExtractMCP here: that helper
		// starts from context.Background() and discards the MCP-server-provided
		// ctx (cancellation, deadlines, transport-scoped values). Using
		// otel.GetTextMapPropagator().Extract(ctx, ...) attaches the remote
		// span context onto the existing ctx, preserving everything else.
		if _, hasTP := req["_traceparent"]; hasTP {
			carrier := otelprop.MapCarrier{}
			if tp, ok := req["_traceparent"].(string); ok {
				carrier.Set("traceparent", tp)
			}
			if ts, ok := req["_tracestate"].(string); ok {
				carrier.Set("tracestate", ts)
			}
			ctx = otel.GetTextMapPropagator().Extract(ctx, carrier)
		}
		ctx, span := feotel.StartSpan(ctx, "torque.mcp.call")
		span.SetAttributes(
			attribute.String("hollis.app", "torque"),
			attribute.String("hollis.tool.name", toolName),
		)
		// Named returns + defer keep span lifecycle panic-safe: if the
		// inner handler panics, the deferred End still fires, so spans
		// never leak. The result/err inspection runs against the actual
		// return values (or zero values on panic).
		defer func() {
			if err != nil {
				// CallToolResult.IsError conflates validation rejects and
				// infra faults at the mcpadapter layer (errResult/toolError
				// both produce a non-nil err here). Per the OTel guide's
				// policy-not-infra guidance, surface as an attribute rather
				// than span.RecordError so an operator can grep failed tool
				// calls without those drowning out real faults.
				span.SetAttributes(attribute.Bool("torque.mcp.result_error", true))
			}
			span.End()
		}()
		if rejectErr := rejectUnknown(req); rejectErr != nil {
			return nil, rejectErr
		}
		if toolName == "torque_task_list" || toolName == "torque_task_facets" {
			if rejectErr := rejectMalformedTaskListTags(req); rejectErr != nil {
				return nil, rejectErr
			}
		}
		return h(ctx, req)
	}
	a.server.RegisterTool(gomcp.Tool{
		Name:            toolName,
		Description:     spec.Description,
		InputSchema:     gomcp.ObjectSchema(spec.Properties, spec.Required...),
		Handler:         traced,
		ReadOnlyHint:    hints.ReadOnly,
		DestructiveHint: hints.Destructive,
		IdempotentHint:  hints.Idempotent,
		OpenWorldHint:   false,
	})
}

// argsMetaPrefix marks transport-level arguments that ride alongside a tool's
// own inputs and are deliberately absent from its schema — currently the
// _traceparent/_tracestate pair extracted above. They are exempt from the
// unknown-argument check.
const argsMetaPrefix = "_"

// unknownArgRejector builds the per-tool guard against silently-dropped
// arguments (CW-20260907-0060).
//
// Neither the MCP protocol layer nor go-mcp validates an incoming argument
// against the tool's schema, and every Torque handler reads its inputs
// presence-based (reqStr/reqInt and friends). An argument no handler reads is
// therefore simply not read: no rejection, and `ok: true` either way. A caller
// passing `body` where the schema says `content`, or `status` to a tool that
// cannot write it, was told the call succeeded while the value went nowhere.
// That is worse than a plain missing-validation bug, because the response is
// confident and well-formed — the reporting session in CW-20260903-0044 lost
// four records this way and reported them as landed.
//
// The set is captured once at registration, so the per-call cost is a map
// lookup per supplied argument.
//
// Safety of a blanket guard: an audit across the global, loopback and
// all-features adapters found ZERO handlers reading an argument their tool
// does not declare, so no legitimate call is newly rejected.
func unknownArgRejector(spec toolSpec) func(map[string]any) error {
	declared := make(map[string]bool, len(spec.Properties))
	names := make([]string, 0, len(spec.Properties))
	for k := range spec.Properties {
		declared[k] = true
		names = append(names, k)
	}
	sort.Strings(names)
	accepted := "this tool accepts no arguments"
	if len(names) > 0 {
		accepted = "accepted arguments: " + strings.Join(names, ", ")
	}

	return func(req map[string]any) error {
		var unknown []string
		for k := range req {
			if declared[k] || strings.HasPrefix(k, argsMetaPrefix) {
				continue
			}
			unknown = append(unknown, k)
		}
		if len(unknown) == 0 {
			return nil
		}
		sort.Strings(unknown)
		field := ""
		if len(unknown) == 1 {
			field = unknown[0]
		}
		return argError(ErrCodeArgInvalid, fmt.Sprintf(
			"unknown argument(s) for %s: %s — the call was rejected rather than applied with those values dropped; %s",
			spec.Name, strings.Join(unknown, ", "), accepted,
		), field)
	}
}

func (a *Adapter) registerCoreTools() {
	a.addTool(newTool("torque_health",
		withDescription(`Liveness probe for the Torque MCP server.
Use before any other tool when you need to confirm the service is reachable and discover which opt-in feature flags (sprints, projects, epics, collections) are enabled.
Response shape: data = {status, message, enabled_features[]}.
Example: {}`),
	), a.handleHealth)
	a.registerTaskTools()
	a.registerTaskBulkTools()
	a.registerTagTools()
	a.registerRunTools()
	a.registerArtifactTools()
	a.registerCommentTools()
	a.registerSettingsTools()
	a.registerSchedulerTools()
	a.registerCheckpointTools()
	a.registerTemplateTools()
	a.registerSubtodoTools()
	a.registerPlanTools()
	a.registerIssueTools()
	a.registerModelTools()
	a.registerSessionTools()
	a.registerBrokerTools()
}

// registerOptInTools checks feature flags and registers tools for enabled layers.
// Called at startup; features must be enabled before the adapter is created.
func (a *Adapter) registerOptInTools() {
	if a.svc.Feature.IsEnabled("sprints") {
		a.registerSprintTools()
	}
	if a.svc.Feature.IsEnabled("projects") {
		a.registerProjectTools()
	}
	if a.svc.Feature.IsEnabled("epics") {
		a.registerEpicTools()
	}
	if a.svc.Feature.IsEnabled("collections") {
		a.registerCollectionTools()
	}
}

func (a *Adapter) handleHealth(ctx context.Context, req map[string]any) (any, error) {
	features := a.svc.Feature.ListEnabled()
	return okResult(map[string]interface{}{
		"status":           "running",
		"message":          "Torque is running",
		"enabled_features": features,
	})
}

// ---- helpers ----------------------------------------------------------------

// reqHasArg reports whether the caller supplied the named argument. Use to
// distinguish "omitted" from "explicit zero / empty string / false" when a
// handler treats the two cases differently (e.g. partial-update PATCH calls).
func reqHasArg(req map[string]any, key string) bool {
	_, ok := req[key]
	return ok
}

// reqStr extracts a string argument. Non-string values return "" to match
// mcp-go's CallToolRequest.GetString behavior — callers that need
// presence-detection should use reqHasArg.
func reqStr(req map[string]any, key string) string {
	args := req
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// reqStrSlice extracts a list-of-strings argument tolerant of both the legacy
// JSON-encoded-string shape and the post-sanitize []any shape.
//
// Background: torque's MCP schema declares list-shaped params
// (notably `tags` and `depends_on`) as a JSON-encoded string so LLM-backed
// clients that emit the array as a literal can round-trip through mcp-go's
// schema-string boundary. Per CW-20260509-0033, the go-mcp-sanitize middleware
// (Pattern 4) auto-recovers `tags` from a JSON-encoded string into a real
// []any of strings before the handler runs. This helper keeps both shapes
// working uniformly so the handler call sites don't have to branch.
//
// Return contract:
//   - present, []any of strings    → ([]string, nil)   (post-sanitize shape)
//   - present, []string            → ([]string, nil)
//   - present, non-empty JSON str  → unmarshal to []string; err if bad JSON
//   - present, empty string        → (nil, nil) — caller treats as absent
//   - absent                       → (nil, nil)
//   - present but unsupported type → (nil, error)
//
// Use in place of `if raw := reqStr(req, "tags"); raw != "" { json.Unmarshal... }`
// at every list-of-strings argument site.
func reqStrSlice(req map[string]any, key string) ([]string, error) {
	args := req
	v, ok := args[key]
	if !ok {
		return nil, nil
	}
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []string:
		out := make([]string, 0, len(t))
		out = append(out, t...)
		return out, nil
	case []any:
		out := make([]string, 0, len(t))
		for i, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("element %d is not a string (got %T)", i, e)
			}
			out = append(out, s)
		}
		return out, nil
	case string:
		if t == "" {
			return nil, nil
		}
		var out []string
		if err := json.Unmarshal([]byte(t), &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported type %T", v)
	}
}

// reqInt extracts an integer parameter from an MCP request.
//
// Per CW-20260418-0011 the schema declares numeric params as strings so
// LLM-backed clients that emit `"limit": "50"` (string-encoded numeric)
// aren't rejected at the mcp-go schema boundary. This helper therefore
// accepts float64, int, int64, AND string inputs — callers passing 50
// (number) or "50" (string) must both resolve to 50. int64 handling
// (CW-20260418-0019) covers mcp-go paths that materialize schema-number
// types as int64.
//
// Silent-zero policy: a malformed numeric string (e.g. "abc") returns 0,
// the same value as a missing key. This is intentional. Every numeric
// call site in the adapter already treats 0 as "not set" (see handleTaskList
// where limit==0 rebinds to default 50, and the presence-gated
// `if _, ok := args[key]; ok` pattern in handleTaskUpdate that only applies
// budgets when the key is present). Degrading garbage input to "as if unset"
// matches that convention and avoids punching a new `(int, error)` signature
// through every tool handler. If a future caller needs to distinguish
// "explicit zero" from "malformed" we would add a separate helper rather
// than change this one.
//
// Sentinel values "-1" (unlimited) and "0" (explicit zero) must round-trip
// exactly — see adapter_test.go for the matrix.
func reqInt(req map[string]any, key string) int {
	args := req
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		case string:
			if n == "" {
				return 0
			}
			if i, err := strconv.ParseInt(n, 10, 64); err == nil {
				return int(i)
			}
			// Accept numeric-looking strings with a fractional part too
			// (e.g. "50.0") by falling back to float parse.
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				return int(f)
			}
			return 0
		}
	}
	return 0
}

// reqFloat extracts a float64 parameter from an MCP request. Mirrors reqInt:
// accepts float64, int, int64, and string inputs; returns 0 on missing key or
// unparseable string. See reqInt for the silent-zero rationale.
func reqFloat(req map[string]any, key string) float64 {
	args := req
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case string:
			if n == "" {
				return 0
			}
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				return f
			}
			return 0
		}
	}
	return 0
}

// reqBool extracts a bool argument. Coerces from string ("true"/"false"/"1"/"0"),
// int, int64, and float64 to match mcp-go's CallToolRequest.GetBool behavior.
// Without this coercion, a caller passing "manual": "true" (string) silently
// drops to false even though the MCP server-side tool declares a Boolean
// field — task_update silent-drop regression tracked in CW-20260418-0019.
func reqBool(req map[string]any, key string) bool {
	args := req
	if v, ok := args[key]; ok {
		switch b := v.(type) {
		case bool:
			return b
		case string:
			if parsed, err := strconv.ParseBool(b); err == nil {
				return parsed
			}
		case int:
			return b != 0
		case int64:
			return b != 0
		case float64:
			return b != 0
		}
	}
	return false
}

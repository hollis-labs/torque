package mcpadapter

import (
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// ErrorCode is the structured taxonomy for MCP tool errors. Agents inspect
// `error.code` to distinguish validation from not-found from conflict, and
// can self-correct deterministically instead of parsing free-text messages.
// The code set is intentionally small and stable; we expand only on demand.
type ErrorCode string

const (
	// ErrCodeArgInvalid — caller's args are malformed or fail validation
	// (bad JSON, missing required field, wrong enum, etc). Retry with
	// corrected args. `field` is set when a single field is at fault.
	ErrCodeArgInvalid ErrorCode = "arg_invalid"
	// ErrCodeNotFound — the referenced entity does not exist (task_id,
	// correlation_id, artifact id, etc). Non-retriable; caller must
	// either create the entity or correct the id.
	ErrCodeNotFound ErrorCode = "not_found"
	// ErrCodeConflict — semantic conflict on a state-dependent op: invalid
	// lifecycle transition, responding to an already-terminal checkpoint,
	// deleting a template with referencing tasks, etc. Retry after state
	// changes.
	ErrCodeConflict ErrorCode = "conflict"
	// ErrCodeDomain — domain-layer rule violation that isn't a simple
	// field validation (e.g. feature not enabled, budget exhausted). Caller
	// must change the surrounding environment, not just the args.
	ErrCodeDomain ErrorCode = "domain"
	// ErrCodePermission — reserved for future permission/auth paths. Phase C
	// does not map any current errors onto this code; documented so the
	// taxonomy is stable when auth lands.
	ErrCodePermission ErrorCode = "permission"
	// ErrCodeInternal — uncategorized server-side failure (DB, marshal,
	// unexpected state). Full context is logged server-side; caller sees
	// a short generic message to avoid leaking stack traces / implementation
	// internals.
	ErrCodeInternal ErrorCode = "internal"
)

// Response is the canonical top-level envelope for every MCP tool call.
// Phase C rule: top-level is ALWAYS `{ok, data, error}`. `data` is the
// payload directly — list handlers set `data = {items, meta}`, singleton
// gets set `data = {<record fields>}`, scalar ops set `data = {<minimal>}`.
// No `data.item` wrapper, no `data.items.items` double-nest.
//
// On error: `ok=false`, `data=nil`, `error` populated.
type Response struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data,omitempty"`
	Error *ErrorInfo `json:"error,omitempty"`
}

// ErrorInfo is the structured error payload. `field` is set when a single
// input field is at fault (usually paired with arg_invalid). `details` is
// freeform structured context — currently unused, reserved so error paths
// can evolve without another breaking change.
type ErrorInfo struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Field   string    `json:"field,omitempty"`
	Details any       `json:"details,omitempty"`
}

// okResult wraps a handler's payload in `{ok: true, data: <payload>, error: nil}`
// and emits the JSON as a mcp TextContent. All successful handler paths MUST
// go through this helper so the envelope shape is consistent.
//
// Pass `data` as whatever the handler wants as the top-level content of `data`:
//   - list handler: the full `{items, meta}` literal (a struct or a map).
//   - singleton get: the record directly.
//   - scalar op: a minimal map like {"deleted": true, "id": "T-1"}.
//
// Marshaling is indented so the wire output is human-readable — matches the
// pre-Phase-C jsonResult behavior so callers diffing raw JSON don't re-format.
func okResult(data any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(Response{OK: true, Data: data}, "", "  ")
	if err != nil {
		// Serialization of a successful response failed — degrade to an
		// internal error envelope. This path should be unreachable for any
		// data we actually pass, but we log and return a safe shape rather
		// than crash the handler chain.
		log.Printf("mcpadapter: okResult marshal failed: %v", err)
		return errResult(ErrCodeInternal, "response serialization failed", "")
	}
	return mcp.NewToolResultText(string(b)), nil
}

// errResult emits the dual-surface error result the ticket's sharp-edge
// section mandates:
//
//  1. At the MCP framing level: IsError=true so mcp-go clients that surface
//     only the `isError` flag / the `NewToolResultError`-shaped content still
//     see the failure.
//  2. At the payload level: the content text is the full
//     {ok:false, data:null, error:{...}} envelope so clients that parse the
//     text can read the structured taxonomy (code, field, message).
//
// We build the CallToolResult manually instead of chaining NewToolResultError
// + a separate TextContent because mcp-go only supports one surface per
// convenience helper. One CallToolResult with IsError=true and a JSON text
// body satisfies both consumers from a single response.
//
// Internal-error sanitization: callers pass the already-sanitized message.
// mapServiceError strips any stack-traceable context from `internal` mappings
// before this function sees it. The full diagnostic context is logged there,
// not here.
func errResult(code ErrorCode, message, field string) (*mcp.CallToolResult, error) {
	env := Response{
		OK:    false,
		Error: &ErrorInfo{Code: code, Message: message, Field: field},
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		// Fall back to a bare NewToolResultError so we at least surface the
		// problem at the framing level. This branch is effectively unreachable
		// (ErrorInfo has no non-marshalable fields) but covers future schema
		// drift.
		log.Printf("mcpadapter: errResult marshal failed: %v", err)
		return mcp.NewToolResultError(message), nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: string(b)},
		},
		IsError: true,
	}, nil
}

// mapServiceError maps a service/sqlstore error into the (ErrorCode, user-safe
// message, field) triple errResult consumes. Callers that already classified
// their error (bad JSON on unmarshal, missing required arg, etc.) should call
// errResult directly with the explicit code — this helper is for errors that
// bubble up from the service layer where the type determines the code.
//
// Rules (applied in order; first match wins):
//   - sqlstore "not found" sentinels → not_found
//   - sqlstore.ErrInvalidCursor (PRIM-001/DEC-001 cursor pagination) →
//     arg_invalid (field=cursor)
//   - *service.NotFoundError → not_found
//   - *service.ValidationError → arg_invalid (with field)
//   - *service.RepoPathError → arg_invalid (field=repo_path)
//   - *service.TransitionError → conflict
//   - *service.ConflictError → conflict (also catches ErrTemplateReferenced
//     via the string-match tier below since the sqlstore error isn't a typed
//     sentinel that service-layer wraps)
//   - *service.FeatureDisabledError → domain
//   - Plain `fmt.Errorf("sprint %s not found", ...)` (sprint/project/epic
//     stores return untyped messages) → string-match tier → not_found
//   - sqlstore.ErrTemplateReferenced → conflict
//   - Default → internal, full context logged server-side, short safe
//     message returned to the caller.
//
// This intentionally doesn't rewrite the service-layer error types. If a
// service error is ambiguous or maps poorly, fix it in a follow-up ticket
// rather than piling special cases here.
func mapServiceError(err error) (ErrorCode, string, string) {
	if err == nil {
		return ErrCodeInternal, "unknown error", ""
	}

	// Typed sqlstore sentinels — all wrapped with fmt.Errorf(...: %w, ...).
	if errors.Is(err, sqlstore.ErrTaskNotFound) ||
		errors.Is(err, sqlstore.ErrTemplateNotFound) ||
		errors.Is(err, sqlstore.ErrCheckpointNotFound) ||
		errors.Is(err, sqlstore.ErrArtifactNotFound) ||
		errors.Is(err, sqlstore.ErrTagNotFound) {
		return ErrCodeNotFound, err.Error(), ""
	}
	if errors.Is(err, sqlstore.ErrTemplateReferenced) {
		return ErrCodeConflict, err.Error(), ""
	}
	// PRIM-001/DEC-001: a cursor whose sort value doesn't type-convert for
	// its sort column (malformed/tampered token) is a caller input fault,
	// not a server fault.
	if errors.Is(err, sqlstore.ErrInvalidCursor) {
		return ErrCodeArgInvalid, err.Error(), "cursor"
	}

	// Typed service errors.
	var nfe *service.NotFoundError
	if errors.As(err, &nfe) {
		return ErrCodeNotFound, err.Error(), ""
	}
	var ve *service.ValidationError
	if errors.As(err, &ve) {
		return ErrCodeArgInvalid, err.Error(), ve.Field
	}
	// repo_path drift (CW-20260517-0011 edge 7): a stale/missing project
	// repo_path is a caller-correctable input fault — map to arg_invalid
	// with field=repo_path so agents can self-correct deterministically
	// (fix the path) rather than parse the free-text message.
	var rpe *service.RepoPathError
	if errors.As(err, &rpe) {
		return ErrCodeArgInvalid, err.Error(), "repo_path"
	}
	var te *service.TransitionError
	if errors.As(err, &te) {
		return ErrCodeConflict, err.Error(), ""
	}
	var ce *service.ConflictError
	if errors.As(err, &ce) {
		return ErrCodeConflict, err.Error(), ""
	}
	var fde *service.FeatureDisabledError
	if errors.As(err, &fde) {
		return ErrCodeDomain, err.Error(), ""
	}

	// String-match tier for untyped "not found" errors from sprint/project/epic
	// stores (they use fmt.Errorf without a sentinel). Constrained to the
	// specific "<entity> <id> not found" shape to avoid false positives.
	msg := err.Error()
	if strings.Contains(msg, " not found") {
		return ErrCodeNotFound, msg, ""
	}

	// Default: internal. Log full diagnostic context server-side; return a
	// short user-safe message so stack traces / DB internals don't leak to
	// the MCP caller.
	log.Printf("mcpadapter: unmapped service error (→ internal): %v", err)
	return ErrCodeInternal, "internal server error", ""
}

// errFromService is a convenience wrapper for handlers that want the common
// "service call failed, map and surface" pattern in one line.
func errFromService(err error) (*mcp.CallToolResult, error) {
	code, msg, field := mapServiceError(err)
	return errResult(code, msg, field)
}

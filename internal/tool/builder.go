package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// CallFunc is the function signature for a tool's execution logic.
type CallFunc func(ctx context.Context, input map[string]any, execCtx ExecutionContext) (*ToolResult, error)

// ToolOption is a functional option for NewTool.
type ToolOption func(*toolImpl)

// toolImpl is the concrete implementation built by NewTool.
type toolImpl struct {
	name        string
	description string
	category    string
	source      string
	tags        []string
	schema      json.RawMessage
	timeout     time.Duration
	permissions []PermissionRule

	callFn           CallFunc
	validateFn       func(map[string]any) error
	concurrencySafeFn func(map[string]any) bool
	concurrencySafe  bool
	readOnlyFn       func(map[string]any) bool
	readOnly         bool
	destructiveFn    func(map[string]any) bool
	destructive      bool
}

// NewTool creates a Tool using functional options.
func NewTool(name, description string, opts ...ToolOption) Tool {
	t := &toolImpl{
		name:        name,
		description: description,
		source:      SourceBuiltin,
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// --- Tool interface implementation ---

func (t *toolImpl) Name() string        { return t.name }
func (t *toolImpl) Description() string { return t.description }
func (t *toolImpl) Category() string    { return t.category }
func (t *toolImpl) Source() string      { return t.source }

func (t *toolImpl) Tags() []string {
	if t.tags == nil {
		return []string{}
	}
	cp := make([]string, len(t.tags))
	copy(cp, t.tags)
	return cp
}

func (t *toolImpl) InputSchema() json.RawMessage {
	if t.schema == nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return t.schema
}

func (t *toolImpl) Timeout() time.Duration { return t.timeout }

func (t *toolImpl) Call(ctx context.Context, input map[string]any, execCtx ExecutionContext) (*ToolResult, error) {
	if t.callFn == nil {
		return nil, fmt.Errorf("tool %q has no call function", t.name)
	}
	return t.callFn(ctx, input, execCtx)
}

func (t *toolImpl) ValidateInput(input map[string]any) error {
	if t.validateFn == nil {
		return nil
	}
	return t.validateFn(input)
}

func (t *toolImpl) IsConcurrencySafe(input map[string]any) bool {
	if t.concurrencySafeFn != nil {
		return t.concurrencySafeFn(input)
	}
	return t.concurrencySafe
}

func (t *toolImpl) IsReadOnly(input map[string]any) bool {
	if t.readOnlyFn != nil {
		return t.readOnlyFn(input)
	}
	return t.readOnly
}

func (t *toolImpl) IsDestructive(input map[string]any) bool {
	if t.destructiveFn != nil {
		return t.destructiveFn(input)
	}
	return t.destructive
}

func (t *toolImpl) DefaultPermissions() []PermissionRule {
	if t.permissions == nil {
		return []PermissionRule{}
	}
	cp := make([]PermissionRule, len(t.permissions))
	copy(cp, t.permissions)
	return cp
}

// --- Options ---

// WithCategory sets the tool category.
func WithCategory(category string) ToolOption {
	return func(t *toolImpl) { t.category = category }
}

// WithSchema sets the raw JSON input schema.
func WithSchema(schema json.RawMessage) ToolOption {
	return func(t *toolImpl) { t.schema = schema }
}

// WithSchemaMap sets the input schema from a map (marshalled to JSON).
func WithSchemaMap(m map[string]any) ToolOption {
	return func(t *toolImpl) {
		data, err := json.Marshal(m)
		if err != nil {
			// Silently ignore; caller should validate.
			return
		}
		t.schema = data
	}
}

// WithSource sets the tool source.
func WithSource(source string) ToolOption {
	return func(t *toolImpl) { t.source = source }
}

// WithTags sets the tool tags.
func WithTags(tags ...string) ToolOption {
	return func(t *toolImpl) { t.tags = tags }
}

// WithTimeout sets the execution timeout. The runner is responsible for
// enforcing this value; toolImpl exposes it via Timeout().
func WithTimeout(d time.Duration) ToolOption {
	return func(t *toolImpl) { t.timeout = d }
}

// WithCallFunc sets the execution function.
func WithCallFunc(fn CallFunc) ToolOption {
	return func(t *toolImpl) { t.callFn = fn }
}

// WithConcurrencySafe sets a static concurrency-safe value.
func WithConcurrencySafe(safe bool) ToolOption {
	return func(t *toolImpl) { t.concurrencySafe = safe }
}

// WithConcurrencySafeFunc sets a dynamic concurrency-safe predicate.
func WithConcurrencySafeFunc(fn func(map[string]any) bool) ToolOption {
	return func(t *toolImpl) { t.concurrencySafeFn = fn }
}

// WithReadOnly sets a static read-only value.
func WithReadOnly(ro bool) ToolOption {
	return func(t *toolImpl) { t.readOnly = ro }
}

// WithReadOnlyFunc sets a dynamic read-only predicate.
func WithReadOnlyFunc(fn func(map[string]any) bool) ToolOption {
	return func(t *toolImpl) { t.readOnlyFn = fn }
}

// WithDestructive sets a static destructive value.
func WithDestructive(d bool) ToolOption {
	return func(t *toolImpl) { t.destructive = d }
}

// WithDestructiveFunc sets a dynamic destructive predicate.
func WithDestructiveFunc(fn func(map[string]any) bool) ToolOption {
	return func(t *toolImpl) { t.destructiveFn = fn }
}

// WithPermissions sets the default permission rules.
func WithPermissions(rules ...PermissionRule) ToolOption {
	return func(t *toolImpl) { t.permissions = rules }
}

// WithValidateFunc sets the input validation function.
func WithValidateFunc(fn func(map[string]any) error) ToolOption {
	return func(t *toolImpl) { t.validateFn = fn }
}

// Ensure toolImpl satisfies Tool at compile time.
var _ Tool = (*toolImpl)(nil)

// errNoCallFunc is used in tests via errors.Is — keep it unexported.
var errNoCallFunc = errors.New("no call function")

# Task API Expansion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface the full canonical task model through the HTTP API and TypeScript types — 12 missing fields, named typed request structs, strict enum/numeric/dependency validation, sentinel values for nullable numeric fields.

**Architecture:** Extend `TaskCreateInput` with the 9 missing service-level fields. Introduce a shared `validateTaskWrites` helper that both `Create` and `Update` call. Replace the inline/map-based HTTP handler parsing with named `TaskCreateRequest` / `TaskUpdateRequest` types. Add JSON-blob parse helpers for the read side and a `nullJSONString` write helper. Narrow TypeScript lifecycle fields to enum union types and add `Deliverable`, `DeliverableType`, `UNLIMITED`.

**Tech Stack:** Go 1.26, `modernc.org/sqlite`, `stretchr/testify`, React 19, TypeScript strict, `encoding/json`.

**Reference:** `docs/superpowers/specs/2026-04-09-task-api-expansion-design.md` — read this first if any task is unclear.

---

## File Structure

### New files

| Path | Responsibility |
|---|---|
| `internal/service/task_validation.go` | `Deliverable` type, `Unlimited` constant, valid-value maps, `taskWriteFields` internal struct, `validateTaskWrites` helper, `extractCreateFields` / `extractUpdateFields` builders |
| `internal/service/task_validation_test.go` | Table-driven tests for the validation helper covering each rule |

### Modified files

| Path | Changes |
|---|---|
| `internal/service/task.go` | Expand `TaskCreateInput` with 9 new fields; wire each new field into `Create`'s store-record build; call `validateTaskWrites` from both `Create` and `Update`; apply lifecycle enum defaults via `orDefault` |
| `internal/service/task_test.go` | Add round-trip create/update tests for all new fields including sentinel values |
| `internal/httpserver/tasks.go` | Add `TaskCreateRequest` and `TaskUpdateRequest` named types; add 4 read-side parse helpers (`parseStringArray`, `parseStringMap`, `parseFreeMap`, `parseDeliverables`); add `nullJSONString` write helper; rewrite `createTask` and `updateTask` handlers to use the new types; expand `taskJSON` with all 12 new fields; reject `status` on update |
| `internal/httpserver/server_test.go` | Add full-field create test, per-field update tests, enum/numeric/dependency error tests, status rejection test, sentinel round-trip test |
| `apps/gui/src/lib/types.ts` | Add `OnDone`, `OnFail`, `OnReview`, `OnDoneMerge`, `DeliverableType`, `Deliverable`, `UNLIMITED`; expand `Task` with 12 new fields; narrow existing 4 lifecycle fields from `string` to enum unions; add JSDoc on the 3 nullable numeric fields documenting sentinel meaning |
| `apps/gui/src/lib/api.ts` | Extend type imports to include the new types (no signature changes needed — existing `Partial<Task>` accommodates the new fields) |

---

## Task 1: Service layer — `task_validation.go` foundation

**Files:**
- Create: `internal/service/task_validation.go`
- Create: `internal/service/task_validation_test.go`

- [ ] **Step 1: Write the failing test for the `Unlimited` constant and `Deliverable` type existence**

Create `internal/service/task_validation_test.go`:

```go
package service_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
)

func TestUnlimitedConstantExists(t *testing.T) {
	assert.Equal(t, int64(-1), service.Unlimited)
}

func TestDeliverableTypeExists(t *testing.T) {
	d := service.Deliverable{
		Type:        "diff",
		Required:    true,
		Description: "Git diff or patch content",
	}
	assert.Equal(t, "diff", d.Type)
	assert.True(t, d.Required)
	assert.Equal(t, "Git diff or patch content", d.Description)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/service/... -run 'TestUnlimitedConstantExists|TestDeliverableTypeExists' -v`
Expected: compile errors — `service.Unlimited` and `service.Deliverable` don't exist yet.

- [ ] **Step 3: Create `task_validation.go` with the constant, type, and valid-value maps**

Create `internal/service/task_validation.go`:

```go
package service

// Unlimited is the sentinel value meaning "no cap" for the three nullable
// numeric task fields: CostBudget, MaxDurationMs, TokenBudget. Use this
// instead of a magic -1 at call sites.
const Unlimited = int64(-1)

// Deliverable declares an expected artifact a task must produce before it
// can be marked complete.
type Deliverable struct {
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

// validDeliverableTypes is the set of built-in artifact types that
// Deliverables.Type may take. The "custom" value is the plugin-extensibility
// escape hatch for plugin-defined types.
var validDeliverableTypes = map[string]bool{
	"diff":         true,
	"test-results": true,
	"screenshot":   true,
	"pr-link":      true,
	"branch":       true,
	"commit":       true,
	"log":          true,
	"finding":      true,
	"report":       true,
	"note":         true,
	"metrics":      true,
	"custom":       true,
}

// Lifecycle enum value sets per the canonical task model spec.
var validOnDone = map[string]bool{
	"close":  true,
	"review": true,
	"notify": true,
}

var validOnFail = map[string]bool{
	"retry":    true,
	"block":    true,
	"escalate": true,
	"notify":   true,
}

var validOnReview = map[string]bool{
	"pause":        true,
	"notify":       true,
	"auto-approve": true,
}

var validOnDoneMerge = map[string]bool{
	"none":         true,
	"auto":         true,
	"pr":           true,
	"auto-resolve": true,
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/service/... -run 'TestUnlimitedConstantExists|TestDeliverableTypeExists' -v`
Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/service/task_validation.go internal/service/task_validation_test.go
git commit -m "feat(service): add Unlimited constant, Deliverable type, valid-value maps"
```

---

## Task 2: Service layer — `taskWriteFields` + `validateTaskWrites` helper

**Files:**
- Modify: `internal/service/task_validation.go`
- Modify: `internal/service/task_validation_test.go`

- [ ] **Step 1: Write the failing tests for `validateTaskWrites`**

Append to `internal/service/task_validation_test.go`:

```go
import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)
```

(Merge the imports with the existing import block — only add `database/sql`, `sqlstore`, `migrations`, `require`, and the sqlite blank import to whatever's already there.)

Then append the test functions:

```go
func setupTaskValidationTest(t *testing.T) *service.Service {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return service.New(store)
}

func TestValidateTaskWritesAcceptsEmptyEnums(t *testing.T) {
	svc := setupTaskValidationTest(t)
	// Empty strings on enums means "use default" — should NOT error
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title: "Test",
	})
	assert.NoError(t, err)
}

func TestValidateTaskWritesAcceptsValidOnDone(t *testing.T) {
	svc := setupTaskValidationTest(t)
	for _, v := range []string{"close", "review", "notify"} {
		_, err := svc.Task.Create(service.TaskCreateInput{
			Title:  "Test " + v,
			OnDone: v,
		})
		assert.NoError(t, err, "value %q should be accepted", v)
	}
}

func TestValidateTaskWritesRejectsBadOnDone(t *testing.T) {
	svc := setupTaskValidationTest(t)
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:  "Test",
		OnDone: "purge",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "on_done")
}

func TestValidateTaskWritesAcceptsValidOnFail(t *testing.T) {
	svc := setupTaskValidationTest(t)
	for _, v := range []string{"retry", "block", "escalate", "notify"} {
		_, err := svc.Task.Create(service.TaskCreateInput{
			Title:  "Test " + v,
			OnFail: v,
		})
		assert.NoError(t, err, "value %q should be accepted", v)
	}
}

func TestValidateTaskWritesRejectsBadOnFail(t *testing.T) {
	svc := setupTaskValidationTest(t)
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:  "Test",
		OnFail: "explode",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "on_fail")
}

func TestValidateTaskWritesAcceptsValidOnReview(t *testing.T) {
	svc := setupTaskValidationTest(t)
	for _, v := range []string{"pause", "notify", "auto-approve"} {
		_, err := svc.Task.Create(service.TaskCreateInput{
			Title:    "Test " + v,
			OnReview: v,
		})
		assert.NoError(t, err, "value %q should be accepted", v)
	}
}

func TestValidateTaskWritesRejectsBadOnReview(t *testing.T) {
	svc := setupTaskValidationTest(t)
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:    "Test",
		OnReview: "ignore",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "on_review")
}

func TestValidateTaskWritesAcceptsValidOnDoneMerge(t *testing.T) {
	svc := setupTaskValidationTest(t)
	for _, v := range []string{"none", "auto", "pr", "auto-resolve"} {
		_, err := svc.Task.Create(service.TaskCreateInput{
			Title:       "Test " + v,
			OnDoneMerge: v,
		})
		assert.NoError(t, err, "value %q should be accepted", v)
	}
}

func TestValidateTaskWritesRejectsBadOnDoneMerge(t *testing.T) {
	svc := setupTaskValidationTest(t)
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Test",
		OnDoneMerge: "force-push",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "on_done_merge")
}

func TestValidateTaskWritesCostBudgetSentinels(t *testing.T) {
	svc := setupTaskValidationTest(t)

	zero := 0.0
	unlimited := -1.0
	specific := 25.5

	// Valid: 0, -1, positive
	for i, v := range []*float64{&zero, &unlimited, &specific} {
		_, err := svc.Task.Create(service.TaskCreateInput{
			Title:      "Test cost " + string(rune('A'+i)),
			CostBudget: v,
		})
		assert.NoError(t, err, "value %v should be accepted", *v)
	}

	// Invalid: < -1
	bad := -2.0
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:      "Test bad cost",
		CostBudget: &bad,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost_budget")
}

func TestValidateTaskWritesMaxDurationMsSentinels(t *testing.T) {
	svc := setupTaskValidationTest(t)

	unlimited := int64(-1)
	specific := int64(30000)

	// Valid: -1, positive
	for i, v := range []*int64{&unlimited, &specific} {
		_, err := svc.Task.Create(service.TaskCreateInput{
			Title:         "Test dur " + string(rune('A'+i)),
			MaxDurationMs: v,
		})
		assert.NoError(t, err, "value %v should be accepted", *v)
	}

	// Invalid: 0 (zero duration is meaningless)
	zero := int64(0)
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:         "Test zero dur",
		MaxDurationMs: &zero,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_duration_ms")

	// Invalid: < -1
	bad := int64(-2)
	_, err = svc.Task.Create(service.TaskCreateInput{
		Title:         "Test bad dur",
		MaxDurationMs: &bad,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_duration_ms")
}

func TestValidateTaskWritesTokenBudgetSentinels(t *testing.T) {
	svc := setupTaskValidationTest(t)

	unlimited := int64(-1)
	specific := int64(100000)

	// Valid: -1, positive
	for i, v := range []*int64{&unlimited, &specific} {
		_, err := svc.Task.Create(service.TaskCreateInput{
			Title:       "Test tokens " + string(rune('A'+i)),
			TokenBudget: v,
		})
		assert.NoError(t, err, "value %v should be accepted", *v)
	}

	// Invalid: 0
	zero := int64(0)
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Test zero tokens",
		TokenBudget: &zero,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token_budget")

	// Invalid: < -1
	bad := int64(-5)
	_, err = svc.Task.Create(service.TaskCreateInput{
		Title:       "Test bad tokens",
		TokenBudget: &bad,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token_budget")
}

func TestValidateTaskWritesMaxRetriesNegative(t *testing.T) {
	svc := setupTaskValidationTest(t)

	bad := -1
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:      "Test",
		MaxRetries: &bad,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_retries")
}

func TestValidateTaskWritesDeliverablesAcceptValidTypes(t *testing.T) {
	svc := setupTaskValidationTest(t)
	allTypes := []string{"diff", "test-results", "screenshot", "pr-link", "branch", "commit", "log", "finding", "report", "note", "metrics", "custom"}
	dels := make([]service.Deliverable, len(allTypes))
	for i, t := range allTypes {
		dels[i] = service.Deliverable{Type: t, Required: false}
	}
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:        "Test",
		Deliverables: dels,
	})
	assert.NoError(t, err)
}

func TestValidateTaskWritesDeliverablesRejectsBadType(t *testing.T) {
	svc := setupTaskValidationTest(t)
	_, err := svc.Task.Create(service.TaskCreateInput{
		Title: "Test",
		Deliverables: []service.Deliverable{
			{Type: "diff", Required: true},
			{Type: "screencast", Required: false}, // not in the valid set
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deliverables")
	assert.Contains(t, err.Error(), "screencast")
}

func TestValidateTaskWritesDependsOnExistenceHappyPath(t *testing.T) {
	svc := setupTaskValidationTest(t)

	// Create the dependency first
	dep, err := svc.Task.Create(service.TaskCreateInput{Title: "Dependency"})
	require.NoError(t, err)

	// Now create a task that depends on it
	_, err = svc.Task.Create(service.TaskCreateInput{
		Title:     "Dependent",
		DependsOn: []string{dep.ID},
	})
	assert.NoError(t, err)
}

func TestValidateTaskWritesDependsOnRejectsMissing(t *testing.T) {
	svc := setupTaskValidationTest(t)

	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:     "Test",
		DependsOn: []string{"CW-99999999-9999"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "depends_on")
	assert.Contains(t, err.Error(), "CW-99999999-9999")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/service/... -run TestValidateTaskWrites -v 2>&1 | tail -30`
Expected: compile errors or test failures because `validateTaskWrites` and the new `TaskCreateInput` fields don't exist yet.

- [ ] **Step 3: Add the validation helper to `task_validation.go`**

Append to `internal/service/task_validation.go`:

```go
// taskWriteFields is the internal projection of writable task fields shared
// between the Create and Update validation paths. Pointer fields use nil to
// mean "field not present in this write" (so Update can skip rules for
// fields the caller didn't touch).
type taskWriteFields struct {
	OnDone        string
	OnFail        string
	OnReview      string
	OnDoneMerge   string
	CostBudget    *float64
	MaxRetries    *int
	MaxDurationMs *int64
	TokenBudget   *int64
	Deliverables  []Deliverable
	DependsOn     []string
}

// validateTaskWrites enforces write-time invariants shared between Create and
// Update. Returns nil on success or a *ValidationError on the first failed
// rule. The store parameter is used to validate DependsOn task ID existence.
func (s *TaskService) validateTaskWrites(fields taskWriteFields) error {
	// Lifecycle enum validation. Empty string means "use default" — accepted.
	if fields.OnDone != "" && !validOnDone[fields.OnDone] {
		return &ValidationError{
			Field:   "on_done",
			Message: "invalid on_done: got '" + fields.OnDone + "', expected one of: close, review, notify",
		}
	}
	if fields.OnFail != "" && !validOnFail[fields.OnFail] {
		return &ValidationError{
			Field:   "on_fail",
			Message: "invalid on_fail: got '" + fields.OnFail + "', expected one of: retry, block, escalate, notify",
		}
	}
	if fields.OnReview != "" && !validOnReview[fields.OnReview] {
		return &ValidationError{
			Field:   "on_review",
			Message: "invalid on_review: got '" + fields.OnReview + "', expected one of: pause, notify, auto-approve",
		}
	}
	if fields.OnDoneMerge != "" && !validOnDoneMerge[fields.OnDoneMerge] {
		return &ValidationError{
			Field:   "on_done_merge",
			Message: "invalid on_done_merge: got '" + fields.OnDoneMerge + "', expected one of: none, auto, pr, auto-resolve",
		}
	}

	// Numeric bound validation with sentinel value support.
	if fields.CostBudget != nil {
		v := *fields.CostBudget
		// Allowed: -1 (unlimited), 0, or any positive value.
		// Rejected: < -1 or any negative other than -1.
		if v < -1 || (v > -1 && v < 0) {
			return &ValidationError{
				Field:   "cost_budget",
				Message: "cost_budget must be -1 (unlimited), 0 (none), or a positive value",
			}
		}
	}
	if fields.MaxRetries != nil && *fields.MaxRetries < 0 {
		return &ValidationError{
			Field:   "max_retries",
			Message: "max_retries must be a non-negative integer",
		}
	}
	if fields.MaxDurationMs != nil {
		v := *fields.MaxDurationMs
		// Allowed: -1 (unlimited) or any positive value. Reject 0 and other negatives.
		if v != Unlimited && v <= 0 {
			return &ValidationError{
				Field:   "max_duration_ms",
				Message: "max_duration_ms must be -1 (unlimited) or a positive value in milliseconds",
			}
		}
	}
	if fields.TokenBudget != nil {
		v := *fields.TokenBudget
		if v != Unlimited && v <= 0 {
			return &ValidationError{
				Field:   "token_budget",
				Message: "token_budget must be -1 (unlimited) or a positive value",
			}
		}
	}

	// Deliverable type validation.
	for i, d := range fields.Deliverables {
		if !validDeliverableTypes[d.Type] {
			return &ValidationError{
				Field:   "deliverables",
				Message: "deliverables[" + intToString(i) + "].type '" + d.Type + "' is not a known artifact type",
			}
		}
	}

	// DependsOn existence validation. N+1 lookups; acceptable for typical
	// dependency counts. Empty list is fine (no deps).
	for _, depID := range fields.DependsOn {
		if _, err := s.store.GetTask(depID); err != nil {
			return &ValidationError{
				Field:   "depends_on",
				Message: "task " + depID + " not found",
			}
		}
	}

	return nil
}

// intToString is a tiny stdlib-only int-to-string helper to avoid pulling
// in strconv just for an error message position index.
func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	negative := i < 0
	if negative {
		i = -i
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
```

(Note: `intToString` is included so this file has zero new package imports beyond what `service` already uses. This avoids a chicken-and-egg with `strconv` in a pure-validation file. If `strconv` is already imported elsewhere in the package, replace `intToString(i)` with `strconv.Itoa(i)` at use sites and delete the helper.)

- [ ] **Step 4: Run tests to verify they fail with new errors**

Run: `go test ./internal/service/... -run TestValidateTaskWrites -v 2>&1 | tail -60`
Expected: tests now compile but most still fail because `Create` doesn't yet call `validateTaskWrites`. The exact failure depends on Task 3 — for now, you may see "expected error, got nil" for the rejection tests. That's correct: the helper is defined but not yet wired in.

- [ ] **Step 5: Commit**

```bash
git add internal/service/task_validation.go internal/service/task_validation_test.go
git commit -m "feat(service): add validateTaskWrites helper with enum/numeric/deliverable rules"
```

---

## Task 3: Service layer — expand `TaskCreateInput` and wire validation into `Create`

**Files:**
- Modify: `internal/service/task.go`

- [ ] **Step 1: Add 9 new fields to `TaskCreateInput`**

In `internal/service/task.go`, find the `TaskCreateInput` struct definition (around line 19). It currently has 22 fields. Add the 9 new fields immediately before the closing `}`:

```go
type TaskCreateInput struct {
	// ... existing 22 fields unchanged ...

	// New in Project 2
	Permissions     map[string]any
	Environment     map[string]string
	MaxDurationMs   *int64
	TokenBudget     *int64
	EscalationChain []string
	QualityGates    []string
	Deliverables    []Deliverable
	BlockedReason   string
	Metadata        map[string]any
}
```

- [ ] **Step 2: Wire `validateTaskWrites` into `TaskService.Create`**

Find `func (s *TaskService) Create(input TaskCreateInput)` (around line 53). After the existing title check but before the sprint/project/epic association validation, add:

```go
// Validate write-time invariants (enums, numeric bounds, deliverables, depends_on).
if err := s.validateTaskWrites(extractCreateFields(input)); err != nil {
	return nil, err
}
```

The full ordering should be:

1. Title required check (existing)
2. Priority default (existing)
3. **NEW: validateTaskWrites call**
4. Sprint association validation (existing)
5. Project association validation (existing)
6. Epic association validation (existing)
7. NextTaskID + record build + persist (existing)

- [ ] **Step 3: Add `extractCreateFields` helper**

Append to the bottom of `internal/service/task.go` (or to `task_validation.go` if you prefer to keep it adjacent to the helper that consumes it):

```go
// extractCreateFields projects a TaskCreateInput into the validation helper's
// internal field set.
func extractCreateFields(input TaskCreateInput) taskWriteFields {
	return taskWriteFields{
		OnDone:        input.OnDone,
		OnFail:        input.OnFail,
		OnReview:      input.OnReview,
		OnDoneMerge:   input.OnDoneMerge,
		CostBudget:    input.CostBudget,
		MaxRetries:    input.MaxRetries,
		MaxDurationMs: input.MaxDurationMs,
		TokenBudget:   input.TokenBudget,
		Deliverables:  input.Deliverables,
		DependsOn:     input.DependsOn,
	}
}
```

- [ ] **Step 4: Apply lifecycle enum defaults in the record build**

Inside `Create`, find the `rec := &sqlstore.TaskRecord{...}` block (around line 106). The current code has empty strings for `OnDone`, `OnFail`, `OnReview`, `OnDoneMerge`. Replace with explicit defaults using `orDefault`:

```go
rec := &sqlstore.TaskRecord{
	ID:                id,
	Title:             input.Title,
	Description:       input.Description,
	Priority:          priority,
	Manual:            input.Manual,
	Executor:          orDefault(input.Executor, "cli"),
	AgentProfile:      input.AgentProfile,
	WorkingDir:        input.WorkingDir,
	SystemPrompt:      input.SystemPrompt,
	MaxRetries:        orDefaultInt(input.MaxRetries, 3),
	OnDone:            orDefault(input.OnDone, "review"),
	OnFail:            orDefault(input.OnFail, "retry"),
	OnReview:          orDefault(input.OnReview, "pause"),
	OnDoneMerge:       orDefault(input.OnDoneMerge, "none"),
	DeliverablePreset: input.DeliverablePreset,
	BlockedReason:     input.BlockedReason,
}
```

(`BlockedReason` is added as a new field assignment in the literal.)

- [ ] **Step 5: Wire the 9 new fields into the record build**

Below the `rec := &sqlstore.TaskRecord{...}` block, the existing code already handles `Tools`, `Files`, `DependsOn`, `CostBudget`, `SprintID`, `ProjectID`, `EpicID`. Add new conditional blocks for the 9 new fields:

```go
if input.Permissions != nil && len(input.Permissions) > 0 {
	rec.Permissions = sql.NullString{String: marshalJSON(input.Permissions), Valid: true}
}
if input.Environment != nil && len(input.Environment) > 0 {
	rec.Environment = sql.NullString{String: marshalJSON(input.Environment), Valid: true}
}
if input.MaxDurationMs != nil {
	rec.MaxDurationMs = sql.NullInt64{Int64: *input.MaxDurationMs, Valid: true}
}
if input.TokenBudget != nil {
	rec.TokenBudget = sql.NullInt64{Int64: *input.TokenBudget, Valid: true}
}
if len(input.EscalationChain) > 0 {
	rec.EscalationChain = sql.NullString{String: marshalJSON(input.EscalationChain), Valid: true}
}
if len(input.QualityGates) > 0 {
	rec.QualityGates = sql.NullString{String: marshalJSON(input.QualityGates), Valid: true}
}
if len(input.Deliverables) > 0 {
	rec.Deliverables = sql.NullString{String: marshalJSON(input.Deliverables), Valid: true}
}
if input.Metadata != nil && len(input.Metadata) > 0 {
	rec.Metadata = sql.NullString{String: marshalJSON(input.Metadata), Valid: true}
}
```

(Place these adjacent to the existing `if len(input.Tools) > 0` blocks for consistency.)

- [ ] **Step 6: Run the validation tests to verify they now pass**

Run: `go test ./internal/service/... -run TestValidateTaskWrites -v 2>&1 | tail -60`
Expected: all 16 validation tests PASS (the rejection tests now hit the validator that's wired into Create; the happy-path tests succeed because the fields are accepted).

- [ ] **Step 7: Run the broader service test suite to confirm no regressions**

Run: `go test ./internal/service/... -v 2>&1 | tail -40`
Expected: all existing tests still pass plus the 16 new validation tests.

- [ ] **Step 8: Commit**

```bash
git add internal/service/task.go internal/service/task_validation.go
git commit -m "feat(service): expand TaskCreateInput with 9 fields, wire validation into Create"
```

---

## Task 4: Service layer — wire `validateTaskWrites` into `Update`

**Files:**
- Modify: `internal/service/task.go`

- [ ] **Step 1: Write the failing test for update validation**

Append to `internal/service/task_validation_test.go`:

```go
func TestUpdateValidatesEnums(t *testing.T) {
	svc := setupTaskValidationTest(t)

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "Test"})
	require.NoError(t, err)

	// Bad on_done on update
	bad := "purge"
	err = svc.Task.Update(task.ID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{OnDone: &bad},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "on_done")
}

func TestUpdateValidatesNumericSentinels(t *testing.T) {
	svc := setupTaskValidationTest(t)

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "Test"})
	require.NoError(t, err)

	// Bad max_duration_ms (zero) on update
	zero := sql.NullInt64{Int64: 0, Valid: true}
	err = svc.Task.Update(task.ID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{MaxDurationMs: &zero},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_duration_ms")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/service/... -run 'TestUpdateValidates' -v`
Expected: tests fail because `Update` doesn't yet call the validation helper.

- [ ] **Step 3: Add `extractUpdateFields` helper**

Append to the bottom of `internal/service/task.go` (next to `extractCreateFields`):

```go
// extractUpdateFields projects a TaskUpdateInput into the validation helper's
// internal field set. Only fields where the update pointer is non-nil get
// passed through — fields the caller didn't touch are left zero so the
// validator skips them.
func extractUpdateFields(input TaskUpdateInput) taskWriteFields {
	f := taskWriteFields{
		Deliverables: nil, // populated below if present
		DependsOn:    nil,
	}
	if input.OnDone != nil {
		f.OnDone = *input.OnDone
	}
	if input.OnFail != nil {
		f.OnFail = *input.OnFail
	}
	if input.OnReview != nil {
		f.OnReview = *input.OnReview
	}
	if input.OnDoneMerge != nil {
		f.OnDoneMerge = *input.OnDoneMerge
	}
	if input.CostBudget != nil && input.CostBudget.Valid {
		v := input.CostBudget.Float64
		f.CostBudget = &v
	}
	if input.MaxRetries != nil {
		f.MaxRetries = input.MaxRetries
	}
	if input.MaxDurationMs != nil && input.MaxDurationMs.Valid {
		v := input.MaxDurationMs.Int64
		f.MaxDurationMs = &v
	}
	if input.TokenBudget != nil && input.TokenBudget.Valid {
		v := input.TokenBudget.Int64
		f.TokenBudget = &v
	}
	if input.Deliverables != nil && input.Deliverables.Valid && input.Deliverables.String != "" {
		var dels []Deliverable
		if err := unmarshalJSON([]byte(input.Deliverables.String), &dels); err == nil {
			f.Deliverables = dels
		}
	}
	if input.DependsOn != nil && input.DependsOn.Valid && input.DependsOn.String != "" {
		var deps []string
		if err := unmarshalJSON([]byte(input.DependsOn.String), &deps); err == nil {
			f.DependsOn = deps
		}
	}
	return f
}
```

- [ ] **Step 4: Add `unmarshalJSON` helper to `internal/service/helpers.go`**

The existing `helpers.go` has `marshalJSON` but no inverse. Add:

```go
// unmarshalJSON is a thin wrapper around json.Unmarshal for symmetry with
// marshalJSON. Returns the underlying error so callers can decide whether
// to surface or silently fall back.
func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
```

You'll need to add `"encoding/json"` to the imports if it's not already there.

- [ ] **Step 5: Wire validation into `Update`**

Find `func (s *TaskService) Update(id string, input TaskUpdateInput)` in `internal/service/task.go`. Replace its current body:

```go
func (s *TaskService) Update(id string, input TaskUpdateInput) error {
	if err := s.store.UpdateTask(id, input.TaskUpdate); err != nil {
		return err
	}
	if input.Tags != nil {
		slugs, err := s.tags.ResolveNames(*input.Tags)
		if err != nil {
			return err
		}
		return s.store.SetTaskTags(id, slugs)
	}
	return nil
}
```

with:

```go
func (s *TaskService) Update(id string, input TaskUpdateInput) error {
	if err := s.validateTaskWrites(extractUpdateFields(input)); err != nil {
		return err
	}
	if err := s.store.UpdateTask(id, input.TaskUpdate); err != nil {
		return err
	}
	if input.Tags != nil {
		slugs, err := s.tags.ResolveNames(*input.Tags)
		if err != nil {
			return err
		}
		return s.store.SetTaskTags(id, slugs)
	}
	return nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/service/... -run 'TestUpdateValidates' -v`
Expected: 2 PASS.

- [ ] **Step 7: Run the full service suite**

Run: `go test ./internal/service/... -v 2>&1 | tail -30`
Expected: all tests green.

- [ ] **Step 8: Commit**

```bash
git add internal/service/task.go internal/service/helpers.go internal/service/task_validation_test.go
git commit -m "feat(service): wire validateTaskWrites into Update with extractUpdateFields"
```

---

## Task 5: Service layer — round-trip create test for all new fields

**Files:**
- Modify: `internal/service/task_test.go`

- [ ] **Step 1: Write a comprehensive round-trip test**

Append to `internal/service/task_test.go`:

```go
func TestCreateWithAllFieldsRoundTrip(t *testing.T) {
	svc := setupTaskValidationTest(t)

	// First create a task that the test task can depend on
	dep, err := svc.Task.Create(service.TaskCreateInput{Title: "Dependency"})
	require.NoError(t, err)

	costBudget := 50.0
	maxRetries := 5
	maxDurationMs := int64(60000)
	tokenBudget := int64(100000)

	input := service.TaskCreateInput{
		Title:             "Full field task",
		Description:       "All the fields",
		Priority:          1,
		Tags:              []string{"bug", "ui"},
		Manual:            true,
		Executor:          "api",
		AgentProfile:      "claude-opus",
		WorkingDir:        "/repos/test",
		Tools:             []string{"bash", "edit", "read"},
		Permissions:       map[string]any{"network": true, "filesystem": "read-only"},
		Environment:       map[string]string{"NODE_ENV": "test", "DEBUG": "1"},
		SystemPrompt:      "You are a test agent",
		Files:             []string{"src/main.go", "src/utils.go"},
		CostBudget:        &costBudget,
		MaxRetries:        &maxRetries,
		MaxDurationMs:     &maxDurationMs,
		TokenBudget:       &tokenBudget,
		OnDone:            "close",
		OnFail:            "block",
		OnReview:          "auto-approve",
		OnDoneMerge:       "auto",
		EscalationChain:   []string{"senior-agent", "human-reviewer"},
		QualityGates:      []string{"go test ./...", "go vet ./..."},
		Deliverables: []service.Deliverable{
			{Type: "diff", Required: true, Description: "Code changes"},
			{Type: "test-results", Required: true},
		},
		DeliverablePreset: "backend-fix",
		DependsOn:         []string{dep.ID},
		BlockedReason:     "",
		Metadata:          map[string]any{"source": "test", "priority_score": 0.95},
	}

	created, err := svc.Task.Create(input)
	require.NoError(t, err)
	require.NotNil(t, created)

	// Roundtrip via Get
	got, err := svc.Task.Get(created.ID)
	require.NoError(t, err)

	// Spot-check the new fields are persisted
	assert.Equal(t, "Full field task", got.Title)
	assert.Equal(t, true, got.Manual)
	assert.Equal(t, "api", got.Executor)
	assert.Equal(t, "claude-opus", got.AgentProfile)
	assert.Equal(t, "close", got.OnDone)
	assert.Equal(t, "block", got.OnFail)
	assert.Equal(t, "auto-approve", got.OnReview)
	assert.Equal(t, "auto", got.OnDoneMerge)
	assert.Equal(t, "backend-fix", got.DeliverablePreset)
	assert.Equal(t, 5, got.MaxRetries)
	assert.True(t, got.CostBudget.Valid)
	assert.Equal(t, 50.0, got.CostBudget.Float64)
	assert.True(t, got.MaxDurationMs.Valid)
	assert.Equal(t, int64(60000), got.MaxDurationMs.Int64)
	assert.True(t, got.TokenBudget.Valid)
	assert.Equal(t, int64(100000), got.TokenBudget.Int64)

	// Verify JSON-blob fields are populated (parsed shape verified at HTTP layer)
	assert.True(t, got.Tools.Valid)
	assert.Contains(t, got.Tools.String, "bash")
	assert.True(t, got.Permissions.Valid)
	assert.Contains(t, got.Permissions.String, "network")
	assert.True(t, got.Environment.Valid)
	assert.Contains(t, got.Environment.String, "NODE_ENV")
	assert.True(t, got.Files.Valid)
	assert.Contains(t, got.Files.String, "main.go")
	assert.True(t, got.EscalationChain.Valid)
	assert.Contains(t, got.EscalationChain.String, "senior-agent")
	assert.True(t, got.QualityGates.Valid)
	assert.Contains(t, got.QualityGates.String, "go test")
	assert.True(t, got.Deliverables.Valid)
	assert.Contains(t, got.Deliverables.String, "diff")
	assert.True(t, got.DependsOn.Valid)
	assert.Contains(t, got.DependsOn.String, dep.ID)
	assert.True(t, got.Metadata.Valid)
	assert.Contains(t, got.Metadata.String, "source")
}

func TestCreateWithSentinelValues(t *testing.T) {
	svc := setupTaskValidationTest(t)

	unlimited := -1.0
	unlimitedInt := int64(-1)

	created, err := svc.Task.Create(service.TaskCreateInput{
		Title:         "Sentinel task",
		CostBudget:    &unlimited,
		MaxDurationMs: &unlimitedInt,
		TokenBudget:   &unlimitedInt,
	})
	require.NoError(t, err)

	got, err := svc.Task.Get(created.ID)
	require.NoError(t, err)

	require.True(t, got.CostBudget.Valid)
	assert.Equal(t, -1.0, got.CostBudget.Float64)
	require.True(t, got.MaxDurationMs.Valid)
	assert.Equal(t, int64(-1), got.MaxDurationMs.Int64)
	require.True(t, got.TokenBudget.Valid)
	assert.Equal(t, int64(-1), got.TokenBudget.Int64)
}
```

- [ ] **Step 2: Run the new tests**

Run: `go test ./internal/service/... -run 'TestCreateWithAllFieldsRoundTrip|TestCreateWithSentinelValues' -v`
Expected: PASS.

- [ ] **Step 3: Run the full service suite**

Run: `go test ./internal/service/... -v 2>&1 | tail -30`
Expected: all tests green.

- [ ] **Step 4: Commit**

```bash
git add internal/service/task_test.go
git commit -m "test(service): add round-trip create tests for all new task fields"
```

---

## Task 6: HTTP layer — request types and parse helpers

**Files:**
- Modify: `internal/httpserver/tasks.go`

- [ ] **Step 1: Add the new request types at the top of the file**

In `internal/httpserver/tasks.go`, find a logical location near the existing helper functions (after `nullTime` is fine). Add:

```go
// TaskCreateRequest is the JSON request body for POST /api/v1/tasks.
// Field types are non-pointer where the zero value is acceptable as
// "not provided" (e.g. empty string), and pointer where we need to
// distinguish "not provided" from "explicit zero" (numeric nullables).
type TaskCreateRequest struct {
	Title             string                `json:"title"`
	Description       string                `json:"description,omitempty"`
	Priority          int                   `json:"priority,omitempty"`
	Tags              []string              `json:"tags,omitempty"`
	Manual            bool                  `json:"manual,omitempty"`
	Executor          string                `json:"executor,omitempty"`
	AgentProfile      string                `json:"agent_profile,omitempty"`
	WorkingDir        string                `json:"working_dir,omitempty"`
	Tools             []string              `json:"tools,omitempty"`
	Permissions       map[string]any        `json:"permissions,omitempty"`
	Environment       map[string]string     `json:"environment,omitempty"`
	SystemPrompt      string                `json:"system_prompt,omitempty"`
	Files             []string              `json:"files,omitempty"`
	CostBudget        *float64              `json:"cost_budget,omitempty"`
	MaxRetries        *int                  `json:"max_retries,omitempty"`
	MaxDurationMs     *int64                `json:"max_duration_ms,omitempty"`
	TokenBudget       *int64                `json:"token_budget,omitempty"`
	OnDone            string                `json:"on_done,omitempty"`
	OnFail            string                `json:"on_fail,omitempty"`
	OnReview          string                `json:"on_review,omitempty"`
	OnDoneMerge       string                `json:"on_done_merge,omitempty"`
	EscalationChain   []string              `json:"escalation_chain,omitempty"`
	QualityGates      []string              `json:"quality_gates,omitempty"`
	Deliverables      []service.Deliverable `json:"deliverables,omitempty"`
	DeliverablePreset string                `json:"deliverable_preset,omitempty"`
	DependsOn         []string              `json:"depends_on,omitempty"`
	BlockedReason     string                `json:"blocked_reason,omitempty"`
	Metadata          map[string]any        `json:"metadata,omitempty"`
	SprintID          string                `json:"sprint_id,omitempty"`
	ProjectID         string                `json:"project_id,omitempty"`
	EpicID            string                `json:"epic_id,omitempty"`
}

// TaskUpdateRequest is the JSON request body for PUT /api/v1/tasks/:id.
// Every field is a pointer so "not provided" (nil) is distinguishable from
// "explicit zero/empty value". The Status field is present only so the
// handler can detect and reject it — status changes go through the
// dedicated /transition endpoint instead.
type TaskUpdateRequest struct {
	Title             *string                `json:"title,omitempty"`
	Description       *string                `json:"description,omitempty"`
	Priority          *int                   `json:"priority,omitempty"`
	Tags              *[]string              `json:"tags,omitempty"`
	Manual            *bool                  `json:"manual,omitempty"`
	Executor          *string                `json:"executor,omitempty"`
	AgentProfile      *string                `json:"agent_profile,omitempty"`
	WorkingDir        *string                `json:"working_dir,omitempty"`
	Tools             *[]string              `json:"tools,omitempty"`
	Permissions       *map[string]any        `json:"permissions,omitempty"`
	Environment       *map[string]string     `json:"environment,omitempty"`
	SystemPrompt      *string                `json:"system_prompt,omitempty"`
	Files             *[]string              `json:"files,omitempty"`
	CostBudget        *float64               `json:"cost_budget,omitempty"`
	MaxRetries        *int                   `json:"max_retries,omitempty"`
	MaxDurationMs     *int64                 `json:"max_duration_ms,omitempty"`
	TokenBudget       *int64                 `json:"token_budget,omitempty"`
	OnDone            *string                `json:"on_done,omitempty"`
	OnFail            *string                `json:"on_fail,omitempty"`
	OnReview          *string                `json:"on_review,omitempty"`
	OnDoneMerge       *string                `json:"on_done_merge,omitempty"`
	EscalationChain   *[]string              `json:"escalation_chain,omitempty"`
	QualityGates      *[]string              `json:"quality_gates,omitempty"`
	Deliverables      *[]service.Deliverable `json:"deliverables,omitempty"`
	DeliverablePreset *string                `json:"deliverable_preset,omitempty"`
	DependsOn         *[]string              `json:"depends_on,omitempty"`
	BlockedReason     *string                `json:"blocked_reason,omitempty"`
	Metadata          *map[string]any        `json:"metadata,omitempty"`
	SprintID          *string                `json:"sprint_id,omitempty"`
	ProjectID         *string                `json:"project_id,omitempty"`
	EpicID            *string                `json:"epic_id,omitempty"`

	// Status is included only for detection — the handler returns 400 if it
	// is non-nil and points the caller to POST /tasks/:id/transition.
	Status *string `json:"status,omitempty"`
}
```

- [ ] **Step 2: Add the four read-side parse helpers**

Add these helpers next to the existing `nullStr` / `nullFloat` / `nullInt` / `nullTime` block in the same file:

```go
// parseStringArray parses a NullString containing a JSON-encoded []string.
// Returns an empty slice (not nil) on missing or invalid data so the JSON
// response shape stays stable.
func parseStringArray(ns sql.NullString) []string {
	if !ns.Valid || ns.String == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

// parseStringMap parses a NullString containing a JSON-encoded map[string]string.
func parseStringMap(ns sql.NullString) map[string]string {
	if !ns.Valid || ns.String == "" {
		return map[string]string{}
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return map[string]string{}
	}
	return out
}

// parseFreeMap parses a NullString containing a JSON-encoded free-form map.
func parseFreeMap(ns sql.NullString) map[string]any {
	if !ns.Valid || ns.String == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

// parseDeliverables parses a NullString containing a JSON-encoded []Deliverable.
func parseDeliverables(ns sql.NullString) []service.Deliverable {
	if !ns.Valid || ns.String == "" {
		return []service.Deliverable{}
	}
	var out []service.Deliverable
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil || out == nil {
		return []service.Deliverable{}
	}
	return out
}
```

You'll need to add `"encoding/json"` to the imports at the top of `tasks.go` if it's not already imported. Check the imports block.

- [ ] **Step 3: Add the write-side `nullJSONString` helper**

Add immediately after the parse helpers:

```go
// nullJSONString marshals v to JSON and wraps the result in a *sql.NullString
// suitable for assigning to a TaskUpdate pointer field. Returns a nil-valued
// NullString if marshaling fails (should never happen for well-formed Go values
// the request layer accepts).
func nullJSONString(v any) *sql.NullString {
	raw, err := json.Marshal(v)
	if err != nil {
		return &sql.NullString{Valid: false}
	}
	return &sql.NullString{String: string(raw), Valid: true}
}
```

- [ ] **Step 4: Verify the file still compiles**

Run: `go build ./internal/httpserver/...`
Expected: clean build (the new code is unused so far, but should compile).

- [ ] **Step 5: Commit**

```bash
git add internal/httpserver/tasks.go
git commit -m "feat(http): add TaskCreateRequest, TaskUpdateRequest types and JSON-blob helpers"
```

---

## Task 7: HTTP layer — expand `taskJSON` with the 12 new fields

**Files:**
- Modify: `internal/httpserver/tasks.go`

- [ ] **Step 1: Update `taskJSON` to include all 12 new fields**

Find `func taskJSON(t *sqlstore.TaskRecord, tags []sqlstore.TagRecord)` (around line 17). Replace its body to include the new fields, keeping the existing fields in their current positions for minimal diff:

```go
func taskJSON(t *sqlstore.TaskRecord, tags []sqlstore.TagRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":                 t.ID,
		"title":              t.Title,
		"description":        t.Description,
		"status":             t.Status,
		"priority":           t.Priority,
		"tags":               tagsJSON(tags),
		"manual":             t.Manual,
		"executor":           t.Executor,
		"agent_profile":      t.AgentProfile,
		"working_dir":        t.WorkingDir,
		"tools":              parseStringArray(t.Tools),
		"permissions":        parseFreeMap(t.Permissions),
		"environment":        parseStringMap(t.Environment),
		"system_prompt":      t.SystemPrompt,
		"files":              parseStringArray(t.Files),
		"cost_budget":        nullFloat(t.CostBudget),
		"max_retries":        t.MaxRetries,
		"max_duration_ms":    nullInt(t.MaxDurationMs),
		"token_budget":       nullInt(t.TokenBudget),
		"on_done":            t.OnDone,
		"on_fail":            t.OnFail,
		"on_review":          t.OnReview,
		"on_done_merge":      t.OnDoneMerge,
		"escalation_chain":   parseStringArray(t.EscalationChain),
		"quality_gates":      parseStringArray(t.QualityGates),
		"deliverables":       parseDeliverables(t.Deliverables),
		"deliverable_preset": t.DeliverablePreset,
		"depends_on":         parseStringArray(t.DependsOn),
		"blocked_reason":     t.BlockedReason,
		"metadata":           parseFreeMap(t.Metadata),
		"sprint_id":          nullStr(t.SprintID),
		"project_id":         nullStr(t.ProjectID),
		"epic_id":            nullStr(t.EpicID),
		"created_at":         t.CreatedAt,
		"updated_at":         t.UpdatedAt,
	}
}
```

- [ ] **Step 2: Run the existing httpserver test suite to confirm no regressions**

Run: `go test ./internal/httpserver/... -v 2>&1 | tail -40`
Expected: all existing tests still pass. Existing tests assert specific field values; the new fields are additive.

- [ ] **Step 3: Verify build is clean**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/httpserver/tasks.go
git commit -m "feat(http): expand taskJSON response with 12 canonical task fields"
```

---

## Task 8: HTTP layer — rewrite `createTask` handler with `TaskCreateRequest`

**Files:**
- Modify: `internal/httpserver/tasks.go`
- Modify: `internal/httpserver/server_test.go`

- [ ] **Step 1: Write the failing test for full-field create**

Append to `internal/httpserver/server_test.go`:

```go
func TestCreateTaskWithAllFields(t *testing.T) {
	ts := setupTestServer(t)

	body := `{
		"title": "Full field task",
		"description": "All the fields",
		"priority": 1,
		"tags": ["bug","ui"],
		"manual": true,
		"executor": "api",
		"agent_profile": "claude-opus",
		"working_dir": "/repos/test",
		"tools": ["bash","edit"],
		"permissions": {"network": true},
		"environment": {"NODE_ENV": "test"},
		"system_prompt": "test agent",
		"files": ["src/main.go"],
		"cost_budget": 50.0,
		"max_retries": 5,
		"max_duration_ms": 60000,
		"token_budget": 100000,
		"on_done": "close",
		"on_fail": "block",
		"on_review": "auto-approve",
		"on_done_merge": "auto",
		"escalation_chain": ["senior","human"],
		"quality_gates": ["go test ./..."],
		"deliverables": [{"type":"diff","required":true}],
		"deliverable_preset": "backend-fix",
		"blocked_reason": "",
		"metadata": {"source":"test"}
	}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	assert.Equal(t, "Full field task", got["title"])
	assert.Equal(t, true, got["manual"])
	assert.Equal(t, "api", got["executor"])
	assert.Equal(t, "claude-opus", got["agent_profile"])
	assert.Equal(t, "close", got["on_done"])
	assert.Equal(t, "block", got["on_fail"])
	assert.Equal(t, "auto-approve", got["on_review"])
	assert.Equal(t, "auto", got["on_done_merge"])
	assert.Equal(t, "backend-fix", got["deliverable_preset"])
	assert.Equal(t, float64(5), got["max_retries"])
	assert.Equal(t, float64(50), got["cost_budget"])
	assert.Equal(t, float64(60000), got["max_duration_ms"])
	assert.Equal(t, float64(100000), got["token_budget"])

	// Structured fields
	tools, ok := got["tools"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"bash", "edit"}, tools)

	perms, ok := got["permissions"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, perms["network"])

	env, ok := got["environment"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "test", env["NODE_ENV"])

	delivs, ok := got["deliverables"].([]interface{})
	require.True(t, ok)
	require.Len(t, delivs, 1)
	d0 := delivs[0].(map[string]interface{})
	assert.Equal(t, "diff", d0["type"])
	assert.Equal(t, true, d0["required"])
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/httpserver/... -run TestCreateTaskWithAllFields -v`
Expected: the test fails because the current handler only accepts 6 fields and ignores the rest. Most assertions will fail.

- [ ] **Step 3: Rewrite the `createTask` handler**

In `internal/httpserver/tasks.go`, find `func (s *Server) createTask(w http.ResponseWriter, r *http.Request)` (around line 156). Replace its body:

```go
func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var req TaskCreateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	input := service.TaskCreateInput{
		Title:             req.Title,
		Description:       req.Description,
		Priority:          req.Priority,
		Tags:              req.Tags,
		Manual:            req.Manual,
		Executor:          req.Executor,
		AgentProfile:      req.AgentProfile,
		WorkingDir:        req.WorkingDir,
		Tools:             req.Tools,
		Permissions:       req.Permissions,
		Environment:       req.Environment,
		SystemPrompt:      req.SystemPrompt,
		Files:             req.Files,
		CostBudget:        req.CostBudget,
		MaxRetries:        req.MaxRetries,
		MaxDurationMs:     req.MaxDurationMs,
		TokenBudget:       req.TokenBudget,
		OnDone:            req.OnDone,
		OnFail:            req.OnFail,
		OnReview:          req.OnReview,
		OnDoneMerge:       req.OnDoneMerge,
		EscalationChain:   req.EscalationChain,
		QualityGates:      req.QualityGates,
		Deliverables:      req.Deliverables,
		DeliverablePreset: req.DeliverablePreset,
		DependsOn:         req.DependsOn,
		BlockedReason:     req.BlockedReason,
		Metadata:          req.Metadata,
		SprintID:          req.SprintID,
		ProjectID:         req.ProjectID,
		EpicID:            req.EpicID,
	}

	task, err := s.svc.Task.Create(input)
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sse.Broadcast("task.created", map[string]interface{}{"task_id": task.ID, "title": task.Title})
	writeJSON(w, http.StatusCreated, taskJSON(task, tags))
}
```

- [ ] **Step 4: Run the new test to verify it passes**

Run: `go test ./internal/httpserver/... -run TestCreateTaskWithAllFields -v`
Expected: PASS.

- [ ] **Step 5: Run the full httpserver suite to confirm no regressions**

Run: `go test ./internal/httpserver/... -v 2>&1 | tail -40`
Expected: all tests green, including existing `TestCreateAndGetTask` and the tag-system tests.

- [ ] **Step 6: Commit**

```bash
git add internal/httpserver/tasks.go internal/httpserver/server_test.go
git commit -m "feat(http): rewrite createTask handler with TaskCreateRequest typed input"
```

---

## Task 9: HTTP layer — rewrite `updateTask` handler with `TaskUpdateRequest`

**Files:**
- Modify: `internal/httpserver/tasks.go`
- Modify: `internal/httpserver/server_test.go`

- [ ] **Step 1: Write failing tests for the new update handler**

Append to `internal/httpserver/server_test.go`:

```go
func TestUpdateTaskAllNewFields(t *testing.T) {
	ts := setupTestServer(t)

	// Create a task with minimal fields
	createBody := `{"title":"Test","executor":"cli"}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(createBody))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	id := created["id"].(string)

	// Update each new field type
	updateBody := `{
		"agent_profile": "updated-profile",
		"working_dir": "/new/dir",
		"tools": ["bash","grep"],
		"permissions": {"network": false},
		"environment": {"DEBUG": "1"},
		"system_prompt": "updated",
		"files": ["new.go"],
		"cost_budget": 25.5,
		"max_retries": 10,
		"max_duration_ms": 45000,
		"token_budget": 200000,
		"on_done": "review",
		"on_fail": "retry",
		"escalation_chain": ["senior"],
		"quality_gates": ["lint"],
		"deliverables": [{"type":"log","required":false}],
		"deliverable_preset": "research",
		"blocked_reason": "waiting on data",
		"metadata": {"updated":true}
	}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&updated))

	assert.Equal(t, "updated-profile", updated["agent_profile"])
	assert.Equal(t, "/new/dir", updated["working_dir"])
	assert.Equal(t, float64(25.5), updated["cost_budget"])
	assert.Equal(t, float64(10), updated["max_retries"])
	assert.Equal(t, float64(45000), updated["max_duration_ms"])
	assert.Equal(t, float64(200000), updated["token_budget"])
	assert.Equal(t, "review", updated["on_done"])
	assert.Equal(t, "research", updated["deliverable_preset"])
	assert.Equal(t, "waiting on data", updated["blocked_reason"])
}

func TestUpdateTaskRejectsStatusField(t *testing.T) {
	ts := setupTestServer(t)

	createBody := `{"title":"Test","executor":"cli"}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(createBody))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	id := created["id"].(string)

	updateBody := `{"status":"done"}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)

	var errBody map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&errBody))
	assert.Contains(t, errBody["error"].(string), "transition")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/httpserver/... -run 'TestUpdateTaskAllNewFields|TestUpdateTaskRejectsStatusField' -v`
Expected: tests fail because the current handler only accepts title/description/priority/tags.

- [ ] **Step 3: Rewrite the `updateTask` handler**

Find `func (s *Server) updateTask(w http.ResponseWriter, r *http.Request)` (around line 200). Replace its body:

```go
func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req TaskUpdateRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Status != nil {
		writeError(w, http.StatusBadRequest,
			"status updates are not allowed via PUT /tasks/:id — use POST /tasks/:id/transition")
		return
	}

	update := sqlstore.TaskUpdate{
		Title:             req.Title,
		Description:       req.Description,
		Priority:          req.Priority,
		Manual:            req.Manual,
		Executor:          req.Executor,
		AgentProfile:      req.AgentProfile,
		WorkingDir:        req.WorkingDir,
		SystemPrompt:      req.SystemPrompt,
		MaxRetries:        req.MaxRetries,
		OnDone:            req.OnDone,
		OnFail:            req.OnFail,
		OnReview:          req.OnReview,
		OnDoneMerge:       req.OnDoneMerge,
		DeliverablePreset: req.DeliverablePreset,
		BlockedReason:     req.BlockedReason,
	}

	// JSON-blob fields wrap as *sql.NullString
	if req.Tools != nil {
		update.Tools = nullJSONString(*req.Tools)
	}
	if req.Permissions != nil {
		update.Permissions = nullJSONString(*req.Permissions)
	}
	if req.Environment != nil {
		update.Environment = nullJSONString(*req.Environment)
	}
	if req.Files != nil {
		update.Files = nullJSONString(*req.Files)
	}
	if req.EscalationChain != nil {
		update.EscalationChain = nullJSONString(*req.EscalationChain)
	}
	if req.QualityGates != nil {
		update.QualityGates = nullJSONString(*req.QualityGates)
	}
	if req.Deliverables != nil {
		update.Deliverables = nullJSONString(*req.Deliverables)
	}
	if req.DependsOn != nil {
		update.DependsOn = nullJSONString(*req.DependsOn)
	}
	if req.Metadata != nil {
		update.Metadata = nullJSONString(*req.Metadata)
	}

	// Numeric nullables wrap as *sql.NullFloat64 / *sql.NullInt64
	if req.CostBudget != nil {
		update.CostBudget = &sql.NullFloat64{Float64: *req.CostBudget, Valid: true}
	}
	if req.MaxDurationMs != nil {
		update.MaxDurationMs = &sql.NullInt64{Int64: *req.MaxDurationMs, Valid: true}
	}
	if req.TokenBudget != nil {
		update.TokenBudget = &sql.NullInt64{Int64: *req.TokenBudget, Valid: true}
	}

	// Association fields wrap as *sql.NullString
	if req.SprintID != nil {
		update.SprintID = &sql.NullString{String: *req.SprintID, Valid: *req.SprintID != ""}
	}
	if req.ProjectID != nil {
		update.ProjectID = &sql.NullString{String: *req.ProjectID, Valid: *req.ProjectID != ""}
	}
	if req.EpicID != nil {
		update.EpicID = &sql.NullString{String: *req.EpicID, Valid: *req.EpicID != ""}
	}

	input := service.TaskUpdateInput{TaskUpdate: update, Tags: req.Tags}

	if err := s.svc.Task.Update(id, input); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	task, err := s.svc.Task.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tags, err := s.svc.Task.ListTags(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sse.Broadcast("task.updated", map[string]interface{}{"task_id": id})
	writeJSON(w, http.StatusOK, taskJSON(task, tags))
}
```

- [ ] **Step 4: Run the new tests to verify they pass**

Run: `go test ./internal/httpserver/... -run 'TestUpdateTaskAllNewFields|TestUpdateTaskRejectsStatusField' -v`
Expected: 2 PASS.

- [ ] **Step 5: Run the full httpserver suite**

Run: `go test ./internal/httpserver/... -v 2>&1 | tail -50`
Expected: all tests green. Existing `TestUpdateTaskTags` and `TestUpdateTaskTagsInvalidPayloadReturns400` should still pass since they exercise the `tags` path which is unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/httpserver/tasks.go internal/httpserver/server_test.go
git commit -m "feat(http): rewrite updateTask handler with TaskUpdateRequest typed input"
```

---

## Task 10: HTTP layer — error path tests for the new validation

**Files:**
- Modify: `internal/httpserver/server_test.go`

- [ ] **Step 1: Add tests for each error path**

Append to `internal/httpserver/server_test.go`:

```go
func TestCreateTaskInvalidEnumReturns422(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test","executor":"cli","on_done":"purge"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errBody map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	assert.Contains(t, errBody["error"].(string), "on_done")
}

func TestCreateTaskInvalidNumericSentinelReturns422(t *testing.T) {
	ts := setupTestServer(t)

	// max_duration_ms = 0 is rejected
	body1 := `{"title":"Test","executor":"cli","max_duration_ms":0}`
	resp1, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body1))
	assert.Equal(t, http.StatusUnprocessableEntity, resp1.StatusCode)

	// max_duration_ms = -2 is rejected (only -1 is valid negative)
	body2 := `{"title":"Test","executor":"cli","max_duration_ms":-2}`
	resp2, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body2))
	assert.Equal(t, http.StatusUnprocessableEntity, resp2.StatusCode)

	// cost_budget = -5 is rejected
	body3 := `{"title":"Test","executor":"cli","cost_budget":-5}`
	resp3, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body3))
	assert.Equal(t, http.StatusUnprocessableEntity, resp3.StatusCode)
}

func TestCreateTaskUnknownDependsOnReturns422(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test","executor":"cli","depends_on":["CW-99999999-9999"]}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errBody map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&errBody)
	assert.Contains(t, errBody["error"].(string), "depends_on")
}

func TestCreateTaskInvalidDeliverableTypeReturns422(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test","executor":"cli","deliverables":[{"type":"screencast","required":true}]}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errBody map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&errBody)
	assert.Contains(t, errBody["error"].(string), "deliverables")
}

func TestCreateTaskSentinelUnlimitedRoundTrip(t *testing.T) {
	ts := setupTestServer(t)

	body := `{
		"title":"Sentinel task",
		"executor":"cli",
		"cost_budget":-1,
		"max_duration_ms":-1,
		"token_budget":-1
	}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, float64(-1), got["cost_budget"])
	assert.Equal(t, float64(-1), got["max_duration_ms"])
	assert.Equal(t, float64(-1), got["token_budget"])

	id := got["id"].(string)

	// Now PUT a partial update with cost_budget = 50 (resetting the sentinel to a real value)
	updateBody := `{"cost_budget":50.0}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&updated))
	assert.Equal(t, float64(50), updated["cost_budget"])
	// The other two sentinel fields should be unchanged
	assert.Equal(t, float64(-1), updated["max_duration_ms"])
	assert.Equal(t, float64(-1), updated["token_budget"])
}

func TestUpdateTaskEmptyBodyIsNoOp(t *testing.T) {
	ts := setupTestServer(t)

	// Create a task
	createBody := `{"title":"Test","executor":"cli"}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(createBody))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	id := created["id"].(string)
	originalTitle := created["title"]

	// Empty body update should be 200 with the unchanged task
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&updated))
	assert.Equal(t, originalTitle, updated["title"])
}
```

- [ ] **Step 2: Run all the new tests**

Run: `go test ./internal/httpserver/... -run 'TestCreateTaskInvalid|TestCreateTaskUnknown|TestCreateTaskSentinel|TestUpdateTaskEmpty' -v`
Expected: 6 PASS (TestCreateTaskInvalidEnumReturns422, TestCreateTaskInvalidNumericSentinelReturns422, TestCreateTaskUnknownDependsOnReturns422, TestCreateTaskInvalidDeliverableTypeReturns422, TestCreateTaskSentinelUnlimitedRoundTrip, TestUpdateTaskEmptyBodyIsNoOp).

- [ ] **Step 3: Run the full httpserver suite**

Run: `go test ./internal/httpserver/... -v 2>&1 | tail -50`
Expected: all tests green.

- [ ] **Step 4: Commit**

```bash
git add internal/httpserver/server_test.go
git commit -m "test(http): add validation error and sentinel round-trip tests"
```

---

## Task 11: Frontend types — add new types and expand `Task`

**Files:**
- Modify: `apps/gui/src/lib/types.ts`

- [ ] **Step 1: Add the new types and expand `Task`**

Open `apps/gui/src/lib/types.ts`. After the existing `Tag` interface (around line 23), add the new enum types and `Deliverable`:

```ts
export type OnDone = 'close' | 'review' | 'notify'
export type OnFail = 'retry' | 'block' | 'escalate' | 'notify'
export type OnReview = 'pause' | 'notify' | 'auto-approve'
export type OnDoneMerge = 'none' | 'auto' | 'pr' | 'auto-resolve'

export type DeliverableType =
  | 'diff'
  | 'test-results'
  | 'screenshot'
  | 'pr-link'
  | 'branch'
  | 'commit'
  | 'log'
  | 'finding'
  | 'report'
  | 'note'
  | 'metrics'
  | 'custom'

export interface Deliverable {
  type: DeliverableType
  required: boolean
  description?: string
}

/**
 * Sentinel value meaning "unlimited — no cap" for the three nullable
 * numeric task fields (cost_budget, max_duration_ms, token_budget).
 */
export const UNLIMITED = -1 as const
```

Then find the `Task` interface (around line 25) and replace it with the expanded version:

```ts
export interface Task {
  id: string
  title: string
  description: string
  status: TaskStatus
  priority: number
  tags: Tag[]
  manual: boolean
  executor: string
  agent_profile: string
  working_dir: string

  // Execution context
  tools: string[]
  permissions: Record<string, unknown>
  environment: Record<string, string>
  system_prompt: string
  files: string[]

  // Budget & limits
  /**
   * Cost budget in dollars. Sentinel values:
   * - `null` — use default (inherit from sprint/global)
   * - `-1` — unlimited (no cap)
   * - `0` — explicit zero (no spend allowed)
   * - positive — specific budget
   */
  cost_budget: number | null
  max_retries: number
  /**
   * Max execution duration in milliseconds. Sentinel values:
   * - `null` — use default
   * - `-1` — unlimited
   * - positive — specific duration
   */
  max_duration_ms: number | null
  /**
   * Max tokens per run. Sentinel values:
   * - `null` — use default
   * - `-1` — unlimited
   * - positive — specific cap
   */
  token_budget: number | null

  // Lifecycle rules (narrowed from string to enum unions)
  on_done: OnDone
  on_fail: OnFail
  on_review: OnReview
  on_done_merge: OnDoneMerge

  // Lifecycle extensions
  escalation_chain: string[]
  quality_gates: string[]

  // Deliverables
  deliverables: Deliverable[]
  deliverable_preset: string

  // Dependencies
  depends_on: string[]
  blocked_reason: string

  // Metadata
  metadata: Record<string, unknown>

  // Grouping
  sprint_id: string | null
  project_id: string | null
  epic_id: string | null

  // Audit
  created_at: string
  updated_at: string
}
```

- [ ] **Step 2: Run the TypeScript check**

Run: `cd apps/gui && npx tsc --noEmit 2>&1 | head -20`
Expected: clean (no errors). The expanded `Task` interface adds new optional fields from the consumer perspective; nothing currently reads them.

- [ ] **Step 3: Run the lint**

Run: `cd apps/gui && npm run lint 2>&1 | tail -10`
Expected: no new errors beyond the pre-existing 11.

- [ ] **Step 4: Commit**

```bash
git add apps/gui/src/lib/types.ts
git commit -m "feat(types): add Deliverable, lifecycle enums, UNLIMITED, expand Task with 12 fields"
```

---

## Task 12: Frontend API client — extend type imports

**Files:**
- Modify: `apps/gui/src/lib/api.ts`

- [ ] **Step 1: Extend the type imports**

In `apps/gui/src/lib/api.ts`, find the existing `import type { ... } from './types'` block and add the new types:

```ts
import type {
  Task,
  TaskFilter,
  Run,
  Artifact,
  Comment,
  SSEEvent,
  FeatureFlags,
  Project,
  Sprint,
  Epic,
  Tag,
  TagColor,
  Deliverable,
  DeliverableType,
  OnDone,
  OnFail,
  OnReview,
  OnDoneMerge,
} from './types'
```

(Add only the new types — `Deliverable`, `DeliverableType`, `OnDone`, `OnFail`, `OnReview`, `OnDoneMerge` — to whatever's already in the import.)

- [ ] **Step 2: Verify the imports compile**

Run: `cd apps/gui && npx tsc --noEmit 2>&1 | head -20`
Expected: clean. The new imports are unused at the moment but the existing `Partial<Task>` signature on `createTask`/`updateTask` already accommodates the new `Task` fields automatically.

- [ ] **Step 3: Commit**

```bash
git add apps/gui/src/lib/api.ts
git commit -m "feat(gui): import new task type aliases in api client"
```

---

## Task 13: Full backend test suite + smoke test

**Files:** none — verification only.

- [ ] **Step 1: Run the full backend test suite**

Run: `go test ./... 2>&1 | tail -25`
Expected: all 20 packages green.

- [ ] **Step 2: Run `go vet`**

Run: `go vet ./... 2>&1 | head -10`
Expected: clean.

- [ ] **Step 3: Run `gofmt -l` on this branch's touched files**

Run:
```bash
git diff --name-only main..HEAD | grep '\.go$' | while read f; do
  if [ -n "$f" ] && [ -n "$(gofmt -l "$f" 2>/dev/null)" ]; then
    echo "drift: $f"
  fi
done
```
Expected: empty output. If anything has drift, run `gofmt -w` on it and re-commit as a small chore.

- [ ] **Step 4: Rebuild the API via cerberus**

Use the cerberus MCP tool `cerberus_rebuild` with `service_id: torque-api` and a reason like `"Deploy task API expansion: 12 new fields, sentinel values, strict validation, typed request shapes"`.

Expected: rebuild succeeds, service restarts.

- [ ] **Step 5: Smoke-test full-field create**

Run:
```bash
curl -s -X POST http://localhost:8990/api/v1/tasks \
  -H 'content-type: application/json' \
  -d '{
    "title": "Smoke test full fields",
    "executor": "cli",
    "tools": ["bash","edit"],
    "permissions": {"network": false},
    "environment": {"NODE_ENV": "test"},
    "files": ["src/main.go"],
    "cost_budget": -1,
    "max_duration_ms": -1,
    "token_budget": 50000,
    "on_done": "close",
    "deliverables": [{"type":"diff","required":true}],
    "metadata": {"smoke":"test"}
  }' | jq
```
Expected: 201 response with all fields present, `cost_budget: -1`, `max_duration_ms: -1`, `token_budget: 50000`.

- [ ] **Step 6: Smoke-test enum rejection**

Run:
```bash
curl -s -o /tmp/out -w "HTTP %{http_code}\n" -X POST http://localhost:8990/api/v1/tasks \
  -H 'content-type: application/json' \
  -d '{"title":"Bad enum","executor":"cli","on_done":"purge"}'
cat /tmp/out
```
Expected: `HTTP 422` with an error message naming the allowed `on_done` set.

- [ ] **Step 7: Smoke-test status rejection on update**

Run:
```bash
# First create a task
TASK_ID=$(curl -s -X POST http://localhost:8990/api/v1/tasks \
  -H 'content-type: application/json' \
  -d '{"title":"Status test","executor":"cli"}' | jq -r .id)

# Now try to update status via PUT
curl -s -o /tmp/out -w "HTTP %{http_code}\n" -X PUT http://localhost:8990/api/v1/tasks/$TASK_ID \
  -H 'content-type: application/json' \
  -d '{"status":"done"}'
cat /tmp/out
```
Expected: `HTTP 400` with an error message pointing to `/transition`.

- [ ] **Step 8: No commit — verification only**

If any smoke test fails, fix and commit. Otherwise, the project is verified end-to-end and ready for the PR cycle.

---

## Success criteria (from spec Section 13)

- `go build ./...` clean ✓
- `go vet ./...` clean ✓
- `go test ./...` all 20 packages green ✓
- `gofmt -l` on touched files empty ✓
- `cd apps/gui && npx tsc --noEmit` clean ✓
- `cd apps/gui && npm run lint` → no new errors beyond the pre-existing 11 ✓
- Round-trip smoke test: full-field create → response includes every field ✓
- Enum validation rejects `{"on_done":"bogus"}` with 422 ✓
- Status rejection: `{"status":"done"}` on PUT returns 400 ✓
- Unknown `depends_on` task ID returns 422 ✓
- Unknown `deliverables[0].type` returns 422 ✓
- Sentinel value round-trip: `-1` persists and reads back correctly ✓
- Sentinel rejection: `0` for `max_duration_ms`, `-2` for any sentinel field, returns 422 ✓

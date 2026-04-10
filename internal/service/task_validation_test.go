package service_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
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
	for i, ty := range allTypes {
		dels[i] = service.Deliverable{Type: ty, Required: false}
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

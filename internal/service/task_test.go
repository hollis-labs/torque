package service_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"

	_ "modernc.org/sqlite"
)

func setupService(t *testing.T) *service.Service {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return service.New(store)
}

func TestTaskCreate(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:    "My first task",
		Priority: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, task)
	require.True(t, strings.Contains(task.ID, "CW-"), "ID should contain CW-")
	require.Equal(t, "todo", task.Status)
	require.Equal(t, 1, task.Priority)
}

func TestTaskCreate_FacetDefaults(t *testing.T) {
	svc := setupService(t)

	rec, err := svc.Task.Create(service.TaskCreateInput{Title: "minimal"})
	require.NoError(t, err)
	require.Equal(t, "agent", rec.Kind)
	require.Equal(t, "user", rec.SourceType)
	require.False(t, rec.SourceRef.Valid)
	require.Equal(t, "normal", rec.Trust)
	require.Equal(t, "none", rec.CheckpointMode)
	require.Equal(t, "resume", rec.OnCheckpointResponse)
}

func TestTaskCreate_FacetExplicit(t *testing.T) {
	svc := setupService(t)

	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:                "explicit",
		Kind:                 "agent",
		SourceType:           "agent",
		SourceRef:            "claude-code",
		Trust:                "trusted",
		CheckpointMode:       "blocking",
		OnCheckpointResponse: "review",
	})
	require.NoError(t, err)
	require.Equal(t, "agent", rec.SourceType)
	require.True(t, rec.SourceRef.Valid)
	require.Equal(t, "claude-code", rec.SourceRef.String)
	require.Equal(t, "trusted", rec.Trust)
	require.Equal(t, "blocking", rec.CheckpointMode)
	require.Equal(t, "review", rec.OnCheckpointResponse)
}

func TestTaskCreateValidation(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Task.Create(service.TaskCreateInput{Title: ""})
	require.Error(t, err)

	var ve *service.ValidationError
	require.ErrorAs(t, err, &ve)
}

func TestTaskTransitionValid(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "Transition test"})
	require.NoError(t, err)

	err = svc.Task.Transition(task.ID, "doing")
	require.NoError(t, err)

	updated, err := svc.Task.Get(task.ID)
	require.NoError(t, err)
	require.Equal(t, "doing", updated.Status)
}

func TestTaskTransitionInvalid(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "Invalid transition test"})
	require.NoError(t, err)

	// todo → done is not a valid transition
	err = svc.Task.Transition(task.ID, "done")
	require.Error(t, err)

	var te *service.TransitionError
	require.ErrorAs(t, err, &te)
}

func TestTaskSearch(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Task.Create(service.TaskCreateInput{Title: "Alpha task about widgets"})
	require.NoError(t, err)

	_, err = svc.Task.Create(service.TaskCreateInput{Title: "Beta task about gadgets"})
	require.NoError(t, err)

	results, err := svc.Task.Search("widgets")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Contains(t, results[0].Title, "widgets")
}

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
		Title:           "Full field task",
		Description:     "All the fields",
		Priority:        1,
		Tags:            []string{"bug", "ui"},
		Manual:          true,
		Executor:        "api",
		AgentProfile:    "claude-opus",
		WorkingDir:      "/repos/test",
		Tools:           []string{"bash", "edit", "read"},
		Permissions:     map[string]any{"network": true, "filesystem": "read-only"},
		Environment:     map[string]string{"NODE_ENV": "test", "DEBUG": "1"},
		SystemPrompt:    "You are a test agent",
		Files:           []string{"src/main.go", "src/utils.go"},
		CostBudget:      &costBudget,
		MaxRetries:      &maxRetries,
		MaxDurationMs:   &maxDurationMs,
		TokenBudget:     &tokenBudget,
		OnDone:          "close",
		OnFail:          "block",
		OnReview:        "auto-approve",
		OnDoneMerge:     "auto",
		EscalationChain: []string{"senior-agent", "human-reviewer"},
		QualityGates:    []string{"go test ./...", "go vet ./..."},
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

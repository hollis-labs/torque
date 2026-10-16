package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSelfReportReceiptsRateAndNoParking(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "report", ProgressDetail: "step", CheckpointMode: "blocking"})
	require.NoError(t, err)
	ctx := context.Background()
	in := map[string]any{"task_id": task.ID, "label": "step one", "idempotency_key": "same"}
	var wg sync.WaitGroup
	results := make(chan map[string]any, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out, err := svc.Task.Progress(ctx, in); results <- out; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	emitted, duplicates := 0, 0
	eventID := ""
	for out := range results {
		if out["emitted"] == true {
			emitted++
		} else {
			require.Equal(t, "duplicate", out["suppressed_reason"])
			duplicates++
		}
		if eventID == "" {
			eventID = out["event_id"].(string)
		}
		require.Equal(t, eventID, out["event_id"])
	}
	require.Equal(t, 1, emitted)
	require.Equal(t, 7, duplicates)
	next := map[string]any{"task_id": task.ID, "label": "step two", "idempotency_key": "retry"}
	out, err := svc.Task.Progress(ctx, next)
	require.NoError(t, err)
	require.Equal(t, "rate_limited", out["suppressed_reason"])
	var n int
	require.NoError(t, store.DB().QueryRow(`SELECT COUNT(*) FROM task_self_report_receipts WHERE idempotency_key='retry'`).Scan(&n))
	require.Zero(t, n)
	// Advance the persisted slot, not a process-local clock; the boundary is inclusive.
	_, err = store.DB().Exec(`UPDATE task_progress_rate SET last_emitted_at=? WHERE task_id=?`, time.Now().Add(-2*time.Second).UTC().Format(time.RFC3339Nano), task.ID)
	require.NoError(t, err)
	out, err = svc.Task.Progress(ctx, next)
	require.NoError(t, err)
	require.Equal(t, true, out["emitted"])
	changed := map[string]any{"task_id": task.ID, "label": "changed", "idempotency_key": "same"}
	_, err = svc.Task.Progress(ctx, changed)
	assertReportCode(t, err, "idempotency_conflict")
	_, err = svc.Task.EmitData(ctx, map[string]any{"task_id": task.ID, "schema": "stats@1", "data": map[string]any{}, "idempotency_key": "same"})
	assertReportCode(t, err, "idempotency_conflict")
	after, err := svc.Task.Get(task.ID)
	require.NoError(t, err)
	require.Equal(t, task.Status, after.Status)
	require.NoError(t, store.DB().QueryRow(`SELECT COUNT(*) FROM checkpoints WHERE task_id=?`, task.ID).Scan(&n))
	require.Zero(t, n)
	// Even a changed setting cannot override an emitted receipt's original result.
	off := sql.NullString{String: "off", Valid: true}
	require.NoError(t, svc.Task.Update(task.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{ProgressDetail: &off}}))
	out, err = svc.Task.Progress(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "duplicate", out["suppressed_reason"])
	require.Equal(t, "step", out["effective_level"])
	below := map[string]any{"task_id": task.ID, "level": "milestone", "label": "boundary", "idempotency_key": "below"}
	out, err = svc.Task.Progress(ctx, below)
	require.NoError(t, err)
	require.Equal(t, "below_detail", out["suppressed_reason"])
	step := sql.NullString{String: "step", Valid: true}
	require.NoError(t, svc.Task.Update(task.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{ProgressDetail: &step}}))
	out, err = svc.Task.Progress(ctx, below)
	require.NoError(t, err)
	require.Equal(t, true, out["emitted"])
}

func assertReportCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *service.SelfReportError
	require.ErrorAs(t, err, &e)
	require.Equal(t, code, e.Code)
}

func TestSelfReportDataValidationOwnershipAndScope(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "report", ProgressDetail: "off"})
	require.NoError(t, err)
	other, err := svc.Task.Create(service.TaskCreateInput{Title: "other"})
	require.NoError(t, err)
	run := &sqlstore.RunRecord{TaskID: task.ID, Executor: "cli", ProgressDetail: sql.NullString{String: "verbose", Valid: true}, TetherSessionID: sql.NullString{String: "ses_verified", Valid: true}, LaunchID: sql.NullString{String: "launch_verified", Valid: true}, WorkstreamID: sql.NullString{String: "ws_verified", Valid: true}}
	runID, err := store.CreateRun(run)
	require.NoError(t, err)
	ctx := sqlstore.ContextWithTrigger(context.Background(), sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: "test"})
	input := map[string]any{"task_id": task.ID, "schema": "stats@1", "data": map[string]any{"z": json.Number("9007199254740993"), "a": true}, "idempotency_key": "data"}
	out, err := svc.Task.EmitData(ctx, input)
	require.NoError(t, err)
	require.NotEmpty(t, out["data_ref"])
	again, err := svc.Task.EmitData(ctx, input)
	require.NoError(t, err)
	require.Equal(t, out["event_id"], again["event_id"])
	require.Equal(t, true, again["duplicate"])
	var raw string
	require.NoError(t, store.DB().QueryRow(`SELECT envelope FROM event_outbox WHERE event_id=?`, out["event_id"]).Scan(&raw))
	require.Contains(t, raw, "9007199254740993")
	progress := map[string]any{"task_id": task.ID, "run_id": fmt.Sprint(runID), "label": "🔧", "data_ref": out["data_ref"]}
	result, err := svc.Task.Progress(ctx, progress)
	require.NoError(t, err)
	require.Equal(t, true, result["emitted"])
	require.Equal(t, "launch", result["detail_source"])
	require.NoError(t, store.DB().QueryRow(`SELECT envelope FROM event_outbox WHERE event_id=?`, result["event_id"]).Scan(&raw))
	var event runtimeevents.Event
	require.NoError(t, json.Unmarshal([]byte(raw), &event))
	require.Equal(t, "ses_verified", event.SessionID)
	require.Equal(t, uint32(1), event.Scope.Attempt)
	require.Equal(t, "launch_verified", event.Scope.LaunchID)
	require.Equal(t, "ws_verified", event.Scope.WorkstreamID)
	require.False(t, event.Class.Sanitized)
	progress["task_id"] = other.ID
	_, err = svc.Task.Progress(ctx, progress)
	assertReportCode(t, err, "arg_invalid")
	artifact := &sqlstore.ArtifactRecord{TaskID: task.ID, Type: "json", Content: "{}"}
	require.NoError(t, store.CreateArtifact(artifact))
	artifactIn := map[string]any{"task_id": task.ID, "schema": "stats@1", "artifact_id": fmt.Sprint(artifact.ID)}
	_, err = svc.Task.EmitData(ctx, artifactIn)
	require.NoError(t, err)
	artifactIn["task_id"] = other.ID
	_, err = svc.Task.EmitData(ctx, artifactIn)
	assertReportCode(t, err, "arg_invalid")
	for _, bad := range []map[string]any{
		{"task_id": task.ID, "label": strings.Repeat("é", 81)},
		{"task_id": task.ID, "label": "x", "summary": strings.Repeat("é", 601)},
		{"task_id": task.ID, "label": "x", "pct": 101},
		{"task_id": task.ID, "label": "x", "step": 0.5},
		{"task_id": task.ID, "label": "x", "total_steps": -1},
		{"task_id": task.ID, "label": "x", "run_id": "0x12"},
		{"task_id": task.ID, "label": "x", "level": "off"},
		{"task_id": task.ID, "label": "x", "bogus": true},
	} {
		_, err := svc.Task.Progress(ctx, bad)
		require.Error(t, err)
	}
	// {"x":"..."} is eight bytes beyond the value itself.
	for _, size := range []int{16384, 16385} {
		data := map[string]any{"x": strings.Repeat("a", size-8)}
		_, err := svc.Task.EmitData(ctx, map[string]any{"task_id": task.ID, "schema": "s@1", "data": data})
		if size == 16384 {
			require.NoError(t, err)
		} else {
			assertReportCode(t, err, "data_too_large")
		}
	}
	_, err = svc.Task.EmitData(ctx, map[string]any{"task_id": task.ID, "schema": "s@1", "data": map[string]any{}, "artifact_id": fmt.Sprint(artifact.ID)})
	require.Error(t, err)
}

func TestSelfReportRollbackIsAtomic(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "rollback", ProgressDetail: "step"})
	require.NoError(t, err)
	_, err = store.DB().Exec(`CREATE TRIGGER fail_receipt BEFORE INSERT ON task_self_report_receipts BEGIN SELECT RAISE(ABORT, 'injected'); END`)
	require.NoError(t, err)
	in := map[string]any{"task_id": task.ID, "label": "retry", "idempotency_key": "retry"}
	_, err = svc.Task.Progress(context.Background(), in)
	require.Error(t, err)
	for _, table := range []string{"event_outbox", "task_progress_rate", "task_self_report_receipts"} {
		var n int
		require.NoError(t, store.DB().QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE task_id=?`, task.ID).Scan(&n))
		require.Zero(t, n, table)
	}
	_, err = store.DB().Exec(`DROP TRIGGER fail_receipt`)
	require.NoError(t, err)
	out, err := svc.Task.Progress(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, true, out["emitted"])
}

func TestSelfReportBoundSessionOnly(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "session", ProgressDetail: "off"})
	require.NoError(t, err)
	runID, err := store.CreateRun(&sqlstore.RunRecord{TaskID: task.ID, Executor: "cli"})
	require.NoError(t, err)
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "SES-bound", TaskID: sql.NullString{String: task.ID, Valid: true}, RunID: sql.NullInt64{Int64: runID, Valid: true}, ProgressDetail: sql.NullString{String: "step", Valid: true}}))
	in := map[string]any{"task_id": task.ID, "label": "bound"}
	out, err := svc.Task.Progress(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, "below_detail", out["suppressed_reason"], "never infer latest session or run")
	ctx := sqlstore.ContextWithReportSession(context.Background(), "SES-bound")
	out, err = svc.Task.Progress(ctx, in)
	require.NoError(t, err)
	require.Equal(t, true, out["emitted"])
	require.Equal(t, "launch", out["detail_source"])
	var raw string
	require.NoError(t, store.DB().QueryRow(`SELECT envelope FROM event_outbox WHERE event_id=?`, out["event_id"]).Scan(&raw))
	var ev runtimeevents.Event
	require.NoError(t, json.Unmarshal([]byte(raw), &ev))
	require.Equal(t, fmt.Sprint(runID), ev.Scope.RunID)
	require.Empty(t, ev.SessionID, "Torque session id is not a Tether identity")
}

func TestSelfReportReceiptsAndRateSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports.db")
	open := func() *sqlstore.Store {
		db, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		require.NoError(t, migrations.Run(db))
		store, err := sqlstore.New(db, "sqlite")
		require.NoError(t, err)
		return store
	}
	store := open()
	svc := service.New(store)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "restart", ProgressDetail: "step"})
	require.NoError(t, err)
	in := map[string]any{"task_id": task.ID, "label": "persist", "idempotency_key": "persist"}
	first, err := svc.Task.Progress(context.Background(), in)
	require.NoError(t, err)
	// A future persisted slot also exercises clock regression conservatively.
	_, err = store.DB().Exec(`UPDATE task_progress_rate SET last_emitted_at=? WHERE task_id=?`, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), task.ID)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	store = open()
	defer store.Close()
	svc = service.New(store)
	out, err := svc.Task.Progress(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, "duplicate", out["suppressed_reason"])
	require.Equal(t, first["event_id"], out["event_id"])
	in["idempotency_key"] = "new"
	out, err = svc.Task.Progress(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, "rate_limited", out["suppressed_reason"])
}

func TestSelfReportCanonicalDataInput(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "canonical"})
	require.NoError(t, err)
	in := map[string]any{"task_id": task.ID, "schema": "numbers@1", "data": map[string]any{"n": json.Number("1.0")}, "idempotency_key": "n"}
	first, err := svc.Task.EmitData(context.Background(), in)
	require.NoError(t, err)
	in["data"] = map[string]any{"n": json.Number("1e0")}
	out, err := svc.Task.EmitData(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, true, out["duplicate"])
	require.Equal(t, first["event_id"], out["event_id"])
	in["data"] = map[string]any{"n": []any{"number", "1"}}
	_, err = svc.Task.EmitData(context.Background(), in)
	assertReportCode(t, err, "idempotency_conflict")
}

func TestSelfReportCatalogPayloadConformance(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "conformance", ProgressDetail: "verbose"})
	require.NoError(t, err)
	artifact := &sqlstore.ArtifactRecord{TaskID: task.ID, Type: "json", Content: "{}"}
	require.NoError(t, store.CreateArtifact(artifact))
	for _, input := range []map[string]any{
		{"task_id": task.ID, "schema": "test@1", "data": map[string]any{"x": true}},
		{"task_id": task.ID, "schema": "test@1", "artifact_id": fmt.Sprint(artifact.ID)},
		{"task_id": task.ID, "label": "conformance", "pct": 25, "step": 1, "total_steps": 4},
	} {
		var result map[string]any
		if _, data := input["schema"]; data {
			result, err = svc.Task.EmitData(context.Background(), input)
		} else {
			result, err = svc.Task.Progress(context.Background(), input)
		}
		require.NoError(t, err)
		var raw string
		require.NoError(t, store.DB().QueryRow(`SELECT envelope FROM event_outbox WHERE event_id=?`, result["event_id"]).Scan(&raw))
		var event runtimeevents.Event
		require.NoError(t, json.Unmarshal([]byte(raw), &event))
		entry, ok := catalog.Lookup(string(event.Kind))
		require.True(t, ok)
		require.Equal(t, entry.Category, event.Class.Category)
		require.Equal(t, entry.PayloadSchema, event.Class.PayloadSchema)
		schemaRaw, err := catalog.PayloadSchema(entry.PayloadSchema)
		require.NoError(t, err)
		var schema jsonschema.Schema
		require.NoError(t, json.Unmarshal(schemaRaw, &schema))
		resolved, err := schema.Resolve(nil)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(event.Payload, &payload))
		require.NoError(t, resolved.Validate(payload))
		if _, reference := input["artifact_id"]; reference {
			require.Equal(t, fmt.Sprintf("msg://artifact/torque/%d", artifact.ID), payload["ref"])
		}
	}
}

func TestSelfReportDefaultLevelIdempotency(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "default level", ProgressDetail: "step"})
	require.NoError(t, err)
	in := map[string]any{"task_id": task.ID, "label": "ready", "idempotency_key": "default"}
	first, err := svc.Task.Progress(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, true, first["emitted"])
	require.NotContains(t, in, "level", "validation must not mutate shared caller input")
	in["level"] = "step"
	out, err := svc.Task.Progress(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, "duplicate", out["suppressed_reason"])
	require.Equal(t, first["event_id"], out["event_id"])
	in["level"] = "milestone"
	_, err = svc.Task.Progress(context.Background(), in)
	assertReportCode(t, err, "idempotency_conflict")
}

func TestSelfReportInlineHTMLByteLimit(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "HTML data"})
	require.NoError(t, err)
	for _, size := range []int{16384, 16385} {
		_, err := svc.Task.EmitData(context.Background(), map[string]any{"task_id": task.ID, "schema": "html@1", "data": map[string]any{"x": strings.Repeat("<", size-8)}})
		if size == 16384 {
			require.NoError(t, err)
		} else {
			assertReportCode(t, err, "data_too_large")
		}
	}
}

func TestSelfReportMissingBoundSession(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "missing session", ProgressDetail: "step"})
	require.NoError(t, err)
	ctx := sqlstore.ContextWithReportSession(context.Background(), "SES-removed")
	_, err = svc.Task.Progress(ctx, map[string]any{"task_id": task.ID, "label": "ready"})
	assertReportCode(t, err, "not_found")
	var n int
	require.NoError(t, store.DB().QueryRow(`SELECT COUNT(*) FROM event_outbox WHERE task_id=?`, task.ID).Scan(&n))
	require.Zero(t, n)
}

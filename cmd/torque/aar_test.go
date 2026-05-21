package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/aar"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"

	_ "modernc.org/sqlite"
)

// newCLITestStore opens an in-memory sqlite store, runs migrations, and seeds
// a project + task so foreign-key constraints on artifacts pass. Returns the
// store + a cleanup func + the seeded task id.
func newCLITestStore(t *testing.T) (*sqlstore.Store, func(), string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	taskID, err := store.NextTaskID()
	require.NoError(t, err)
	task := &sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "cli-aar-target",
		Description:  "",
		Status:       "todo",
		Manual:       true,
		Kind:         "agent",
		AgentProfile: "implementer",
	}
	require.NoError(t, store.CreateTask(task))
	cleanup := func() {
		store.Close()
		db.Close()
	}
	return store, cleanup, task.ID
}

// seedAAR inserts an AAR artifact row mirroring what the loopback handler
// produces. When runIDSeed > 0 it also creates a runs row first so the
// artifact's run_id FK resolves. Returns the inserted artifact ID.
func seedAAR(t *testing.T, store *sqlstore.Store, taskID string, runIDSeed int64, outcome aar.Outcome, errs int) int64 {
	t.Helper()
	var runID int64
	if runIDSeed > 0 {
		var err error
		runID, err = store.CreateRun(&sqlstore.RunRecord{TaskID: taskID, Executor: "cli"})
		require.NoError(t, err)
	}
	id := aar.Identity{TaskID: taskID, RunID: runID, AgentProfile: "implementer-long"}
	ref := aar.Reflection{
		Summary: "seeded for test",
		Outcome: outcome,
		Errors:  make([]string, errs),
	}
	for i := range ref.Errors {
		ref.Errors[i] = "err"
	}
	body := aar.Render(id, ref)
	meta := map[string]any{
		"schema":               aar.Schema,
		"outcome":              string(outcome),
		"reflection_populated": 1,
		"errors_count":         errs,
	}
	mb, _ := json.Marshal(meta)
	rec := &sqlstore.ArtifactRecord{
		TaskID:   taskID,
		Type:     aar.ArtifactType,
		Content:  body,
		Metadata: sql.NullString{String: string(mb), Valid: true},
	}
	if runID != 0 {
		rec.RunID = sql.NullInt64{Int64: runID, Valid: true}
	}
	require.NoError(t, store.CreateArtifact(rec))
	return rec.ID
}

// seedNonAAR drops a non-AAR artifact in to verify the listing filter
// excludes other artifact types.
func seedNonAAR(t *testing.T, store *sqlstore.Store, taskID string) {
	t.Helper()
	rec := &sqlstore.ArtifactRecord{
		TaskID:  taskID,
		Type:    "file",
		Content: "irrelevant",
	}
	require.NoError(t, store.CreateArtifact(rec))
}

func TestAARList_FiltersToAARTypeOnly(t *testing.T) {
	store, cleanup, taskID := newCLITestStore(t)
	defer cleanup()

	successID := seedAAR(t, store, taskID, 1, aar.OutcomeSuccess, 0)
	partialID := seedAAR(t, store, taskID, 2, aar.OutcomePartial, 3)
	seedNonAAR(t, store, taskID)

	rows, err := listAARs(store, listFilter{All: true})
	require.NoError(t, err)
	require.Len(t, rows, 2, "non-AAR artifacts must be excluded")

	ids := []int64{rows[0].ArtifactID, rows[1].ArtifactID}
	assert.Contains(t, ids, successID)
	assert.Contains(t, ids, partialID)
}

func TestAARList_OutcomeFilter(t *testing.T) {
	store, cleanup, taskID := newCLITestStore(t)
	defer cleanup()

	seedAAR(t, store, taskID, 1, aar.OutcomeSuccess, 0)
	partialID := seedAAR(t, store, taskID, 2, aar.OutcomePartial, 3)
	seedAAR(t, store, taskID, 3, aar.OutcomeBlocked, 1)

	rows, err := listAARs(store, listFilter{Outcome: "partial", All: true})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, partialID, rows[0].ArtifactID)
	assert.Equal(t, "partial", rows[0].Outcome)
}

func TestAARList_TaskFilter(t *testing.T) {
	store, cleanup, taskA := newCLITestStore(t)
	defer cleanup()

	// Seed a second task in the same store.
	bID, err := store.NextTaskID()
	require.NoError(t, err)
	taskB := &sqlstore.TaskRecord{
		ID: bID, Title: "second-task", Status: "todo", Manual: true, Kind: "agent", AgentProfile: "implementer",
	}
	require.NoError(t, store.CreateTask(taskB))

	aIDA := seedAAR(t, store, taskA, 11, aar.OutcomeSuccess, 0)
	seedAAR(t, store, taskB.ID, 22, aar.OutcomeSuccess, 0)

	rows, err := listAARs(store, listFilter{TaskID: taskA, All: true})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, aIDA, rows[0].ArtifactID)
}

func TestAARList_LimitTrumpsAll(t *testing.T) {
	store, cleanup, taskID := newCLITestStore(t)
	defer cleanup()

	for i := 1; i <= 5; i++ {
		seedAAR(t, store, taskID, int64(i), aar.OutcomeSuccess, 0)
	}

	limited, err := listAARs(store, listFilter{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, limited, 3)

	all, err := listAARs(store, listFilter{All: true})
	require.NoError(t, err)
	assert.Len(t, all, 5)
}

func TestAARExport_WritesMarkdownFiles(t *testing.T) {
	store, cleanup, taskID := newCLITestStore(t)
	defer cleanup()

	seedAAR(t, store, taskID, 7, aar.OutcomeSuccess, 0)
	seedAAR(t, store, taskID, 0, aar.OutcomePartial, 1) // no run_id → fallback name

	dir := t.TempDir()
	rows, err := listAARs(store, listFilter{All: true})
	require.NoError(t, err)
	for _, r := range rows {
		path := filepath.Join(dir, exportFilename(r))
		require.NoError(t, os.WriteFile(path, []byte(r.Content), 0o644))
	}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	names := []string{entries[0].Name(), entries[1].Name()}
	hasRunNamed := false
	hasFallback := false
	for _, n := range names {
		if strings.Contains(n, "-run-") {
			hasRunNamed = true
		}
		if strings.HasSuffix(n, ".md") && !strings.Contains(n, "-run-") {
			hasFallback = true
		}
	}
	assert.True(t, hasRunNamed, "expected a run-id-named file; got %v", names)
	assert.True(t, hasFallback, "expected a fallback-named file for the no-run-id row; got %v", names)
}

func TestAARTable_RenderHeadersAndRows(t *testing.T) {
	now := time.Now().UTC()
	rows := []aarRow{
		{
			ArtifactID: 1, TaskID: "CW-X", RunID: 42, Outcome: "success",
			Populated: 4, ErrorsHit: 0, CreatedAt: now,
		},
	}
	var buf bytes.Buffer
	renderAARTable(&buf, rows)
	out := buf.String()
	assert.Contains(t, out, "ARTIFACT")
	assert.Contains(t, out, "OUTCOME")
	assert.Contains(t, out, "CW-X")
	assert.Contains(t, out, "42")
	assert.Contains(t, out, "success")
}

func TestExportFilename_RunIDAndFallback(t *testing.T) {
	assert.Equal(t, "CW-X-run-7.md", exportFilename(aarRow{TaskID: "CW-X", RunID: 7, ArtifactID: 99}))
	assert.Equal(t, "CW-X-99.md", exportFilename(aarRow{TaskID: "CW-X", ArtifactID: 99}))
}

func TestParseSince_RFC3339AndDuration(t *testing.T) {
	rfc, err := parseSince("2026-05-15T00:00:00Z")
	require.NoError(t, err)
	want, _ := time.Parse(time.RFC3339, "2026-05-15T00:00:00Z")
	assert.True(t, rfc.Equal(want))

	dur, err := parseSince("24h")
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(-24*time.Hour), dur, time.Minute)

	days, err := parseSince("7d")
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(-7*24*time.Hour), days, time.Minute)

	_, err = parseSince("garbage")
	require.Error(t, err)
}

func TestAARList_SinceCutsOff(t *testing.T) {
	store, cleanup, taskID := newCLITestStore(t)
	defer cleanup()

	seedAAR(t, store, taskID, 1, aar.OutcomeSuccess, 0)

	future := time.Now().Add(time.Hour).UTC()
	rows, err := listAARs(store, listFilter{Since: future, All: true})
	require.NoError(t, err)
	assert.Empty(t, rows, "no rows newer than 1h from now")
}

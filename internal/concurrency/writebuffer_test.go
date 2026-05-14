package concurrency_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	qsqlite "github.com/hollis-labs/go-queue/driver/sqlite"
	"github.com/hollis-labs/torque/internal/concurrency"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupWriteBufferDeps(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()

	// Main database (torque.db equivalent)
	mainDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	_, err = mainDB.Exec("PRAGMA journal_mode=WAL")
	require.NoError(t, err)
	_, err = mainDB.Exec("PRAGMA busy_timeout=5000")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(mainDB))
	t.Cleanup(func() { mainDB.Close() })

	// Queue database (queue.db equivalent)
	queueDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	_, err = queueDB.Exec("PRAGMA journal_mode=WAL")
	require.NoError(t, err)
	t.Cleanup(func() { queueDB.Close() })

	return mainDB, queueDB
}

func TestWriteBufferPushAndDrain(t *testing.T) {
	mainDB, queueDB := setupWriteBufferDeps(t)

	queueDriver, err := qsqlite.New(queueDB, qsqlite.Opts{
		Table:       "hot_jobs",
		FailedTable: "hot_failed_jobs",
	})
	require.NoError(t, err)

	ws := concurrency.NewWriteSerializer(mainDB, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ws.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	cfg := concurrency.DefaultDrainConfig()
	cfg.DrainInterval = 50 * time.Millisecond // Fast drain for tests
	cfg.BatchSize = 10

	wb := concurrency.NewWriteBuffer(queueDriver, ws, cfg)

	// Push 5 events
	for i := 0; i < 5; i++ {
		err := wb.Push(ctx, concurrency.BufferedEvent{
			RunID:     1,
			TaskID:    "CW-20260407-0001",
			Type:      concurrency.EventTypeProgress,
			Payload:   `{"progress": 0.5}`,
			CreatedAt: time.Now(),
		})
		require.NoError(t, err)
	}

	// Start drain and wait for it to process
	go wb.StartDrain(ctx)
	time.Sleep(200 * time.Millisecond)

	// Verify events landed in main database
	var count int
	err = mainDB.QueryRow("SELECT COUNT(*) FROM run_events WHERE task_id = ?", "CW-20260407-0001").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestWriteBufferBatching(t *testing.T) {
	mainDB, queueDB := setupWriteBufferDeps(t)

	queueDriver, err := qsqlite.New(queueDB, qsqlite.Opts{
		Table:       "hot_jobs",
		FailedTable: "hot_failed_jobs",
	})
	require.NoError(t, err)

	ws := concurrency.NewWriteSerializer(mainDB, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ws.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	cfg := concurrency.DefaultDrainConfig()
	cfg.DrainInterval = 50 * time.Millisecond
	cfg.BatchSize = 3 // Small batch to verify batching behavior

	wb := concurrency.NewWriteBuffer(queueDriver, ws, cfg)

	// Push 7 events — should result in 3 batches (3, 3, 1)
	for i := 0; i < 7; i++ {
		err := wb.Push(ctx, concurrency.BufferedEvent{
			RunID:     1,
			TaskID:    "CW-20260407-0001",
			Type:      concurrency.EventTypeTokens,
			Payload:   `{"prompt": 100, "completion": 50}`,
			CreatedAt: time.Now(),
		})
		require.NoError(t, err)
	}

	go wb.StartDrain(ctx)
	time.Sleep(500 * time.Millisecond)

	var count int
	err = mainDB.QueryRow("SELECT COUNT(*) FROM run_events").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 7, count)

	stats := wb.Stats()
	assert.Equal(t, int64(7), stats.EventsDrained)
	assert.GreaterOrEqual(t, stats.BatchesDrained, int64(3))
}

func TestWriteBufferStats(t *testing.T) {
	_, queueDB := setupWriteBufferDeps(t)

	queueDriver, err := qsqlite.New(queueDB, qsqlite.Opts{
		Table:       "hot_jobs",
		FailedTable: "hot_failed_jobs",
	})
	require.NoError(t, err)

	db := openTestDB(t)
	ws := concurrency.NewWriteSerializer(db, 64)

	cfg := concurrency.DefaultDrainConfig()
	wb := concurrency.NewWriteBuffer(queueDriver, ws, cfg)

	stats := wb.Stats()
	assert.Equal(t, int64(0), stats.BatchesDrained)
	assert.Equal(t, int64(0), stats.EventsDrained)
}

func TestWriteBufferHeartbeatEvent(t *testing.T) {
	mainDB, queueDB := setupWriteBufferDeps(t)

	queueDriver, err := qsqlite.New(queueDB, qsqlite.Opts{
		Table:       "hot_jobs",
		FailedTable: "hot_failed_jobs",
	})
	require.NoError(t, err)

	ws := concurrency.NewWriteSerializer(mainDB, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ws.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	cfg := concurrency.DefaultDrainConfig()
	cfg.DrainInterval = 50 * time.Millisecond
	wb := concurrency.NewWriteBuffer(queueDriver, ws, cfg)

	heartbeat := concurrency.BufferedEvent{
		RunID:     42,
		TaskID:    "CW-20260407-0099",
		Type:      concurrency.EventTypeHeartbeat,
		Payload:   `{"worker_id": "w1"}`,
		CreatedAt: time.Now(),
	}
	require.NoError(t, wb.Push(ctx, heartbeat))

	go wb.StartDrain(ctx)
	time.Sleep(200 * time.Millisecond)

	var eventType string
	err = mainDB.QueryRow("SELECT type FROM run_events WHERE run_id = 42").Scan(&eventType)
	require.NoError(t, err)
	assert.Equal(t, concurrency.EventTypeHeartbeat, eventType)
}

func TestWriteBufferStopDrainsRemaining(t *testing.T) {
	mainDB, queueDB := setupWriteBufferDeps(t)

	queueDriver, err := qsqlite.New(queueDB, qsqlite.Opts{
		Table:       "hot_jobs",
		FailedTable: "hot_failed_jobs",
	})
	require.NoError(t, err)

	ws := concurrency.NewWriteSerializer(mainDB, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ws.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	cfg := concurrency.DefaultDrainConfig()
	cfg.DrainInterval = 5 * time.Second // Very slow — won't auto-drain
	cfg.BatchSize = 100
	wb := concurrency.NewWriteBuffer(queueDriver, ws, cfg)

	// Push events
	for i := 0; i < 3; i++ {
		require.NoError(t, wb.Push(ctx, concurrency.BufferedEvent{
			RunID:     1,
			TaskID:    "CW-20260407-0001",
			Type:      concurrency.EventTypeLog,
			Payload:   `{"line": "test"}`,
			CreatedAt: time.Now(),
		}))
	}

	// Start drain, then immediately stop — should drain remaining
	drainCtx, drainCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		wb.StartDrain(drainCtx)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)

	wb.Stop()
	drainCancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not stop in time")
	}

	// All events should be drained
	var count int
	err = mainDB.QueryRow("SELECT COUNT(*) FROM run_events").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

// Ensure the encoding/json import is used (it's used in writebuffer.go but linters may flag the test).
var _ = json.Marshal

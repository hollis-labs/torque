package concurrency_test

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	_, err = db.Exec("PRAGMA journal_mode=WAL")
	require.NoError(t, err)
	_, err = db.Exec("PRAGMA busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestWriteSerializerSingleWrite(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, val TEXT)")
	require.NoError(t, err)

	ws := concurrency.NewWriteSerializer(db, 64)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ws.Start(ctx)

	// Allow serializer goroutine to start
	time.Sleep(10 * time.Millisecond)

	err = ws.Submit(ctx, func(db *sql.DB) error {
		_, err := db.Exec("INSERT INTO test (val) VALUES (?)", "hello")
		return err
	})
	require.NoError(t, err)

	var val string
	err = db.QueryRow("SELECT val FROM test WHERE id = 1").Scan(&val)
	require.NoError(t, err)
	assert.Equal(t, "hello", val)
}

func TestWriteSerializerConcurrentSubmits(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec("CREATE TABLE counter (id INTEGER PRIMARY KEY, n INTEGER NOT NULL DEFAULT 0)")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO counter (id, n) VALUES (1, 0)")
	require.NoError(t, err)

	ws := concurrency.NewWriteSerializer(db, 64)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ws.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	// 100 concurrent goroutines each incrementing the counter by 1.
	// If writes are truly serialized, final value must be exactly 100.
	const numWriters = 100
	var wg sync.WaitGroup
	wg.Add(numWriters)

	for i := 0; i < numWriters; i++ {
		go func() {
			defer wg.Done()
			err := ws.Submit(ctx, func(db *sql.DB) error {
				_, err := db.Exec("UPDATE counter SET n = n + 1 WHERE id = 1")
				return err
			})
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	var n int
	err = db.QueryRow("SELECT n FROM counter WHERE id = 1").Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, numWriters, n)
}

func TestWriteSerializerErrorPropagation(t *testing.T) {
	db := openTestDB(t)
	ws := concurrency.NewWriteSerializer(db, 64)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ws.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	err := ws.Submit(ctx, func(db *sql.DB) error {
		_, err := db.Exec("INSERT INTO nonexistent_table (x) VALUES (1)")
		return err
	})
	assert.Error(t, err)
}

func TestWriteSerializerContextCancellation(t *testing.T) {
	db := openTestDB(t)
	ws := concurrency.NewWriteSerializer(db, 64)

	ctx, cancel := context.WithCancel(context.Background())
	go ws.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	// Cancel context, then try to submit
	cancel()
	time.Sleep(50 * time.Millisecond) // give Start time to exit and close stopped

	submitCtx, submitCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer submitCancel()

	err := ws.Submit(submitCtx, func(db *sql.DB) error {
		return nil
	})
	assert.Error(t, err)
}

func TestWriteSerializerOrdering(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec("CREATE TABLE ordered (seq INTEGER)")
	require.NoError(t, err)

	ws := concurrency.NewWriteSerializer(db, 64)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ws.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	// Submit writes sequentially and verify ordering is preserved.
	var order atomic.Int64
	const numOps = 50

	for i := 0; i < numOps; i++ {
		seq := i
		err := ws.Submit(ctx, func(db *sql.DB) error {
			// Verify this runs in order — each op sees the previous order value.
			got := order.Add(1)
			if int(got) != seq+1 {
				t.Errorf("expected order %d, got %d", seq+1, got)
			}
			_, err := db.Exec("INSERT INTO ordered (seq) VALUES (?)", seq)
			return err
		})
		require.NoError(t, err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM ordered").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, numOps, count)
}

func TestWriteSerializerStop(t *testing.T) {
	db := openTestDB(t)
	ws := concurrency.NewWriteSerializer(db, 64)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ws.Start(ctx)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	ws.Stop()
	cancel()

	select {
	case <-done:
		// Good — Start returned
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

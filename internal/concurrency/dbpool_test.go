package concurrency_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestDBPoolOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	queuePath := filepath.Join(dir, "queue.db")

	cfg := concurrency.DBPoolConfig{
		DBPath:           dbPath,
		QueueDBPath:      queuePath,
		MaxReadConns:     2,
		BusyTimeoutMs:    5000,
		WriteChannelSize: 64,
	}

	pool, err := concurrency.NewDBPool(cfg)
	require.NoError(t, err)
	defer pool.Close()

	assert.NotNil(t, pool.WriteDB())
	assert.NotNil(t, pool.ReadDB())
	assert.NotNil(t, pool.QueueDB())
}

func TestDBPoolReadConcurrency(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	queuePath := filepath.Join(dir, "queue.db")

	cfg := concurrency.DBPoolConfig{
		DBPath:           dbPath,
		QueueDBPath:      queuePath,
		MaxReadConns:     4,
		BusyTimeoutMs:    5000,
		WriteChannelSize: 64,
	}

	pool, err := concurrency.NewDBPool(cfg)
	require.NoError(t, err)
	defer pool.Close()

	// Create a table via the write connection
	_, err = pool.WriteDB().Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, val TEXT)")
	require.NoError(t, err)

	_, err = pool.WriteDB().Exec("INSERT INTO test (val) VALUES ('hello')")
	require.NoError(t, err)

	// Read from the read connection concurrently
	done := make(chan bool, 4)
	for i := 0; i < 4; i++ {
		go func() {
			var val string
			err := pool.ReadDB().QueryRow("SELECT val FROM test WHERE id = 1").Scan(&val)
			done <- (err == nil && val == "hello")
		}()
	}

	for i := 0; i < 4; i++ {
		select {
		case ok := <-done:
			assert.True(t, ok, "concurrent read failed")
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent read timed out")
		}
	}
}

func TestDBPoolWALMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	queuePath := filepath.Join(dir, "queue.db")

	cfg := concurrency.DBPoolConfig{
		DBPath:           dbPath,
		QueueDBPath:      queuePath,
		MaxReadConns:     2,
		BusyTimeoutMs:    5000,
		WriteChannelSize: 64,
	}

	pool, err := concurrency.NewDBPool(cfg)
	require.NoError(t, err)
	defer pool.Close()

	// Verify WAL mode on write connection
	var journalMode string
	err = pool.WriteDB().QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	require.NoError(t, err)
	assert.Equal(t, "wal", journalMode)

	// Verify WAL mode on read connection
	err = pool.ReadDB().QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	require.NoError(t, err)
	assert.Equal(t, "wal", journalMode)

	// Verify WAL mode on queue connection
	err = pool.QueueDB().QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	require.NoError(t, err)
	assert.Equal(t, "wal", journalMode)
}

func TestDBPoolBusyTimeout(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	queuePath := filepath.Join(dir, "queue.db")

	cfg := concurrency.DBPoolConfig{
		DBPath:           dbPath,
		QueueDBPath:      queuePath,
		MaxReadConns:     2,
		BusyTimeoutMs:    3000,
		WriteChannelSize: 64,
	}

	pool, err := concurrency.NewDBPool(cfg)
	require.NoError(t, err)
	defer pool.Close()

	var timeout int
	err = pool.WriteDB().QueryRow("PRAGMA busy_timeout").Scan(&timeout)
	require.NoError(t, err)
	assert.Equal(t, 3000, timeout)
}

func TestDBPoolSeparateFiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "main.db")
	queuePath := filepath.Join(dir, "queue.db")

	cfg := concurrency.DBPoolConfig{
		DBPath:           dbPath,
		QueueDBPath:      queuePath,
		MaxReadConns:     2,
		BusyTimeoutMs:    5000,
		WriteChannelSize: 64,
	}

	pool, err := concurrency.NewDBPool(cfg)
	require.NoError(t, err)
	defer pool.Close()

	// Verify both files were created
	_, err = os.Stat(dbPath)
	require.NoError(t, err)
	_, err = os.Stat(queuePath)
	require.NoError(t, err)
}

func TestDBPoolWriteSerializer(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	queuePath := filepath.Join(dir, "queue.db")

	cfg := concurrency.DBPoolConfig{
		DBPath:           dbPath,
		QueueDBPath:      queuePath,
		MaxReadConns:     2,
		BusyTimeoutMs:    5000,
		WriteChannelSize: 64,
	}

	pool, err := concurrency.NewDBPool(cfg)
	require.NoError(t, err)
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pool.Serializer().Start(ctx)
	time.Sleep(10 * time.Millisecond)

	// Create table and insert through serializer
	err = pool.Serializer().Submit(ctx, func(db *sql.DB) error {
		_, err := db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, val TEXT)")
		return err
	})
	require.NoError(t, err)

	err = pool.Serializer().Submit(ctx, func(db *sql.DB) error {
		_, err := db.Exec("INSERT INTO test (val) VALUES ('serialized')")
		return err
	})
	require.NoError(t, err)

	// Read from the read pool
	var val string
	err = pool.ReadDB().QueryRow("SELECT val FROM test WHERE id = 1").Scan(&val)
	require.NoError(t, err)
	assert.Equal(t, "serialized", val)
}

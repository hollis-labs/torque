package concurrency

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	_ "modernc.org/sqlite"
)

// DBPool manages separate read and write connections to the main database
// (torque.db) and a separate connection to the queue database (queue.db).
// WAL mode enables concurrent readers while the write serializer ensures
// a single writer to the main database.
type DBPool struct {
	writeDB    *sql.DB
	readDB     *sql.DB
	queueDB    *sql.DB
	serializer WriteSerializer
	cfg        DBPoolConfig
}

// NewDBPool opens and configures all database connections.
// - writeDB: single connection (max_open=1) for the write serializer
// - readDB: multiple connections (max_open=MaxReadConns) for concurrent reads
// - queueDB: separate SQLite file for go-queue hot tier
// All connections have WAL mode, busy_timeout, and foreign keys enabled
// via sqlitekit DSN parameters. The writer pool (writeDB/queueDB) carries
// _txlock=immediate so explicit BEGIN IMMEDIATE transactions acquire the
// writer lock at begin time.
func NewDBPool(ctx context.Context, cfg DBPoolConfig) (*DBPool, error) {
	if cfg.MaxReadConns <= 0 {
		cfg.MaxReadConns = 4
	}
	if cfg.WriteChannelSize <= 0 {
		cfg.WriteChannelSize = 256
	}

	// Open write connection — single-connection writer pool with
	// _txlock=immediate baked into the DSN.
	writeDB, err := sqlitekit.OpenWriter(ctx, cfg.DBPath, sqlitekit.OpenOptions{})
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}

	// Open read connection pool — bounded pool for concurrent reads.
	readDB, err := sqlitekit.OpenReader(ctx, cfg.DBPath, sqlitekit.OpenOptions{MaxOpenConns: cfg.MaxReadConns})
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}

	// Open queue database — separate file, single-conn pool with writer
	// options (so explicit BEGIN IMMEDIATE works).
	queueDB, err := sqlitekit.OpenSingle(ctx, cfg.QueueDBPath, sqlitekit.OpenOptions{
		Options:         sqlitekit.WriterOptions(),
		CreateParentDir: true,
	})
	if err != nil {
		writeDB.Close()
		readDB.Close()
		return nil, fmt.Errorf("open queue db: %w", err)
	}

	serializer := NewWriteSerializer(writeDB, cfg.WriteChannelSize)

	return &DBPool{
		writeDB:    writeDB,
		readDB:     readDB,
		queueDB:    queueDB,
		serializer: serializer,
		cfg:        cfg,
	}, nil
}

// WriteDB returns the single-connection write database handle.
// This should only be used by the write serializer. For direct writes
// during initialization (migrations, etc.), use this before starting
// the serializer.
func (p *DBPool) WriteDB() *sql.DB { return p.writeDB }

// ReadDB returns the multi-connection read database handle.
// Safe for concurrent use — WAL mode allows multiple readers.
func (p *DBPool) ReadDB() *sql.DB { return p.readDB }

// QueueDB returns the queue database handle (queue.db).
// Used by go-queue's SQLite driver for hot-tier writes.
func (p *DBPool) QueueDB() *sql.DB { return p.queueDB }

// Serializer returns the write serializer for submitting writes
// to the main database.
func (p *DBPool) Serializer() WriteSerializer { return p.serializer }

// Close closes all database connections. Call after stopping the
// write serializer and drain goroutines.
func (p *DBPool) Close() error {
	var firstErr error
	if err := p.writeDB.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := p.readDB.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := p.queueDB.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

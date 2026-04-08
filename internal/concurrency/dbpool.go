package concurrency

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DBPool manages separate read and write connections to the main database
// (clockwork.db) and a separate connection to the queue database (queue.db).
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
// All connections have WAL mode, busy_timeout, and foreign keys enabled.
func NewDBPool(cfg DBPoolConfig) (*DBPool, error) {
	if cfg.MaxReadConns <= 0 {
		cfg.MaxReadConns = 4
	}
	if cfg.BusyTimeoutMs <= 0 {
		cfg.BusyTimeoutMs = 5000
	}
	if cfg.WriteChannelSize <= 0 {
		cfg.WriteChannelSize = 256
	}

	// Open write connection — single connection, no pooling.
	writeDB, err := openSQLite(cfg.DBPath, cfg.BusyTimeoutMs, 1)
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}

	// Open read connection pool — multiple connections for concurrent reads.
	readDB, err := openSQLite(cfg.DBPath, cfg.BusyTimeoutMs, cfg.MaxReadConns)
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}

	// Open queue database — separate file for hot writes.
	queueDB, err := openSQLite(cfg.QueueDBPath, cfg.BusyTimeoutMs, 1)
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

// openSQLite opens a SQLite connection with WAL mode, busy_timeout,
// foreign keys, and the specified max open connections.
func openSQLite(path string, busyTimeoutMs int, maxOpenConns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(maxOpenConns)

	// Enable WAL mode — allows concurrent reads while writing.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	// Set busy timeout — wait instead of returning SQLITE_BUSY immediately.
	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", busyTimeoutMs)); err != nil {
		db.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	// Enable foreign key enforcement.
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	// Verify connection is working.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return db, nil
}

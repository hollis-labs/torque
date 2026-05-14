package sqlitetest

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	_ "modernc.org/sqlite"
)

const defaultBusyTimeout = 5 * time.Second

type config struct {
	busyTimeout     time.Duration
	maxOpenConns    int
	setMaxOpenConns bool
}

type Option func(*config)

func newConfig(opts ...Option) config {
	cfg := config{
		busyTimeout: defaultBusyTimeout,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithBusyTimeout overrides the per-connection SQLite busy_timeout used by the
// temp-file-backed test database.
func WithBusyTimeout(ms int) Option {
	return func(cfg *config) {
		if ms <= 0 {
			panic("sqlitetest.WithBusyTimeout requires ms > 0")
		}
		cfg.busyTimeout = time.Duration(ms) * time.Millisecond
	}
}

// WithMaxOpenConns opts a test into an explicit pool size. Omit this option to
// keep the default Go sql.DB behavior so pooled SQLite writes are exercised.
func WithMaxOpenConns(n int) Option {
	return func(cfg *config) {
		if n <= 0 {
			panic("sqlitetest.WithMaxOpenConns requires n > 0")
		}
		cfg.maxOpenConns = n
		cfg.setMaxOpenConns = true
	}
}

func OpenDB(t *testing.T, opts ...Option) *sql.DB {
	t.Helper()

	cfg := newConfig(opts...)
	db := openDB(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func OpenStore(t *testing.T, opts ...Option) *sqlstore.Store {
	t.Helper()

	cfg := newConfig(opts...)
	db := openDB(t, cfg)
	requireMigrations(t, db)

	store, err := sqlstore.New(db, "sqlite")
	if err != nil {
		_ = db.Close()
		t.Fatalf("create sqlite test store: %v", err)
	}
	// sqlitekit applies busy_timeout via the DSN to every connection in both
	// the writer and reader pools, so no explicit PRAGMA exec is needed.
	if cfg.setMaxOpenConns {
		store.ReadDB().SetMaxOpenConns(cfg.maxOpenConns)
		store.ReadDB().SetMaxIdleConns(cfg.maxOpenConns)
	}

	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openDB(t *testing.T, cfg config) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), sanitizeName(t.Name())+".db")
	writerOpts := sqlitekit.WriterOptions()
	writerOpts.BusyTimeout = cfg.busyTimeout

	db, err := sqlitekit.OpenWriter(context.Background(), dbPath, sqlitekit.OpenOptions{Options: writerOpts})
	if err != nil {
		t.Fatalf("open sqlite test db: %v", err)
	}
	return db
}

func requireMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := migrations.Run(db); err != nil {
		_ = db.Close()
		t.Fatalf("run sqlite test migrations: %v", err)
	}
}

func sanitizeName(name string) string {
	replacer := strings.NewReplacer("/", "-", " ", "-", ":", "-", "\\", "-")
	return replacer.Replace(name)
}

package sqlitetest

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	_ "modernc.org/sqlite"
)

const defaultBusyTimeoutMs = 5000

type config struct {
	busyTimeoutMs   int
	maxOpenConns    int
	setMaxOpenConns bool
}

type Option func(*config)

// WithBusyTimeout overrides the per-connection SQLite busy_timeout used by the
// temp-file-backed test database.
func WithBusyTimeout(ms int) Option {
	return func(cfg *config) {
		if ms <= 0 {
			panic("sqlitetest.WithBusyTimeout requires ms > 0")
		}
		cfg.busyTimeoutMs = ms
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

	db := openDB(t, opts...)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func OpenStore(t *testing.T, opts ...Option) *sqlstore.Store {
	t.Helper()

	db := openDB(t, opts...)
	requireMigrations(t, db)

	store, err := sqlstore.New(db, "sqlite")
	if err != nil {
		_ = db.Close()
		t.Fatalf("create sqlite test store: %v", err)
	}

	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openDB(t *testing.T, opts ...Option) *sql.DB {
	t.Helper()

	cfg := config{
		busyTimeoutMs: defaultBusyTimeoutMs,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	dbPath := filepath.Join(t.TempDir(), sanitizeName(t.Name())+".db")
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)",
		dbPath,
		cfg.busyTimeoutMs,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite test db: %v", err)
	}
	if cfg.setMaxOpenConns {
		db.SetMaxOpenConns(cfg.maxOpenConns)
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

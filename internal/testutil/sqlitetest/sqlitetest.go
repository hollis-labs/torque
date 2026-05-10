package sqlitetest

import (
	"database/sql"
	"fmt"
	"net/url"
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

func newConfig(opts ...Option) config {
	cfg := config{
		busyTimeoutMs: defaultBusyTimeoutMs,
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
	requireBusyTimeout(t, store.DB(), cfg.busyTimeoutMs)
	if store.ReadDB() != store.DB() {
		requireBusyTimeout(t, store.ReadDB(), cfg.busyTimeoutMs)
	}
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
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", cfg.busyTimeoutMs))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "temp_store(memory)")
	q.Add("_pragma", "mmap_size(30000000000)")
	q.Add("_pragma", "journal_size_limit(67108864)")
	q.Add("_pragma", "cache_size(-64000)")
	q.Set("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: q.Encode()}).String()

	db, err := sql.Open("sqlite", dsn)
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

func requireBusyTimeout(t *testing.T, db *sql.DB, ms int) {
	t.Helper()
	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", ms)); err != nil {
		_ = db.Close()
		t.Fatalf("set sqlite test busy_timeout: %v", err)
	}
}

func sanitizeName(name string) string {
	replacer := strings.NewReplacer("/", "-", " ", "-", ":", "-", "\\", "-")
	return replacer.Replace(name)
}

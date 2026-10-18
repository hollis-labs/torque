package sqlitetest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
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
	db := openDB(t, cfg, false)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func OpenStore(t *testing.T, opts ...Option) *sqlstore.Store {
	t.Helper()

	cfg := newConfig(opts...)
	db := openDB(t, cfg, true)
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

func openDB(t *testing.T, cfg config, migrated bool) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), sanitizeName(t.Name())+".db")
	if migrated {
		// Start from a copy of one migrated file: replaying every migration
		// costs ~2.2s a database under -race (0.07s without it). On any problem
		// the file stays absent and OpenStore's migrations.Run does the work.
		if img := migratedImage(); len(img) > 0 {
			_ = os.WriteFile(dbPath, img, 0o600)
		}
	}
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

var (
	imageOnce  sync.Once
	imageBytes []byte
)

// migratedImage returns the bytes of a closed, fully migrated empty database,
// built once per test binary.
func migratedImage() []byte {
	imageOnce.Do(func() {
		dir, err := os.MkdirTemp("", "torque-migrated-")
		if err != nil {
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		path := filepath.Join(dir, "template.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return
		}
		db.SetMaxOpenConns(1)
		runErr := migrations.Run(db)
		closeErr := db.Close()
		if runErr != nil || closeErr != nil {
			return
		}
		if b, err := os.ReadFile(path); err == nil { //nolint:gosec // G304: the file this function just created
			imageBytes = b
		}
	})
	return imageBytes
}

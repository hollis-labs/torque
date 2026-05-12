package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
)

type Store struct {
	db      *sql.DB
	read    *sql.DB
	owned   *sql.DB
	dialect Dialect

	// Task-transition hook fan-out (CW-20260418-0005). Added so the
	// scheduler can observe DB-driven transitions out of "doing" and
	// cancel the corresponding in-flight worker. See task_hooks.go.
	hooksMu         sync.RWMutex
	transitionHooks []TaskTransitionHook
}

// New constructs a Store on top of the provided *sql.DB. When the driver is
// sqlite and the input handle has a file path (i.e. not :memory:), New
// re-opens dedicated writer + reader pools via sqlitekit so writers serialize
// on a single connection (TxLock=immediate) while reads scale on a bounded
// pool. The original handle is retained as `owned` so Close honors the
// caller's expectation of ownership.
//
// For :memory: callers (test fixtures), the input handle is used as-is for
// both reads and writes, with FK enforcement enabled — these sites bypass the
// DSN path entirely.
func New(db *sql.DB, driver string) (*Store, error) {
	var d Dialect
	readDB := db
	ownedDB := db
	switch driver {
	case "sqlite", "sqlite3":
		d = sqliteDialect{}
		path, err := sqliteMainDBPath(db)
		if err != nil {
			return nil, err
		}
		if path != "" {
			ctx := context.Background()
			writerDB, err := sqlitekit.OpenWriter(ctx, path, sqlitekit.OpenOptions{
				Options: sqlitekit.WriterOptions(),
			})
			if err != nil {
				return nil, fmt.Errorf("open sqlite write pool: %w", err)
			}
			readerDB, err := sqlitekit.OpenReader(ctx, path, sqlitekit.OpenOptions{
				Options:      sqlitekit.ReaderOptions(),
				MaxOpenConns: sqliteReadMaxOpenConns(),
			})
			if err != nil {
				_ = writerDB.Close()
				return nil, fmt.Errorf("open sqlite read pool: %w", err)
			}
			db = writerDB
			readDB = readerDB
		} else {
			// :memory: (or empty path) — caller's handle is the writer and
			// reader. Force single-connection semantics, and turn on FKs since
			// the DSN path was bypassed.
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
				return nil, fmt.Errorf("enable foreign keys: %w", err)
			}
		}
	case "postgres", "pgx":
		d = postgresDialect{}
	default:
		return nil, fmt.Errorf("unsupported driver: %s", driver)
	}
	return &Store{db: db, read: readDB, owned: ownedDB, dialect: d}, nil
}

func (s *Store) DB() *sql.DB { return s.db }
func (s *Store) ReadDB() *sql.DB {
	if s.read != nil {
		return s.read
	}
	return s.db
}

func (s *Store) beginWriteTx() (*sql.Tx, error) { return s.db.Begin() }

func (s *Store) Close() error {
	var firstErr error
	if s.read != nil && s.read != s.db {
		if err := s.read.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := s.db.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if s.owned != nil && s.owned != s.db && s.owned != s.read {
		if err := s.owned.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// sqliteMainDBPath returns the on-disk file backing the "main" SQLite
// database for db, or "" when the database is :memory:. Used by New to
// decide whether to spin up dedicated writer/reader pools.
func sqliteMainDBPath(db *sql.DB) (string, error) {
	rows, err := db.Query(`PRAGMA database_list`)
	if err != nil {
		return "", fmt.Errorf("query sqlite database_list: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "", fmt.Errorf("scan sqlite database_list: %w", err)
		}
		if name != "main" {
			continue
		}
		if file == "" || file == ":memory:" {
			return "", nil
		}
		return file, nil
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate sqlite database_list: %w", err)
	}
	return "", nil
}

// sqliteReadMaxOpenConns honors CLOCKWORK_MAX_READ_CONNS when set to a
// positive int, falling back to sqlitekit's DefaultReadMaxOpenConns. The env
// override is preserved from the pre-migration code so operators can keep
// tuning the read pool size without code changes.
func sqliteReadMaxOpenConns() int {
	if raw := strings.TrimSpace(os.Getenv("CLOCKWORK_MAX_READ_CONNS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return sqlitekit.DefaultReadMaxOpenConns
}

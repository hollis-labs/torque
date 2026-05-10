package sqlstore

import (
	"database/sql"
	"fmt"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/appdb"
	"os"
	"strconv"
	"strings"
	"sync"
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
		busyTimeoutMs, err := sqliteBusyTimeout(db)
		if err != nil {
			return nil, err
		}
		if path != "" {
			writerDB, err := openSQLiteWritePool(path, busyTimeoutMs)
			if err != nil {
				return nil, err
			}
			readDB, err = openSQLiteReadPool(path, busyTimeoutMs)
			if err != nil {
				_ = writerDB.Close()
				return nil, err
			}
			db = writerDB
		} else {
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			if err := applySQLiteWritePragmas(db, busyTimeoutMs); err != nil {
				return nil, err
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

func applySQLiteWritePragmas(db *sql.DB, busyTimeoutMs int) error {
	return applySQLitePragmas(db, true, busyTimeoutMs)
}

func applySQLitePragmas(db *sql.DB, includeCacheSize bool, busyTimeoutMs int) error {
	if busyTimeoutMs <= 0 {
		busyTimeoutMs = appdb.DefaultSQLiteBusyTimeoutMs
	}
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		fmt.Sprintf("PRAGMA busy_timeout=%d", busyTimeoutMs),
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA temp_store=memory",
		"PRAGMA mmap_size=30000000000",
		"PRAGMA journal_size_limit=67108864",
	}
	if includeCacheSize {
		pragmas = append(pragmas, "PRAGMA cache_size=-64000")
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("apply %q: %w", pragma, err)
		}
	}
	return nil
}

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

func sqliteBusyTimeout(db *sql.DB) (int, error) {
	var ms int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&ms); err != nil {
		return 0, fmt.Errorf("query sqlite busy_timeout: %w", err)
	}
	if ms <= 0 {
		ms = appdb.DefaultSQLiteBusyTimeoutMs
	}
	return ms, nil
}

func openSQLiteWritePool(path string, busyTimeoutMs int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", appdb.SQLiteDSN(path, appdb.SQLiteDSNOptions{
		BusyTimeoutMs:    busyTimeoutMs,
		IncludeCacheSize: true,
		TxLock:           "immediate",
	}))
	if err != nil {
		return nil, fmt.Errorf("open sqlite write pool: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := applySQLiteWritePragmas(db, busyTimeoutMs); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite write pool: %w", err)
	}
	return db, nil
}

func openSQLiteReadPool(path string, busyTimeoutMs int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteReadDSN(path, busyTimeoutMs))
	if err != nil {
		return nil, fmt.Errorf("open sqlite read pool: %w", err)
	}
	maxOpen := sqliteReadMaxOpenConns()
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite read pool: %w", err)
	}
	return db, nil
}

func sqliteReadMaxOpenConns() int {
	if raw := strings.TrimSpace(os.Getenv("CLOCKWORK_MAX_READ_CONNS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return appdb.DefaultSQLiteMaxReadConns
}

func sqliteReadDSN(path string, busyTimeoutMs int) string {
	return appdb.SQLiteDSN(path, appdb.SQLiteDSNOptions{
		BusyTimeoutMs: busyTimeoutMs,
	})
}

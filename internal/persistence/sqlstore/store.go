package sqlstore

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

type Store struct {
	db      *sql.DB
	read    *sql.DB
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
	switch driver {
	case "sqlite", "sqlite3":
		d = sqliteDialect{}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		if err := applySQLiteWritePragmas(db); err != nil {
			return nil, err
		}

		path, err := sqliteMainDBPath(db)
		if err != nil {
			return nil, err
		}
		if path != "" {
			readDB, err = openSQLiteReadPool(path)
			if err != nil {
				return nil, err
			}
		}
	case "postgres", "pgx":
		d = postgresDialect{}
	default:
		return nil, fmt.Errorf("unsupported driver: %s", driver)
	}
	return &Store{db: db, read: readDB, dialect: d}, nil
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
	return firstErr
}

func applySQLiteWritePragmas(db *sql.DB) error {
	return applySQLitePragmas(db, true)
}

func applySQLitePragmas(db *sql.DB, includeCacheSize bool) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
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

func openSQLiteReadPool(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteReadDSN(path))
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
	n := runtime.NumCPU() * 2
	if n < 10 {
		return 10
	}
	return n
}

func sqliteReadDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "temp_store(memory)")
	q.Add("_pragma", "mmap_size(30000000000)")
	q.Add("_pragma", "journal_size_limit(67108864)")
	u := &url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	return u.String()
}

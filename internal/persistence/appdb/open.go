package appdb

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const (
	DefaultSQLiteBusyTimeoutMs = 5000
	DefaultSQLiteMaxReadConns  = 4
)

type SQLiteDSNOptions struct {
	BusyTimeoutMs    int
	IncludeCacheSize bool
	TxLock           string
}

func SQLiteDSN(path string, opts SQLiteDSNOptions) string {
	busyTimeoutMs := opts.BusyTimeoutMs
	if busyTimeoutMs <= 0 {
		busyTimeoutMs = DefaultSQLiteBusyTimeoutMs
	}

	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMs))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "temp_store(memory)")
	q.Add("_pragma", "mmap_size(30000000000)")
	q.Add("_pragma", "journal_size_limit(67108864)")
	if opts.IncludeCacheSize {
		q.Add("_pragma", "cache_size(-64000)")
	}
	if opts.TxLock != "" {
		q.Set("_txlock", opts.TxLock)
	}

	if filepath.IsAbs(path) {
		return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
	}

	// Relative SQLite paths must use the "file:foo.db" URI form. Encoding
	// them as "file://foo.db" makes the driver treat the path as an authority-
	// shaped URI and modernc/sqlite fails on first write against real DBs.
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + q.Encode()
}

func Open() (*sql.DB, string, error) {
	if dsn := os.Getenv("CLOCKWORK_POSTGRES_DSN"); dsn != "" {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, "", fmt.Errorf("open postgres: %w", err)
		}
		if err := db.Ping(); err != nil {
			db.Close()
			return nil, "", fmt.Errorf("ping postgres: %w", err)
		}
		return db, "postgres", nil
	}

	path := os.Getenv("CLOCKWORK_DB_PATH")
	if path == "" {
		path = "clockwork.db"
	}

	dsn := SQLiteDSN(path, SQLiteDSNOptions{
		BusyTimeoutMs:    DefaultSQLiteBusyTimeoutMs,
		IncludeCacheSize: true,
		TxLock:           "immediate",
	})
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, "", fmt.Errorf("open sqlite %s: %w", path, err)
	}
	return db, "sqlite", nil
}

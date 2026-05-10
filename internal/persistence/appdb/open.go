package appdb

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

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

	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "temp_store(memory)")
	q.Add("_pragma", "mmap_size(30000000000)")
	q.Add("_pragma", "journal_size_limit(67108864)")
	q.Add("_pragma", "cache_size(-64000)")
	q.Set("_txlock", "immediate")

	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, "", fmt.Errorf("open sqlite %s: %w", path, err)
	}
	return db, "sqlite", nil
}

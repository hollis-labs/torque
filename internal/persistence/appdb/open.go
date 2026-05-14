package appdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Open returns a *sql.DB plus the registered driver name ("sqlite" or
// "postgres") so callers (e.g. sqlstore.New) can dispatch dialect-specific
// behavior. For sqlite, the DB is opened via sqlitekit.OpenWriter — a
// single-connection writer pool with WAL, busy_timeout, and
// _txlock=immediate baked into the DSN so every connection inherits them.
// The pgx branch is left untouched.
func Open(ctx context.Context) (*sql.DB, string, error) {
	if dsn := os.Getenv("TORQUE_POSTGRES_DSN"); dsn != "" {
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

	path := os.Getenv("TORQUE_DB_PATH")
	if path == "" {
		path = "torque.db"
	}

	db, err := sqlitekit.OpenWriter(ctx, path, sqlitekit.OpenOptions{CreateParentDir: true})
	if err != nil {
		return nil, "", fmt.Errorf("open sqlite %s: %w", path, err)
	}
	return db, "sqlite", nil
}

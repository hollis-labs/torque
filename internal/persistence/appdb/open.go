package appdb

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/hollis-labs/libs/util/sqlite/sqlitekit"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const allowUnsupportedPostgresEnv = "TORQUE_ALLOW_UNSUPPORTED_POSTGRES"

func postgresStartupPolicy(dsn, override string) (bool, error) {
	if strings.TrimSpace(dsn) == "" {
		return false, nil
	}
	if override == "1" {
		return true, nil
	}
	return false, fmt.Errorf("Postgres application storage is not supported: Torque CRUD still uses SQLite-style placeholders and LastInsertId; unset TORQUE_POSTGRES_DSN to use SQLite or set %s=1 to continue at your own risk", allowUnsupportedPostgresEnv)
}

// Open returns a *sql.DB plus the registered driver name ("sqlite" or
// "postgres") so callers (e.g. sqlstore.New) can dispatch dialect-specific
// behavior. For sqlite, the DB is opened via sqlitekit.OpenWriter — a
// single-connection writer pool with WAL, busy_timeout, and
// _txlock=immediate baked into the DSN so every connection inherits them.
// The pgx branch is left untouched.
//
// sqlitePath is the caller-resolved main-database path (config.Config.DBPath,
// resolved via go-apppaths). Open no longer reads TORQUE_DB_PATH or falls back
// to a CWD-relative "torque.db" — that fallback was the data-loss footgun
// CW-20260517-0060 removes; path resolution is now config's job alone.
func Open(ctx context.Context, sqlitePath string) (*sql.DB, string, error) {
	if dsn := strings.TrimSpace(os.Getenv("TORQUE_POSTGRES_DSN")); dsn != "" {
		warn, err := postgresStartupPolicy(dsn, os.Getenv(allowUnsupportedPostgresEnv))
		if err != nil {
			return nil, "", err
		}
		if warn {
			log.Printf("WARNING: %s=1: continuing with unsupported Postgres application storage; CRUD may fail because SQLite-style placeholders and LastInsertId remain in use", allowUnsupportedPostgresEnv)
		}
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

	if sqlitePath == "" {
		return nil, "", fmt.Errorf("open sqlite: empty database path")
	}

	db, err := sqlitekit.OpenWriter(ctx, sqlitePath, sqlitekit.OpenOptions{CreateParentDir: true})
	if err != nil {
		return nil, "", fmt.Errorf("open sqlite %s: %w", sqlitePath, err)
	}
	return db, "sqlite", nil
}

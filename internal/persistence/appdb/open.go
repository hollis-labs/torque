package appdb

import (
	"database/sql"
	"fmt"
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

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, "", fmt.Errorf("open sqlite %s: %w", path, err)
	}
	return db, "sqlite", nil
}

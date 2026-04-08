package sqlstore

import (
	"database/sql"
	"fmt"
)

type Store struct {
	db      *sql.DB
	dialect Dialect
}

func New(db *sql.DB, driver string) (*Store, error) {
	var d Dialect
	switch driver {
	case "sqlite", "sqlite3":
		d = sqliteDialect{}
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			return nil, fmt.Errorf("enable WAL: %w", err)
		}
		if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
			return nil, fmt.Errorf("set busy_timeout: %w", err)
		}
		if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
			return nil, fmt.Errorf("enable foreign keys: %w", err)
		}
	case "postgres", "pgx":
		d = postgresDialect{}
	default:
		return nil, fmt.Errorf("unsupported driver: %s", driver)
	}
	return &Store{db: db, dialect: d}, nil
}

func (s *Store) DB() *sql.DB  { return s.db }
func (s *Store) Close() error { return s.db.Close() }

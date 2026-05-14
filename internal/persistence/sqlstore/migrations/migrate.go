package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed *.sql
var migrationFS embed.FS

func Run(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin migration connection: %w", err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	entries, err := migrationFS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, f := range files {
		var count int
		err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", f).Scan(&count)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", f, err)
		}
		if count > 0 {
			continue
		}

		content, err := migrationFS.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}

		restoreForeignKeys, err := foreignKeyRestoreStmt(ctx, conn)
		if err != nil {
			return fmt.Errorf("read foreign key state for %s: %w", f, err)
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return fmt.Errorf("disable foreign keys for %s: %w", f, err)
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			_, _ = conn.ExecContext(ctx, restoreForeignKeys)
			return fmt.Errorf("begin tx for %s: %w", f, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			_, _ = conn.ExecContext(ctx, restoreForeignKeys)
			return fmt.Errorf("execute migration %s: %w", f, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", f); err != nil {
			tx.Rollback()
			_, _ = conn.ExecContext(ctx, restoreForeignKeys)
			return fmt.Errorf("record migration %s: %w", f, err)
		}

		if err := tx.Commit(); err != nil {
			_, _ = conn.ExecContext(ctx, restoreForeignKeys)
			return fmt.Errorf("commit migration %s: %w", f, err)
		}

		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
			return fmt.Errorf("enable foreign keys after %s: %w", f, err)
		}
		if err := checkForeignKeys(ctx, conn, f); err != nil {
			_, _ = conn.ExecContext(ctx, restoreForeignKeys)
			return err
		}
		if _, err := conn.ExecContext(ctx, restoreForeignKeys); err != nil {
			return fmt.Errorf("restore foreign keys after %s: %w", f, err)
		}
	}

	return nil
}

func foreignKeyRestoreStmt(ctx context.Context, conn *sql.Conn) (string, error) {
	var enabled int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		return "", err
	}
	if enabled == 0 {
		return "PRAGMA foreign_keys = OFF", nil
	}
	return "PRAGMA foreign_keys = ON", nil
}

func checkForeignKeys(ctx context.Context, conn *sql.Conn, migration string) error {
	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign key check after %s: %w", migration, err)
	}
	defer rows.Close()

	for rows.Next() {
		var table string
		var rowID int64
		var parent string
		var fkID int64
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return fmt.Errorf("scan foreign key check after %s: %w", migration, err)
		}
		return fmt.Errorf("foreign key check after %s: %s row %d references missing %s (fk %d)", migration, table, rowID, parent, fkID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("foreign key check after %s: %w", migration, err)
	}
	return nil
}

package appdb

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteDSN_RelativePathUsesFileColonForm(t *testing.T) {
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prev)
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir tempdir: %v", err)
	}

	dsn := SQLiteDSN("clockwork.db", SQLiteDSNOptions{
		BusyTimeoutMs:    DefaultSQLiteBusyTimeoutMs,
		IncludeCacheSize: true,
		TxLock:           "immediate",
	})
	if !strings.HasPrefix(dsn, "file:clockwork.db?") {
		t.Fatalf("relative dsn prefix mismatch: %q", dsn)
	}
	if strings.HasPrefix(dsn, "file://") {
		t.Fatalf("relative dsn must not use authority form: %q", dsn)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
}

func TestSQLiteDSN_AbsolutePathUsesFileURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clockwork.db")
	dsn := SQLiteDSN(path, SQLiteDSNOptions{
		BusyTimeoutMs:    DefaultSQLiteBusyTimeoutMs,
		IncludeCacheSize: true,
		TxLock:           "immediate",
	})

	if !strings.HasPrefix(dsn, "file:///") {
		t.Fatalf("absolute dsn prefix mismatch: %q", dsn)
	}
	if !strings.Contains(dsn, "clockwork.db") {
		t.Fatalf("absolute dsn missing filename: %q", dsn)
	}
}

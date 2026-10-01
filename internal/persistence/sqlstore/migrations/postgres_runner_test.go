package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

func TestPostgresMissingMigrationFailsBeforeSQLiteDDL(t *testing.T) {
	db := postgresMigrationFixture(t)
	err := migrations.Run(db)
	require.ErrorContains(t, err, "postgres/001_initial.sql")
	var exists bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='tasks')`).Scan(&exists))
	require.False(t, exists, "unsupported historical migrations must not partially execute")
}

func postgresMigrationFixture(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TORQUE_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TORQUE_MIGRATION_TEST_POSTGRES_DSN to a disposable Postgres database")
	}
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	admin := stdlib.OpenDB(*config)
	t.Cleanup(func() { admin.Close() })
	schema := fmt.Sprintf("torque_migration_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); require.NoError(t, err) })
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, db.PingContext(context.Background()))
	return db
}

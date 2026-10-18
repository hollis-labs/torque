package healthscan_test

import (
	"os"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
)

// TestMain has migrations.Run start blank SQLite databases from one migrated
// image: under -race each replay otherwise costs ~2.2s, and these tests open
// many databases.
func TestMain(m *testing.M) {
	migrations.UseMigratedTemplate()
	os.Exit(m.Run())
}

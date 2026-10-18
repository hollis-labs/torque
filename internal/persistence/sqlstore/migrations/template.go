package migrations

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/stdlib"
)

// A test binary turns this on (UseMigratedTemplate, from TestMain) so that Run
// on a blank SQLite database loads one cached, fully migrated image instead of
// replaying every migration. Under -race the replay costs ~2.2s a database
// (0.07s without it), and the suites open hundreds. It is off in production and
// in the migration tests themselves, which keep replaying for real.
var (
	templateOn   atomic.Bool
	templateOnce sync.Once
	templateImg  []byte
)

// UseMigratedTemplate makes Run start a blank SQLite database from a migrated
// image. For test binaries only: call it from TestMain.
func UseMigratedTemplate() { templateOn.Store(true) }

type imageConn interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

// seedFromTemplate loads the migrated image into db when it is a blank in-memory database. It never
// reports a problem: on any doubt it leaves db as it found it and Run replays
// the migrations, which also checks the image is complete.
func seedFromTemplate(db *sql.DB) {
	if !templateOn.Load() {
		return
	}
	if _, postgres := db.Driver().(*stdlib.Driver); postgres {
		return
	}
	img := migratedImage()
	if len(img) == 0 {
		return
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	// Only a database that lives in memory: Deserialize swaps the connection's
	// database for the image, which a file-backed database would never write
	// back to its file.
	var file string
	if err := conn.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file); err != nil || file != "" {
		return
	}
	var objects int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master").Scan(&objects); err != nil || objects != 0 {
		return
	}
	_ = conn.Raw(func(dc any) error {
		if c, ok := dc.(imageConn); ok {
			return c.Deserialize(img)
		}
		return nil
	})
}

// migratedImage builds the image once per process, from a throwaway in-memory
// database, and returns nil when it cannot.
func migratedImage() []byte {
	templateOnce.Do(func() {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			return
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		if err := run(db); err != nil {
			return
		}
		conn, err := db.Conn(context.Background())
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.Raw(func(dc any) error {
			c, ok := dc.(imageConn)
			if !ok {
				return nil
			}
			img, err := c.Serialize()
			if err == nil {
				templateImg = img
			}
			return err
		})
	})
	return templateImg
}

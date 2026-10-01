package sqlstore

import (
	"database/sql"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

// The optional DSN must identify a disposable test database. Each PostgreSQL
// run uses a private schema; this fixture never bootstraps operator state.
func TestRunTimeSeriesStoreDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "pgx"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := ":memory:"
			stampType := "DATETIME"
			if dialect == "pgx" {
				dsn = os.Getenv("TORQUE_TIMESERIES_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("set TORQUE_TIMESERIES_TEST_POSTGRES_DSN to a disposable database")
				}
				stampType = "TIMESTAMPTZ"
			}
			db, err := sql.Open(dialect, dsn)
			require.NoError(t, err)
			t.Cleanup(func() { db.Close() })
			db.SetMaxOpenConns(1)
			if dialect == "pgx" {
				schema := fmt.Sprintf("torque_series_test_%d", time.Now().UnixNano())
				_, err = db.Exec("CREATE SCHEMA " + schema)
				require.NoError(t, err)
				t.Cleanup(func() { _, err := db.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
				_, err = db.Exec("SET search_path TO " + schema)
				require.NoError(t, err)
			}
			_, err = db.Exec("CREATE TABLE tasks (id TEXT PRIMARY KEY,project_id TEXT,sprint_id TEXT,epic_id TEXT,launch_profile TEXT,agent_profile TEXT); CREATE TABLE runs (id INTEGER PRIMARY KEY,task_id TEXT,status TEXT,executor TEXT,started_at " + stampType + ",prompt_tokens INTEGER,completion_tokens INTEGER); CREATE TABLE cost_ledger (run_id INTEGER,cost REAL)")
			require.NoError(t, err)
			s, err := New(db, dialect)
			require.NoError(t, err)
			_, err = db.Exec("INSERT INTO tasks VALUES ('t','p','s','e','','mock')")
			require.NoError(t, err)
			for i, stamp := range []string{"2026-10-01T23:59:59.999999Z", "2026-10-02T00:00:00Z"} {
				parsed, err := time.Parse(time.RFC3339Nano, stamp)
				require.NoError(t, err)
				var value any = parsed
				if dialect == "sqlite" {
					value = parsed.Format("2006-01-02 15:04:05.999999999") + " +0000 UTC"
				}
				_, err = db.Exec(s.runBind("INSERT INTO runs VALUES (?, 't', 'done', 'mock', ?,10,20)"), i+1, value)
				require.NoError(t, err)
				_, err = db.Exec(s.runBind("INSERT INTO cost_ledger VALUES (?,1.5), (?,2.5)"), i+1, i+1)
				require.NoError(t, err)
			}
			f := RunFilter{TaskID: "t", ProjectID: "p", SprintID: "s", EpicID: "e", Statuses: []string{"done"}, Since: time.Date(2026, 10, 1, 23, 0, 0, 0, time.UTC), Until: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}
			cells, err := s.RunTimeSeries(f, "hour", 0)
			require.NoError(t, err)
			require.Len(t, cells, 2)
			require.Equal(t, "2026-10-01T23:00:00", cells[0].Start)
			require.Equal(t, "2026-10-02T00:00:00", cells[1].Start)
			for _, c := range cells {
				require.Equal(t, 1, c.Count)
				require.EqualValues(t, 10, c.PromptTokens)
				require.EqualValues(t, 20, c.CompletionTokens)
				require.Equal(t, 4.0, c.Cost)
			}
			shifted, err := s.RunTimeSeries(f, "day", 30)
			require.NoError(t, err)
			require.Len(t, shifted, 1)
			require.Equal(t, "2026-10-02T00:00:00", shifted[0].Start)
			require.Equal(t, 2, shifted[0].Count)
			facet, err := s.RunFacets(f, []string{"status"}, 50)
			require.NoError(t, err)
			require.Equal(t, facet.MatchingCount, shifted[0].Count)
			require.Equal(t, facet.Totals.Cost, shifted[0].Cost)
			require.Equal(t, facet.Totals.PromptTokens, shifted[0].PromptTokens)
		})
	}
}

package sqlstore

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestTimestampComparisonDialect(t *testing.T) {
	pg := &Store{dialect: postgresDialect{}}
	require.Equal(t, "updated_at", pg.timestampSortKey("updated_at"))
	arg, err := pg.timestampArg("2026-08-19 21:31:49.100000001")
	require.NoError(t, err)
	instant, ok := arg.(time.Time)
	require.True(t, ok)
	require.Equal(t, 100000001, instant.Nanosecond())
	sqlite := &Store{dialect: sqliteDialect{}}
	arg, err = sqlite.timestampArg("2026-08-19 21:31:49.1")
	require.NoError(t, err)
	require.Equal(t, "2026-08-19 21:31:49.100000000", arg)
	_, err = sqlite.timestampArg("bad")
	require.Error(t, err)
	require.Equal(t, "priority", sqlite.timestampSortKey("priority"))
}

package bootstrap_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/runtime/waitpoll"
)

func bootstrapStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestWaitpoll_RegistersBuiltins(t *testing.T) {
	reg := waitpoll.NewRegistry()
	require.NoError(t, bootstrap.Waitpoll(reg, bootstrapStore(t)))
	for _, typ := range []string{"task_done", "url_reachable", "file_exists"} {
		_, ok := reg.Get(typ)
		assert.True(t, ok, "predicate %q should be registered", typ)
	}
}

func TestWaitpoll_NilRegistry(t *testing.T) {
	err := bootstrap.Waitpoll(nil, bootstrapStore(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry")
}

func TestWaitpoll_NilStore(t *testing.T) {
	err := bootstrap.Waitpoll(waitpoll.NewRegistry(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store")
}

func TestWaitpoll_DoubleRegistration_Fails(t *testing.T) {
	reg := waitpoll.NewRegistry()
	store := bootstrapStore(t)
	require.NoError(t, bootstrap.Waitpoll(reg, store))
	// Second call should fail — task_done is already registered.
	require.Error(t, bootstrap.Waitpoll(reg, store))
}

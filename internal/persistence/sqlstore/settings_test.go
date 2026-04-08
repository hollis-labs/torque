package sqlstore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSetSettings(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.SetSetting("theme", "dark"))

	val, err := store.GetSetting("theme")
	require.NoError(t, err)
	assert.Equal(t, "dark", val)

	// overwrite
	require.NoError(t, store.SetSetting("theme", "light"))
	val, err = store.GetSetting("theme")
	require.NoError(t, err)
	assert.Equal(t, "light", val)
}

func TestGetSettingDefault(t *testing.T) {
	store := setupTestStore(t)

	val, err := store.GetSetting("nonexistent")
	require.NoError(t, err)
	assert.Equal(t, "", val)
}

func TestListSettings(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.SetSetting("beta", "2"))
	require.NoError(t, store.SetSetting("alpha", "1"))
	require.NoError(t, store.SetSetting("gamma", "3"))

	settings, err := store.ListSettings()
	require.NoError(t, err)
	require.Len(t, settings, 3)

	// ORDER BY key ascending
	assert.Equal(t, "alpha", settings[0].Key)
	assert.Equal(t, "1", settings[0].Value)
	assert.Equal(t, "beta", settings[1].Key)
	assert.Equal(t, "gamma", settings[2].Key)
}

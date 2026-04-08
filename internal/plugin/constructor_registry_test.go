package plugin

import (
	"testing"

	goplugin "github.com/hollis-labs/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterAndLookupConstructor(t *testing.T) {
	ResetRegistry()
	defer ResetRegistry()

	RegisterPluginConstructor("plug-reg-1", func() goplugin.Plugin {
		return &testPlugin{id: "plug-reg-1", name: "Registered Plugin 1"}
	})

	ctor, ok := LookupConstructor("plug-reg-1")
	require.True(t, ok)
	require.NotNil(t, ctor)

	p := ctor()
	require.NotNil(t, p)
	assert.Equal(t, "plug-reg-1", p.ID())
}

func TestLookupMissingConstructor(t *testing.T) {
	ResetRegistry()
	defer ResetRegistry()

	_, ok := LookupConstructor("does-not-exist")
	assert.False(t, ok)
}

func TestGetRegistered(t *testing.T) {
	ResetRegistry()
	defer ResetRegistry()

	RegisterPluginConstructor("plug-a", func() goplugin.Plugin {
		return &testPlugin{id: "plug-a", name: "A"}
	})
	RegisterPluginConstructor("plug-b", func() goplugin.Plugin {
		return &testPlugin{id: "plug-b", name: "B"}
	})

	all := GetRegistered()
	assert.Len(t, all, 2)
	assert.Contains(t, all, "plug-a")
	assert.Contains(t, all, "plug-b")

	// Verify the returned map is a copy — modifications don't affect the registry.
	delete(all, "plug-a")
	_, stillThere := LookupConstructor("plug-a")
	assert.True(t, stillThere)
}

func TestRegisterDuplicatePanics(t *testing.T) {
	ResetRegistry()
	defer ResetRegistry()

	RegisterPluginConstructor("dup-id", func() goplugin.Plugin {
		return &testPlugin{id: "dup-id", name: "Dup"}
	})

	assert.Panics(t, func() {
		RegisterPluginConstructor("dup-id", func() goplugin.Plugin {
			return &testPlugin{id: "dup-id", name: "Dup Again"}
		})
	})
}

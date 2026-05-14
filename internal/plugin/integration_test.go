package plugin_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	goplugin "github.com/hollis-labs/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pluginpkg "github.com/hollis-labs/torque/internal/plugin"
)

// ---- integrationPlugin ----

type integrationPlugin struct {
	id     string
	loaded bool
}

func newIntegrationPlugin(id string) *integrationPlugin {
	return &integrationPlugin{id: id}
}

func (p *integrationPlugin) ID() string             { return p.id }
func (p *integrationPlugin) Name() string           { return "Integration Plugin" }
func (p *integrationPlugin) Version() string        { return "1.0.0" }
func (p *integrationPlugin) Description() string    { return "Integration test plugin" }
func (p *integrationPlugin) Dependencies() []string { return nil }
func (p *integrationPlugin) Status() goplugin.PluginStatus {
	return goplugin.PluginStatus{Loaded: p.loaded}
}

func (p *integrationPlugin) Load(host goplugin.Host) error {
	// Register a CRUD handler.
	if err := host.RegisterCRUDHandler("integration-resources", &integrationCRUDHandler{}); err != nil {
		return err
	}

	// Register an event hook.
	if err := host.RegisterEventHook([]string{pluginpkg.EventTaskCreated}, &integrationEventHook{}); err != nil {
		return err
	}

	// Register a UI component.
	if err := host.RegisterUIComponent(goplugin.UIComponent{
		ID:   "integration-widget",
		Type: goplugin.UIComponentTypeWidget,
		Name: "Integration Widget",
	}); err != nil {
		return err
	}

	// Register a UI slot entry — requires type assertion to *TorqueHost.
	if ch, ok := host.(*pluginpkg.TorqueHost); ok {
		_ = ch.RegisterSlot(pluginpkg.UISlotEntry{
			ID:       "integration-slot-entry",
			PluginID: p.id,
			Slot:     pluginpkg.SlotDashboard,
			Label:    "Integration Dashboard Widget",
			Priority: 5,
		})
	}

	p.loaded = true
	return nil
}

func (p *integrationPlugin) Unload() error {
	p.loaded = false
	return nil
}

// ---- integrationCRUDHandler ----

type integrationCRUDHandler struct{}

func (h *integrationCRUDHandler) Create(_ context.Context, resource interface{}) (interface{}, error) {
	return resource, nil
}
func (h *integrationCRUDHandler) Read(_ context.Context, id string) (interface{}, error) {
	return map[string]string{"id": id}, nil
}
func (h *integrationCRUDHandler) Update(_ context.Context, _ string, resource interface{}) (interface{}, error) {
	return resource, nil
}
func (h *integrationCRUDHandler) Delete(_ context.Context, _ string) error { return nil }
func (h *integrationCRUDHandler) List(_ context.Context, _ map[string]interface{}) ([]interface{}, error) {
	return nil, nil
}

// ---- integrationEventHook ----

type integrationEventHook struct {
	received []goplugin.Event
}

func (h *integrationEventHook) EventTypes() []string { return []string{pluginpkg.EventTaskCreated} }
func (h *integrationEventHook) Handle(_ context.Context, event goplugin.Event) error {
	h.received = append(h.received, event)
	return nil
}

// ---- integrationExecutor ----

type integrationExecutor struct{}

func (e *integrationExecutor) Name() string { return "integration-exec" }
func (e *integrationExecutor) Run(_ interface{}, _ *pluginpkg.ExecutionJob, _ pluginpkg.EventCallback) (*pluginpkg.ExecutionResult, error) {
	return &pluginpkg.ExecutionResult{Status: "done"}, nil
}
func (e *integrationExecutor) Capabilities() pluginpkg.ExecutorCapabilities {
	return pluginpkg.ExecutorCapabilities{}
}
func (e *integrationExecutor) Validate(_ *pluginpkg.ExecutionJob) error { return nil }

// ---- TestIntegrationFullLifecycle ----

func TestIntegrationFullLifecycle(t *testing.T) {
	// 1. Create host with mux.
	mux := http.NewServeMux()
	h := pluginpkg.NewTorqueHost(mux, nil)
	require.NotNil(t, h)

	// 2. Register services, executor, tools, filter.
	h.RegisterService("db", struct{}{})

	exec := &integrationExecutor{}
	require.NoError(t, h.RegisterExecutor(exec))

	tools := []pluginpkg.ToolDefinition{
		{Name: "integration-tool", Description: "An integration tool"},
	}
	require.NoError(t, h.RegisterTools(tools))

	require.NoError(t, h.RegisterFilter(pluginpkg.FilterTaskBeforeCreate, 1,
		func(data interface{}, ctx pluginpkg.FilterContext) (interface{}, error) {
			return data, nil
		}))

	// 3. Load integration plugin.
	p := newIntegrationPlugin("integration")
	require.NoError(t, h.LoadPlugin(p))

	// 4. Verify: plugin loaded.
	loaded, ok := h.GetPlugin("integration")
	require.True(t, ok)
	assert.Equal(t, "integration", loaded.ID())
	assert.True(t, p.Status().Loaded)

	// CRUD handler registered.
	handlers := h.GetCRUDHandlers()
	_, hasHandler := handlers["integration-resources"]
	assert.True(t, hasHandler, "integration-resources CRUD handler should be registered")

	// Slot entries exist.
	slots := h.GetSlotEntries(pluginpkg.SlotDashboard)
	require.NotEmpty(t, slots)
	assert.Equal(t, "integration-slot-entry", slots[0].ID)

	// Events emit without panic.
	sub := h.SubscribeEvents()
	assert.NotPanics(t, func() {
		h.EmitEvent(pluginpkg.NewEvent(pluginpkg.EventTaskCreated, "integration-test",
			pluginpkg.EventData{TaskID: "t-integration-1"}))
	})

	select {
	case ev := <-sub:
		assert.Equal(t, pluginpkg.EventTaskCreated, ev.Type)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for emitted event")
	}
	h.UnsubscribeEvents(sub)

	// 5. Discover plugins from temp dir with manifest.
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "myplugin")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))
	manifest := `name: myplugin
version: 0.1.0
description: Test manifest plugin
author: Test
dependencies: []
`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(manifest), 0o644))

	// Register a constructor so DiscoverPlugins can pick it up.
	pluginpkg.ResetRegistry()
	pluginpkg.RegisterPluginConstructor("myplugin", func() goplugin.Plugin {
		return newIntegrationPlugin("myplugin")
	})

	discovered, err := pluginpkg.DiscoverPlugins(tmpDir)
	require.NoError(t, err)
	require.Len(t, discovered, 1)
	assert.Equal(t, "myplugin", discovered[0].Manifest.Name)

	// 6. Unload plugin, verify status.
	require.NoError(t, h.UnloadPlugin("integration"))
	assert.False(t, p.Status().Loaded)

	// 7. Shutdown host.
	require.NoError(t, h.Shutdown())
}

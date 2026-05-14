package core_test

import (
	"context"
	"net/http"
	"testing"

	goplugin "github.com/hollis-labs/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pluginpkg "github.com/hollis-labs/torque/internal/plugin"
	"github.com/hollis-labs/torque/plugins/core"
)

// ---- mock task service ----

type mockTaskService struct {
	tasks map[string]interface{}
}

func newMockTaskService() *mockTaskService {
	return &mockTaskService{tasks: make(map[string]interface{})}
}

func (m *mockTaskService) Create(_ context.Context, resource interface{}) (interface{}, error) {
	id := "task-1"
	m.tasks[id] = resource
	return resource, nil
}

func (m *mockTaskService) Get(_ context.Context, id string) (interface{}, error) {
	return m.tasks[id], nil
}

func (m *mockTaskService) Update(_ context.Context, id string, resource interface{}) (interface{}, error) {
	m.tasks[id] = resource
	return resource, nil
}

func (m *mockTaskService) Delete(_ context.Context, id string) error {
	delete(m.tasks, id)
	return nil
}

func (m *mockTaskService) List(_ context.Context, _ map[string]interface{}) ([]interface{}, error) {
	out := make([]interface{}, 0, len(m.tasks))
	for _, v := range m.tasks {
		out = append(out, v)
	}
	return out, nil
}

// ---- helpers ----

func newTestHost() *pluginpkg.TorqueHost {
	return pluginpkg.NewTorqueHost(http.NewServeMux(), nil)
}

func newCorePlugin() goplugin.Plugin {
	return core.New()
}

// ---- tests ----

func TestCorePluginLoads(t *testing.T) {
	h := newTestHost()
	p := newCorePlugin()
	require.NoError(t, h.LoadPlugin(p))

	// CRUD handler for "tasks" should be registered.
	handlers := h.GetCRUDHandlers()
	_, found := handlers["tasks"]
	assert.True(t, found, "tasks CRUD handler should be registered")
}

func TestCorePluginTaskCRUD(t *testing.T) {
	h := newTestHost()
	svc := newMockTaskService()
	h.RegisterService("task-service", svc)

	p := newCorePlugin()
	require.NoError(t, h.LoadPlugin(p))

	handlers := h.GetCRUDHandlers()
	handler, ok := handlers["tasks"]
	require.True(t, ok)

	ctx := context.Background()

	// Create a task.
	task := map[string]string{"title": "Test task"}
	created, err := handler.Create(ctx, task)
	require.NoError(t, err)
	assert.Equal(t, task, created)

	// List tasks.
	list, err := handler.List(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestCorePluginEventHooks(t *testing.T) {
	h := newTestHost()
	p := newCorePlugin()
	require.NoError(t, h.LoadPlugin(p))

	// Emit an event — should not panic.
	assert.NotPanics(t, func() {
		h.EmitEvent(pluginpkg.NewEvent(pluginpkg.EventTaskCreated, "test", pluginpkg.EventData{TaskID: "t1"}))
	})
}

func TestCorePluginUnloads(t *testing.T) {
	h := newTestHost()
	p := newCorePlugin()
	require.NoError(t, h.LoadPlugin(p))

	// Status: loaded.
	assert.True(t, p.Status().Loaded)

	// Unload.
	require.NoError(t, h.UnloadPlugin("core"))
	assert.False(t, p.Status().Loaded)
}

package plugin

import (
	"context"
	"net/http"
	"testing"
	"time"

	goplugin "github.com/hollis-labs/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- test helpers ----

type testPlugin struct {
	id, name string
	deps     []string
	loaded   bool
}

func (p *testPlugin) ID() string             { return p.id }
func (p *testPlugin) Name() string           { return p.name }
func (p *testPlugin) Version() string        { return "1.0.0" }
func (p *testPlugin) Description() string    { return "test plugin" }
func (p *testPlugin) Dependencies() []string { return p.deps }
func (p *testPlugin) Unload() error          { p.loaded = false; return nil }
func (p *testPlugin) Status() PluginStatus   { return PluginStatus{Loaded: p.loaded} }
func (p *testPlugin) Load(host Host) error   { p.loaded = true; return nil }

type testCRUDHandler struct{}

func (h *testCRUDHandler) Create(ctx context.Context, resource interface{}) (interface{}, error) {
	return resource, nil
}
func (h *testCRUDHandler) Read(ctx context.Context, id string) (interface{}, error) {
	return map[string]string{"id": id}, nil
}
func (h *testCRUDHandler) Update(ctx context.Context, id string, resource interface{}) (interface{}, error) {
	return resource, nil
}
func (h *testCRUDHandler) Delete(ctx context.Context, id string) error { return nil }
func (h *testCRUDHandler) List(ctx context.Context, filters map[string]interface{}) ([]interface{}, error) {
	return nil, nil
}

type testEventHook struct {
	handled []goplugin.Event
}

func (h *testEventHook) EventTypes() []string { return []string{EventTaskCreated} }
func (h *testEventHook) Handle(ctx context.Context, event goplugin.Event) error {
	h.handled = append(h.handled, event)
	return nil
}

type testConnector struct{ name string }

func (c *testConnector) Name() string                                                   { return c.name }
func (c *testConnector) Send(ctx context.Context, payload map[string]interface{}) error { return nil }
func (c *testConnector) Health(ctx context.Context) error                               { return nil }

type testExecutor struct{ name string }

func (e *testExecutor) Name() string { return e.name }
func (e *testExecutor) Run(ctx interface{}, job *ExecutionJob, cb EventCallback) (*ExecutionResult, error) {
	return &ExecutionResult{Status: "done"}, nil
}
func (e *testExecutor) Capabilities() ExecutorCapabilities {
	return ExecutorCapabilities{SupportsStreaming: true}
}
func (e *testExecutor) Validate(job *ExecutionJob) error { return nil }

// ---- tests ----

func TestNewTorqueHost(t *testing.T) {
	h := NewTorqueHost(nil, nil)
	require.NotNil(t, h)
	assert.NotNil(t, h.inner)
	assert.NotNil(t, h.executors)
	assert.NotNil(t, h.slots)
	assert.NotNil(t, h.filters)
	assert.NotNil(t, h.connectorHealth)
	assert.NotNil(t, h.ctx)
	assert.NotNil(t, h.cancel)
}

func TestLoadPlugin(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))
	p := &testPlugin{id: "plug-1", name: "Test Plugin"}
	err := h.LoadPlugin(p)
	require.NoError(t, err)
	assert.True(t, p.loaded)

	got, ok := h.GetPlugin("plug-1")
	require.True(t, ok)
	assert.Equal(t, "plug-1", got.ID())
}

func TestLoadPluginDuplicate(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))
	p := &testPlugin{id: "plug-dup", name: "Dup"}
	require.NoError(t, h.LoadPlugin(p))
	err := h.LoadPlugin(&testPlugin{id: "plug-dup", name: "Dup2"})
	require.Error(t, err)
}

func TestLoadPluginMissingDep(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))
	p := &testPlugin{id: "needs-dep", name: "Dep", deps: []string{"missing-dep"}}
	err := h.LoadPlugin(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-dep")
}

func TestRegisterCRUDHandler(t *testing.T) {
	mux := http.NewServeMux()
	h := NewTorqueHost(mux, NewLogger("test"))
	err := h.RegisterCRUDHandler("widgets", &testCRUDHandler{})
	require.NoError(t, err)

	handlers := h.GetCRUDHandlers()
	_, ok := handlers["widgets"]
	assert.True(t, ok)
}

func TestRegisterEventHook(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))
	hook := &testEventHook{}
	err := h.RegisterEventHook([]string{EventTaskCreated}, hook)
	require.NoError(t, err)

	h.EmitEvent(NewEvent(EventTaskCreated, "test", EventData{TaskID: "t1"}))
	// Give hooks a moment (they're synchronous)
	assert.Len(t, hook.handled, 1)
}

func TestRegisterExecutor(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))
	exec := &testExecutor{name: "claude"}
	err := h.RegisterExecutor(exec)
	require.NoError(t, err)

	got, ok := h.GetExecutor("claude")
	require.True(t, ok)
	assert.Equal(t, "claude", got.Name())

	// Duplicate should fail.
	err = h.RegisterExecutor(&testExecutor{name: "claude"})
	require.Error(t, err)
}

func TestRegisterTools(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))
	tools := []ToolDefinition{
		{Name: "tool-a", Description: "A tool"},
		{Name: "tool-b", Description: "B tool"},
	}
	err := h.RegisterTools(tools)
	require.NoError(t, err)

	got := h.GetTools()
	require.Len(t, got, 2)
	assert.Equal(t, "tool-a", got[0].Name)
}

func TestRegisterUISlot(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))

	entries := []UISlotEntry{
		{ID: "e1", PluginID: "p1", Slot: SlotDashboard, Label: "Widget A", Priority: 10},
		{ID: "e2", PluginID: "p1", Slot: SlotDashboard, Label: "Widget B", Priority: 1},
		{ID: "e3", PluginID: "p2", Slot: SlotDashboard, Label: "Widget C", Priority: 5},
	}
	for _, e := range entries {
		require.NoError(t, h.RegisterSlot(e))
	}

	got := h.GetSlotEntries(SlotDashboard)
	require.Len(t, got, 3)
	// Should be sorted by priority
	assert.Equal(t, 1, got[0].Priority)
	assert.Equal(t, 5, got[1].Priority)
	assert.Equal(t, 10, got[2].Priority)
}

func TestRegisterFilter(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))
	called := false
	err := h.RegisterFilter(FilterTaskBeforeCreate, 1, func(data interface{}, ctx FilterContext) (interface{}, error) {
		called = true
		return data, nil
	})
	require.NoError(t, err)

	fr := h.GetFilterRegistry()
	assert.Equal(t, 1, fr.Len(FilterTaskBeforeCreate))

	_, err = fr.Apply(FilterTaskBeforeCreate, "x", FilterContext{})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRegisterConnector(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))
	c := &testConnector{name: "slack"}
	err := h.RegisterConnector("slack", c)
	require.NoError(t, err)

	health := h.ConnectorHealth()
	require.Len(t, health, 1)
	assert.Equal(t, "slack", health[0].Name)
	assert.True(t, health[0].Healthy)
}

func TestEmitEvent(t *testing.T) {
	h := NewTorqueHost(nil, NewLogger("test"))

	ch := h.SubscribeEvents()
	hook := &testEventHook{}
	require.NoError(t, h.RegisterEventHook([]string{EventRunStarted}, hook))

	h.EmitEvent(NewEvent(EventRunStarted, "test", EventData{RunID: "r1"}))

	// Both subscriber and hook should receive.
	assert.Len(t, hook.handled, 1)

	select {
	case ev := <-ch:
		assert.Equal(t, EventRunStarted, ev.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for event")
	}

	h.UnsubscribeEvents(ch)
}

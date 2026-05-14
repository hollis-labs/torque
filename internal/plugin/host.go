package plugin

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"

	goplugin "github.com/hollis-labs/plugin"
)

// Compile-time check: TorqueHost must satisfy goplugin.Host.
var _ goplugin.Host = (*TorqueHost)(nil)

// TorqueHost is the Torque-specific Host implementation.
// It wraps the SDK Registry and adds Torque-domain capabilities.
type TorqueHost struct {
	inner           *goplugin.Registry
	mu              sync.RWMutex
	mux             *http.ServeMux
	plugins         map[string]goplugin.Plugin
	executors       map[string]Executor
	tools           []ToolDefinition
	slots           map[UISlotName][]UISlotEntry
	filters         *FilterRegistry
	connectorHealth map[string]*HealthStatus
	crudHandlers    map[string]goplugin.CRUDHandler
	eventSubs       []chan goplugin.Event
	logger          goplugin.Logger
	ctx             context.Context
	cancel          context.CancelFunc
}

// NewTorqueHost creates a new TorqueHost.
func NewTorqueHost(mux *http.ServeMux, log goplugin.Logger) *TorqueHost {
	if log == nil {
		log = NewLogger("torque")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &TorqueHost{
		inner:           goplugin.NewRegistry(log),
		mux:             mux,
		plugins:         make(map[string]goplugin.Plugin),
		executors:       make(map[string]Executor),
		tools:           nil,
		slots:           make(map[UISlotName][]UISlotEntry),
		filters:         NewFilterRegistry(),
		connectorHealth: make(map[string]*HealthStatus),
		crudHandlers:    make(map[string]goplugin.CRUDHandler),
		eventSubs:       nil,
		logger:          log,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// ---- goplugin.Host interface ----

// GetPlugin retrieves a loaded plugin by ID.
func (h *TorqueHost) GetPlugin(id string) (goplugin.Plugin, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.plugins[id]
	return p, ok
}

// RegisterCRUDHandler registers a CRUD handler and auto-wires HTTP routes if mux is set.
func (h *TorqueHost) RegisterCRUDHandler(resourceType string, handler goplugin.CRUDHandler) error {
	if err := h.inner.RegisterCRUDHandler(resourceType, handler); err != nil {
		return err
	}
	h.mu.Lock()
	h.crudHandlers[resourceType] = handler
	h.mu.Unlock()
	if h.mux != nil {
		wireCRUDRoutes(h.mux, resourceType, handler)
	}
	return nil
}

// RegisterEventHook registers an event hook for specific event types.
func (h *TorqueHost) RegisterEventHook(eventTypes []string, hook goplugin.EventHook) error {
	return h.inner.RegisterEventHook(eventTypes, hook)
}

// RegisterUIComponent registers a UI component.
func (h *TorqueHost) RegisterUIComponent(component goplugin.UIComponent) error {
	return h.inner.RegisterUIComponent(component)
}

// GetService returns a named service.
func (h *TorqueHost) GetService(name string) (interface{}, error) {
	return h.inner.GetService(name)
}

// RegisterService registers a named service.
func (h *TorqueHost) RegisterService(name string, service interface{}) {
	h.inner.RegisterService(name, service)
}

// GetConfig returns a config value (delegated to inner registry).
func (h *TorqueHost) GetConfig(key string) (string, error) {
	return h.inner.GetConfig(key)
}

// SetConfig persists a config value.
func (h *TorqueHost) SetConfig(key, value string) error {
	return h.inner.SetConfig(key, value)
}

// RegisterConfigSchema registers config field definitions.
func (h *TorqueHost) RegisterConfigSchema(fields []goplugin.ConfigFieldDef) error {
	return h.inner.RegisterConfigSchema(fields)
}

// RegisterConnector registers a connector and initialises its health entry.
// Note: the inner registry does not support connectors; they are tracked here.
func (h *TorqueHost) RegisterConnector(name string, connector goplugin.Connector) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.connectorHealth[name]; exists {
		return fmt.Errorf("connector %q already registered", name)
	}
	h.connectorHealth[name] = &HealthStatus{
		Name:    name,
		Healthy: true,
	}
	return nil
}

// RegisterProvider registers a runtime LLM provider.
func (h *TorqueHost) RegisterProvider(name string, provider interface{}) error {
	return h.inner.RegisterProvider(name, provider)
}

// RegisterCLIAdapter registers a runtime CLI adapter.
func (h *TorqueHost) RegisterCLIAdapter(name string, adapter interface{}) error {
	return h.inner.RegisterCLIAdapter(name, adapter)
}

// Logger returns the host logger.
func (h *TorqueHost) Logger() goplugin.Logger {
	return h.logger
}

// Context returns the host context.
func (h *TorqueHost) Context() context.Context {
	return h.ctx
}

// ---- Plugin lifecycle ----

// LoadPlugin loads a plugin into the host.
// It validates dependencies, calls p.Load(h) so plugins receive TorqueHost
// as their host (not the inner Registry), and stores the plugin locally.
func (h *TorqueHost) LoadPlugin(p goplugin.Plugin) error {
	id := p.ID()

	h.mu.Lock()
	if _, exists := h.plugins[id]; exists {
		h.mu.Unlock()
		return fmt.Errorf("plugin with ID %s is already loaded", id)
	}
	// Validate dependencies are satisfied.
	for _, dep := range p.Dependencies() {
		if _, ok := h.plugins[dep]; !ok {
			h.mu.Unlock()
			return fmt.Errorf("plugin %s depends on %s which is not loaded", id, dep)
		}
	}
	h.mu.Unlock()

	// Call Load with TorqueHost as the host so plugins get full Torque capabilities.
	if err := p.Load(h); err != nil {
		return fmt.Errorf("failed to load plugin %s: %w", id, err)
	}

	h.mu.Lock()
	h.plugins[id] = p
	h.mu.Unlock()

	h.logger.Info("Plugin loaded", "id", id, "name", p.Name(), "version", p.Version())
	return nil
}

// UnloadPlugin unloads a plugin from the host.
func (h *TorqueHost) UnloadPlugin(id string) error {
	h.mu.Lock()
	p, exists := h.plugins[id]
	if !exists {
		h.mu.Unlock()
		return fmt.Errorf("plugin with ID %s is not loaded", id)
	}
	// Check no other loaded plugin depends on this one.
	for _, other := range h.plugins {
		for _, dep := range other.Dependencies() {
			if dep == id {
				h.mu.Unlock()
				return fmt.Errorf("cannot unload plugin %s: plugin %s depends on it", id, other.ID())
			}
		}
	}
	h.mu.Unlock()

	if err := p.Unload(); err != nil {
		return fmt.Errorf("failed to unload plugin %s: %w", id, err)
	}

	h.mu.Lock()
	delete(h.plugins, id)
	h.mu.Unlock()

	h.logger.Info("Plugin unloaded", "id", id)
	return nil
}

// ---- Torque-specific methods ----

// RegisterExecutor registers a named executor.
func (h *TorqueHost) RegisterExecutor(exec Executor) error {
	if exec == nil {
		return fmt.Errorf("executor must not be nil")
	}
	name := exec.Name()
	if name == "" {
		return fmt.Errorf("executor name must not be empty")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.executors[name]; exists {
		return fmt.Errorf("executor %q already registered", name)
	}
	h.executors[name] = exec
	return nil
}

// GetExecutor retrieves a named executor.
func (h *TorqueHost) GetExecutor(name string) (Executor, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	e, ok := h.executors[name]
	return e, ok
}

// RegisterTools appends tool definitions to the host's tool list.
func (h *TorqueHost) RegisterTools(tools []ToolDefinition) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tools = append(h.tools, tools...)
	return nil
}

// GetTools returns all registered tool definitions.
func (h *TorqueHost) GetTools() []ToolDefinition {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]ToolDefinition, len(h.tools))
	copy(out, h.tools)
	return out
}

// RegisterSlot adds a UISlotEntry to its slot.
func (h *TorqueHost) RegisterSlot(entry UISlotEntry) error {
	if entry.Slot == "" {
		return fmt.Errorf("slot name must not be empty")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.slots[entry.Slot] = append(h.slots[entry.Slot], entry)
	return nil
}

// GetSlotEntries returns all entries for a slot, sorted by priority (lower first).
func (h *TorqueHost) GetSlotEntries(slot UISlotName) []UISlotEntry {
	h.mu.RLock()
	entries := make([]UISlotEntry, len(h.slots[slot]))
	copy(entries, h.slots[slot])
	h.mu.RUnlock()

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Priority < entries[j].Priority
	})
	return entries
}

// RegisterFilter registers a filter with the host (pluginID="host").
func (h *TorqueHost) RegisterFilter(name string, priority int, fn FilterFunc) error {
	h.filters.Register(name, "host", priority, fn)
	return nil
}

// GetFilterRegistry returns the filter registry.
func (h *TorqueHost) GetFilterRegistry() *FilterRegistry {
	return h.filters
}

// GetCRUDHandlers returns a copy of all registered CRUD handlers.
func (h *TorqueHost) GetCRUDHandlers() map[string]goplugin.CRUDHandler {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]goplugin.CRUDHandler, len(h.crudHandlers))
	for k, v := range h.crudHandlers {
		out[k] = v
	}
	return out
}

// EmitEvent dispatches an event to the inner registry hooks and all subscribers.
func (h *TorqueHost) EmitEvent(event goplugin.Event) {
	_ = h.inner.EmitEvent(event)
	h.broadcastEvent(event)
}

// ConnectorHealth returns health status for all registered connectors.
func (h *TorqueHost) ConnectorHealth() []HealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]HealthStatus, 0, len(h.connectorHealth))
	for _, hs := range h.connectorHealth {
		out = append(out, *hs)
	}
	return out
}

// Shutdown cancels the host context and shuts down the inner registry.
func (h *TorqueHost) Shutdown() error {
	err := h.inner.Shutdown()
	h.cancel()
	return err
}

// Copied from https://github.com/hollis-labs/plugin/blob/741878c627b2/registry.go.
// Kept local per architect decision archived_plugin_successor_plugin_sdk_2026_10_16
// (CW-20261016-0078). SDK contract types resolve through the aliases in types.go.

package plugin

import (
	"context"
	"fmt"
	"sync"
)

// UIRegistry manages UI components from all loaded plugins
type UIRegistry interface {
	// RegisterComponent registers a UI component
	RegisterComponent(component UIComponent) error

	// GetComponent retrieves a UI component by ID
	GetComponent(id string) (UIComponent, bool)

	// GetComponentsByType retrieves all components of a specific type
	GetComponentsByType(componentType UIComponentType) []UIComponent

	// ListComponents lists all registered UI components
	ListComponents() []UIComponent

	// UnregisterComponent removes a UI component
	UnregisterComponent(id string) error
}

// Registry is the main plugin registry that manages all loaded plugins
type Registry struct {
	mu           sync.RWMutex
	plugins      map[string]Plugin
	crudHandlers map[string]CRUDHandler
	eventHooks   map[string][]EventHook // eventType -> []hooks
	uiRegistry   UIRegistry
	services     map[string]interface{}
	logger       Logger
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewRegistry creates a new plugin registry
func NewRegistry(logger Logger) *Registry {
	ctx, cancel := context.WithCancel(context.Background())

	return &Registry{
		plugins:      make(map[string]Plugin),
		crudHandlers: make(map[string]CRUDHandler),
		eventHooks:   make(map[string][]EventHook),
		uiRegistry:   NewUIRegistry(),
		services:     make(map[string]interface{}),
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// LoadPlugin loads a plugin into the registry
func (r *Registry) LoadPlugin(plugin Plugin) error {
	r.mu.Lock()
	id := plugin.ID()

	// Check for conflicts
	if _, exists := r.plugins[id]; exists {
		r.mu.Unlock()
		return fmt.Errorf("plugin with ID %s is already loaded", id)
	}

	// Validate dependencies
	for _, dep := range plugin.Dependencies() {
		if _, exists := r.plugins[dep]; !exists {
			r.mu.Unlock()
			return fmt.Errorf("plugin %s depends on %s which is not loaded", id, dep)
		}
	}
	r.mu.Unlock()

	// Load the plugin WITHOUT holding the lock — Load calls back into
	// Register* methods which acquire r.mu.
	if err := plugin.Load(r); err != nil {
		return fmt.Errorf("failed to load plugin %s: %w", id, err)
	}

	r.mu.Lock()
	r.plugins[id] = plugin
	r.mu.Unlock()

	r.logger.Info("Plugin loaded", "id", id, "name", plugin.Name(), "version", plugin.Version())
	return nil
}

// UnloadPlugin unloads a plugin from the registry
func (r *Registry) UnloadPlugin(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugin, exists := r.plugins[id]
	if !exists {
		return fmt.Errorf("plugin with ID %s is not loaded", id)
	}

	// Check if other plugins depend on this one
	for _, p := range r.plugins {
		for _, dep := range p.Dependencies() {
			if dep == id {
				return fmt.Errorf("cannot unload plugin %s: plugin %s depends on it", id, p.ID())
			}
		}
	}

	// Unload the plugin
	if err := plugin.Unload(); err != nil {
		r.logger.Error("Error unloading plugin", "id", id, "error", err)
		return fmt.Errorf("failed to unload plugin %s: %w", id, err)
	}

	delete(r.plugins, id)
	r.logger.Info("Plugin unloaded", "id", id)

	return nil
}

// GetPlugin retrieves a loaded plugin by ID
func (r *Registry) GetPlugin(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugin, exists := r.plugins[id]
	return plugin, exists
}

// ListPlugins returns all loaded plugins
func (r *Registry) ListPlugins() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugins := make([]Plugin, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		plugins = append(plugins, plugin)
	}

	return plugins
}

// RegisterCRUDHandler implements the Host interface
func (r *Registry) RegisterCRUDHandler(resourceType string, handler CRUDHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.crudHandlers[resourceType]; exists {
		return fmt.Errorf("CRUD handler for resource type %s is already registered", resourceType)
	}

	r.crudHandlers[resourceType] = handler
	r.logger.Debug("CRUD handler registered", "resourceType", resourceType)

	return nil
}

// GetCRUDHandler retrieves a CRUD handler by resource type
func (r *Registry) GetCRUDHandler(resourceType string) (CRUDHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, exists := r.crudHandlers[resourceType]
	return handler, exists
}

// RegisterEventHook implements the Host interface
func (r *Registry) RegisterEventHook(eventTypes []string, hook EventHook) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, eventType := range eventTypes {
		r.eventHooks[eventType] = append(r.eventHooks[eventType], hook)
		r.logger.Debug("Event hook registered", "eventType", eventType)
	}

	return nil
}

// EmitEvent dispatches an event to all registered hooks
func (r *Registry) EmitEvent(event Event) error {
	r.mu.RLock()
	hooks := r.eventHooks[event.Type]
	r.mu.RUnlock()

	for _, hook := range hooks {
		if err := hook.Handle(r.ctx, event); err != nil {
			r.logger.Error("Event hook error", "eventType", event.Type, "error", err)
			// Continue processing other hooks even if one fails
		}
	}

	return nil
}

// RegisterUIComponent implements the Host interface
func (r *Registry) RegisterUIComponent(component UIComponent) error {
	return r.uiRegistry.RegisterComponent(component)
}

// GetUIRegistry returns the UI registry
func (r *Registry) GetUIRegistry() UIRegistry {
	return r.uiRegistry
}

// RegisterService registers a service for plugins to access
func (r *Registry) RegisterService(name string, service interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.services[name] = service
	r.logger.Debug("Service registered", "name", name)
}

// GetService implements the Host interface
func (r *Registry) GetService(name string) (interface{}, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services[name]
	if !exists {
		return nil, fmt.Errorf("service %s is not registered", name)
	}

	return service, nil
}

// Logger implements the Host interface
func (r *Registry) Logger() Logger {
	return r.logger
}

// Context implements the Host interface
func (r *Registry) Context() context.Context {
	return r.ctx
}

// GetConfig implements the Host interface. The shared Registry does not support
// plugin config resolution — it always returns an error. Use the Conduit Host
// implementation for config support.
func (r *Registry) GetConfig(key string) (string, error) {
	return "", fmt.Errorf("GetConfig not supported by shared Registry; use Conduit Host")
}

// SetConfig implements the Host interface. Not supported by shared Registry.
func (r *Registry) SetConfig(key string, value string) error {
	return fmt.Errorf("SetConfig not supported by shared Registry; use Conduit Host")
}

// RegisterConfigSchema implements the Host interface. Not supported by shared Registry.
func (r *Registry) RegisterConfigSchema(fields []ConfigFieldDef) error {
	return fmt.Errorf("RegisterConfigSchema not supported by shared Registry; use Conduit Host")
}

// RegisterConnector implements the Host interface. Not supported by shared Registry.
func (r *Registry) RegisterConnector(name string, connector Connector) error {
	return fmt.Errorf("RegisterConnector not supported by shared Registry; use Conduit Host")
}

// RegisterProvider implements the Host interface. Not supported by shared Registry.
func (r *Registry) RegisterProvider(name string, provider interface{}) error {
	return fmt.Errorf("RegisterProvider not supported by shared Registry; use Conduit Host")
}

// RegisterCLIAdapter implements the Host interface. Not supported by shared Registry.
func (r *Registry) RegisterCLIAdapter(name string, adapter interface{}) error {
	return fmt.Errorf("RegisterCLIAdapter not supported by shared Registry; use Conduit Host")
}

// Shutdown gracefully shuts down the registry and all loaded plugins
func (r *Registry) Shutdown() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Unload all plugins in reverse dependency order
	var unloadErrors []error
	for id := range r.plugins {
		if err := r.UnloadPlugin(id); err != nil {
			unloadErrors = append(unloadErrors, err)
		}
	}

	r.cancel()

	if len(unloadErrors) > 0 {
		return fmt.Errorf("errors during shutdown: %v", unloadErrors)
	}

	return nil
}

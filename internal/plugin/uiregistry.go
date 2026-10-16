// Copied from https://github.com/hollis-labs/plugin/blob/741878c627b2/uiregistry.go.
// Kept local per architect decision archived_plugin_successor_plugin_sdk_2026_10_16
// (CW-20261016-0078). SDK contract types resolve through the aliases in types.go.

package plugin

import (
	"fmt"
	"sync"
)

// uiRegistryImpl implements the UIRegistry interface
type uiRegistryImpl struct {
	mu         sync.RWMutex
	components map[string]UIComponent
}

// NewUIRegistry creates a new UI registry
func NewUIRegistry() UIRegistry {
	return &uiRegistryImpl{
		components: make(map[string]UIComponent),
	}
}

// RegisterComponent registers a UI component
func (ur *uiRegistryImpl) RegisterComponent(component UIComponent) error {
	ur.mu.Lock()
	defer ur.mu.Unlock()

	if _, exists := ur.components[component.ID]; exists {
		return fmt.Errorf("UI component with ID %s is already registered", component.ID)
	}

	ur.components[component.ID] = component
	return nil
}

// GetComponent retrieves a UI component by ID
func (ur *uiRegistryImpl) GetComponent(id string) (UIComponent, bool) {
	ur.mu.RLock()
	defer ur.mu.RUnlock()

	component, exists := ur.components[id]
	return component, exists
}

// GetComponentsByType retrieves all components of a specific type
func (ur *uiRegistryImpl) GetComponentsByType(componentType UIComponentType) []UIComponent {
	ur.mu.RLock()
	defer ur.mu.RUnlock()

	var components []UIComponent
	for _, component := range ur.components {
		if component.Type == componentType {
			components = append(components, component)
		}
	}

	return components
}

// ListComponents lists all registered UI components
func (ur *uiRegistryImpl) ListComponents() []UIComponent {
	ur.mu.RLock()
	defer ur.mu.RUnlock()

	components := make([]UIComponent, 0, len(ur.components))
	for _, component := range ur.components {
		components = append(components, component)
	}

	return components
}

// UnregisterComponent removes a UI component
func (ur *uiRegistryImpl) UnregisterComponent(id string) error {
	ur.mu.Lock()
	defer ur.mu.Unlock()

	if _, exists := ur.components[id]; !exists {
		return fmt.Errorf("UI component with ID %s is not registered", id)
	}

	delete(ur.components, id)
	return nil
}

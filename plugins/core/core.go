// Package core provides the core task CRUD handlers and lifecycle hooks for Clockwork Manifold.
package core

import (
	"context"
	"fmt"

	goplugin "github.com/hollis-labs/plugin"

	pluginpkg "github.com/hollis-labs/clockwork-manifold/internal/plugin"
)

func init() {
	pluginpkg.RegisterPluginConstructor("core", func() goplugin.Plugin { return New() })
}

// CorePlugin implements goplugin.Plugin and wires core task capabilities into the host.
type CorePlugin struct {
	host   goplugin.Host
	status goplugin.PluginStatus
	logger goplugin.Logger
}

// New creates a new CorePlugin instance.
func New() *CorePlugin {
	return &CorePlugin{}
}

// ID implements goplugin.Plugin.
func (p *CorePlugin) ID() string { return "core" }

// Name implements goplugin.Plugin.
func (p *CorePlugin) Name() string { return "Core" }

// Version implements goplugin.Plugin.
func (p *CorePlugin) Version() string { return "1.0.0" }

// Description implements goplugin.Plugin.
func (p *CorePlugin) Description() string {
	return "Core task CRUD handlers for Clockwork Manifold"
}

// Dependencies implements goplugin.Plugin.
func (p *CorePlugin) Dependencies() []string { return nil }

// Status implements goplugin.Plugin.
func (p *CorePlugin) Status() goplugin.PluginStatus { return p.status }

// Load implements goplugin.Plugin. It registers CRUD handlers, event hooks, and UI slots.
func (p *CorePlugin) Load(host goplugin.Host) error {
	p.host = host
	p.logger = host.Logger()

	// Register task CRUD handler.
	if err := host.RegisterCRUDHandler("tasks", &taskCRUDHandler{host: host}); err != nil {
		return fmt.Errorf("core: register tasks CRUD handler: %w", err)
	}

	// Register lifecycle event hook.
	hook := &taskLifecycleHook{logger: host.Logger()}
	if err := host.RegisterEventHook([]string{
		pluginpkg.EventTaskCreated,
		pluginpkg.EventTaskUpdated,
		pluginpkg.EventTaskTransitioned,
	}, hook); err != nil {
		return fmt.Errorf("core: register lifecycle hook: %w", err)
	}

	// Register a UI slot entry (Clockwork-specific, requires type assertion).
	if ch, ok := host.(*pluginpkg.ClockworkHost); ok {
		entry := pluginpkg.UISlotEntry{
			ID:       "core-task-list-action",
			PluginID: "core",
			Slot:     pluginpkg.SlotTaskListActions,
			Label:    "Core Actions",
			Priority: 10,
		}
		if err := ch.RegisterSlot(entry); err != nil {
			// Non-fatal: log and continue.
			p.logger.Warn("core: register slot", "error", err)
		}
	}

	p.status = goplugin.PluginStatus{Loaded: true}
	p.logger.Info("core plugin loaded")
	return nil
}

// Unload implements goplugin.Plugin.
func (p *CorePlugin) Unload() error {
	p.status = goplugin.PluginStatus{Loaded: false}
	if p.logger != nil {
		p.logger.Info("core plugin unloaded")
	}
	return nil
}

// ---- taskCRUDHandler ----

// taskCRUDHandler implements goplugin.CRUDHandler for "tasks".
// It retrieves the task service via GetService("task-service") and duck-types it.
type taskCRUDHandler struct {
	host goplugin.Host
}

func (h *taskCRUDHandler) taskService() (taskServiceIface, error) {
	svc, err := h.host.GetService("task-service")
	if err != nil {
		return nil, fmt.Errorf("task-service not available: %w", err)
	}
	ts, ok := svc.(taskServiceIface)
	if !ok {
		return nil, fmt.Errorf("task-service does not implement required interface")
	}
	return ts, nil
}

func (h *taskCRUDHandler) Create(ctx context.Context, resource interface{}) (interface{}, error) {
	ts, err := h.taskService()
	if err != nil {
		return nil, err
	}
	return ts.Create(ctx, resource)
}

func (h *taskCRUDHandler) Read(ctx context.Context, id string) (interface{}, error) {
	ts, err := h.taskService()
	if err != nil {
		return nil, err
	}
	return ts.Get(ctx, id)
}

func (h *taskCRUDHandler) Update(ctx context.Context, id string, resource interface{}) (interface{}, error) {
	ts, err := h.taskService()
	if err != nil {
		return nil, err
	}
	return ts.Update(ctx, id, resource)
}

func (h *taskCRUDHandler) Delete(ctx context.Context, id string) error {
	ts, err := h.taskService()
	if err != nil {
		return err
	}
	return ts.Delete(ctx, id)
}

func (h *taskCRUDHandler) List(ctx context.Context, filters map[string]interface{}) ([]interface{}, error) {
	ts, err := h.taskService()
	if err != nil {
		return nil, err
	}
	return ts.List(ctx, filters)
}

// taskServiceIface is the duck-typed interface for the task service.
type taskServiceIface interface {
	Create(ctx context.Context, resource interface{}) (interface{}, error)
	Get(ctx context.Context, id string) (interface{}, error)
	Update(ctx context.Context, id string, resource interface{}) (interface{}, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filters map[string]interface{}) ([]interface{}, error)
}

// ---- taskLifecycleHook ----

type taskLifecycleHook struct {
	logger goplugin.Logger
}

func (h *taskLifecycleHook) EventTypes() []string {
	return []string{
		pluginpkg.EventTaskCreated,
		pluginpkg.EventTaskUpdated,
		pluginpkg.EventTaskTransitioned,
	}
}

func (h *taskLifecycleHook) Handle(ctx context.Context, event goplugin.Event) error {
	h.logger.Info("task lifecycle event", "type", event.Type, "source", event.Source)
	return nil
}

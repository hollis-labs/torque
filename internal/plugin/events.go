package plugin

import (
	"time"

	goplugin "github.com/hollis-labs/plugin"
)

// Event type constants.
const (
	EventTaskCreated        = "task.created"
	EventTaskUpdated        = "task.updated"
	EventTaskTransitioning  = "task.transitioning"
	EventTaskTransitioned   = "task.transitioned"
	EventTaskAssigned       = "task.assigned"
	EventRunStarted         = "run.started"
	EventRunCompleted       = "run.completed"
	EventRunFailed          = "run.failed"
	EventArtifactCreated    = "artifact.created"
	EventSchedulerTick      = "scheduler.tick"
	EventDeliverableCheck   = "deliverable.check"
	EventPluginInstalled    = "plugin.installed"
	EventPluginUninstalled  = "plugin.uninstalled"
	EventConfigChanged      = "config.changed"
)

// EventData carries structured data for Clockwork events.
type EventData struct {
	TaskID     string
	RunID      string
	ArtifactID string
	Executor   string
	OldStatus  string
	NewStatus  string
	PluginID   string
	Error      string
}

// NewEvent constructs a goplugin.Event from a Clockwork EventData.
func NewEvent(eventType, source string, data EventData) goplugin.Event {
	m := map[string]interface{}{}
	if data.TaskID != "" {
		m["task_id"] = data.TaskID
	}
	if data.RunID != "" {
		m["run_id"] = data.RunID
	}
	if data.ArtifactID != "" {
		m["artifact_id"] = data.ArtifactID
	}
	if data.Executor != "" {
		m["executor"] = data.Executor
	}
	if data.OldStatus != "" {
		m["old_status"] = data.OldStatus
	}
	if data.NewStatus != "" {
		m["new_status"] = data.NewStatus
	}
	if data.PluginID != "" {
		m["plugin_id"] = data.PluginID
	}
	if data.Error != "" {
		m["error"] = data.Error
	}
	return goplugin.Event{
		Type:      eventType,
		Source:    source,
		Timestamp: time.Now(),
		Data:      m,
	}
}

// EmitTaskCreated emits a task.created event.
func (h *ClockworkHost) EmitTaskCreated(taskID string) {
	h.EmitEvent(NewEvent(EventTaskCreated, "clockwork", EventData{TaskID: taskID}))
}

// EmitTaskUpdated emits a task.updated event.
func (h *ClockworkHost) EmitTaskUpdated(taskID string) {
	h.EmitEvent(NewEvent(EventTaskUpdated, "clockwork", EventData{TaskID: taskID}))
}

// EmitTaskTransitioning emits a task.transitioning event.
func (h *ClockworkHost) EmitTaskTransitioning(taskID, oldStatus, newStatus string) {
	h.EmitEvent(NewEvent(EventTaskTransitioning, "clockwork", EventData{
		TaskID:    taskID,
		OldStatus: oldStatus,
		NewStatus: newStatus,
	}))
}

// EmitTaskTransitioned emits a task.transitioned event.
func (h *ClockworkHost) EmitTaskTransitioned(taskID, oldStatus, newStatus string) {
	h.EmitEvent(NewEvent(EventTaskTransitioned, "clockwork", EventData{
		TaskID:    taskID,
		OldStatus: oldStatus,
		NewStatus: newStatus,
	}))
}

// EmitTaskAssigned emits a task.assigned event.
func (h *ClockworkHost) EmitTaskAssigned(taskID, executor string) {
	h.EmitEvent(NewEvent(EventTaskAssigned, "clockwork", EventData{
		TaskID:   taskID,
		Executor: executor,
	}))
}

// EmitRunStarted emits a run.started event.
func (h *ClockworkHost) EmitRunStarted(taskID, runID string) {
	h.EmitEvent(NewEvent(EventRunStarted, "clockwork", EventData{
		TaskID: taskID,
		RunID:  runID,
	}))
}

// EmitRunCompleted emits a run.completed event.
func (h *ClockworkHost) EmitRunCompleted(taskID, runID string) {
	h.EmitEvent(NewEvent(EventRunCompleted, "clockwork", EventData{
		TaskID: taskID,
		RunID:  runID,
	}))
}

// EmitRunFailed emits a run.failed event.
func (h *ClockworkHost) EmitRunFailed(taskID, runID, errMsg string) {
	h.EmitEvent(NewEvent(EventRunFailed, "clockwork", EventData{
		TaskID: taskID,
		RunID:  runID,
		Error:  errMsg,
	}))
}

// EmitArtifactCreated emits an artifact.created event.
func (h *ClockworkHost) EmitArtifactCreated(taskID, artifactID string) {
	h.EmitEvent(NewEvent(EventArtifactCreated, "clockwork", EventData{
		TaskID:     taskID,
		ArtifactID: artifactID,
	}))
}

// EmitPluginInstalled emits a plugin.installed event.
func (h *ClockworkHost) EmitPluginInstalled(pluginID string) {
	h.EmitEvent(NewEvent(EventPluginInstalled, "clockwork", EventData{PluginID: pluginID}))
}

// EmitPluginUninstalled emits a plugin.uninstalled event.
func (h *ClockworkHost) EmitPluginUninstalled(pluginID string) {
	h.EmitEvent(NewEvent(EventPluginUninstalled, "clockwork", EventData{PluginID: pluginID}))
}

// EmitConfigChanged emits a config.changed event.
func (h *ClockworkHost) EmitConfigChanged(pluginID string) {
	h.EmitEvent(NewEvent(EventConfigChanged, "clockwork", EventData{PluginID: pluginID}))
}

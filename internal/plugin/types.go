package plugin

import goplugin "github.com/hollis-labs/plugin"

// Re-export SDK types for convenience.
type UIComponent = goplugin.UIComponent
type UIComponentType = goplugin.UIComponentType
type ConfigFieldDef = goplugin.ConfigFieldDef
type Connector = goplugin.Connector
type CRUDHandler = goplugin.CRUDHandler
type EventHook = goplugin.EventHook
type Event = goplugin.Event
type PluginStatus = goplugin.PluginStatus
type Plugin = goplugin.Plugin
type Host = goplugin.Host
type Logger = goplugin.Logger

const (
	UIComponentTypeWidget   = goplugin.UIComponentTypeWidget
	UIComponentTypeAction   = goplugin.UIComponentTypeAction
	UIComponentTypeView     = goplugin.UIComponentTypeView
	UIComponentTypeWorkflow = goplugin.UIComponentTypeWorkflow
)

// UISlotName identifies a UI extension point.
type UISlotName string

// UISlotEntry is a plugin's registration into a UI slot.
type UISlotEntry struct {
	ID       string     `json:"id"`
	PluginID string     `json:"plugin_id"`
	Slot     UISlotName `json:"slot"`
	Label    string     `json:"label"`
	Priority int        `json:"priority"`
	Props    map[string]interface{} `json:"props,omitempty"`
}

// Clockwork UI slot constants.
const (
	SlotTaskDetail      UISlotName = "task-detail"
	SlotTaskListActions UISlotName = "task-list.actions"
	SlotTaskListColumns UISlotName = "task-list.columns"
	SlotDashboard       UISlotName = "dashboard"
	SlotSettings        UISlotName = "settings"
	SlotNavRail         UISlotName = "nav-rail"
)

// ToolDefinition describes an MCP tool registered by a plugin.
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
	Handler     ToolHandler            `json:"-"`
}

// ToolHandler is a function that handles MCP tool calls.
type ToolHandler func(ctx interface{}, args map[string]interface{}) (interface{}, error)

// Executor is Clockwork's domain-specific executor interface.
type Executor interface {
	Name() string
	Run(ctx interface{}, job *ExecutionJob, cb EventCallback) (*ExecutionResult, error)
	Capabilities() ExecutorCapabilities
	Validate(job *ExecutionJob) error
}

// ExecutionJob is the plugin-facing execution contract.
type ExecutionJob struct {
	TaskID        string
	Description   string
	SystemPrompt  string
	WorkingDir    string
	AgentProfile  string
	Tools         []string
	Permissions   map[string]string
	Environment   map[string]string
	Files         []string
	Limits        ExecutionLimits
}

// ExecutionLimits constrains a single execution run.
type ExecutionLimits struct {
	CostBudget  *float64
	TokenBudget *int
	MaxRetries  int
}

// EventCallback is called with execution events during a run.
type EventCallback func(event ExecutionEvent)

// ExecutionEvent is a single event emitted during execution.
type ExecutionEvent struct {
	Type     string
	Signal   string
	Content  string
	Progress *float64
}

// ExecutionResult is the final outcome of an execution run.
type ExecutionResult struct {
	Status   string
	Reason   string
	Cost     float64
	Duration int64
}

// ExecutorCapabilities declares what features an executor supports.
type ExecutorCapabilities struct {
	SupportsStreaming    bool
	SupportsTools       bool
	SupportsSandbox     bool
	SupportsPermissions bool
}

// FilterFunc transforms data through a pipeline.
type FilterFunc func(data interface{}, ctx FilterContext) (interface{}, error)

// FilterContext carries metadata through a filter chain.
type FilterContext struct {
	TaskID   string
	RunID    string
	Metadata map[string]interface{}
}

// HealthStatus tracks connector health.
type HealthStatus struct {
	Name                string `json:"name"`
	PluginID            string `json:"plugin_id"`
	Healthy             bool   `json:"healthy"`
	LastError           string `json:"last_error,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
}

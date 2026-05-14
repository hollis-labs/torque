package executor

// EventType classifies events emitted to the EventCallback during execution.
//
// The TORQUE_* stdout signal protocol that fed an EventSignal variant was
// retired in Phase E (CW-20260427-0043) — agents emit interpretive signals
// (note / artifact / subtodo-done / checkpoint) via MCP tool calls instead.
type EventType int

const (
	EventLog        EventType = iota // Log output line
	EventArtifact                    // Artifact produced
	EventProgress                    // Progress update (0.0 - 1.0)
	EventTokenUsage                  // Token usage update
	EventToolUse                     // Agent invoked a tool (mid-stream content_block tool_use)
)

var eventNames = map[EventType]string{
	EventLog:        "log",
	EventArtifact:   "artifact",
	EventProgress:   "progress",
	EventTokenUsage: "token_usage",
	EventToolUse:    "tool_use",
}

// String returns the canonical name of the event type.
func (e EventType) String() string {
	if name, ok := eventNames[e]; ok {
		return name
	}
	return "unknown"
}

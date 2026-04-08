package executor

// SignalType identifies the kind of output signal emitted by an executor.
type SignalType int

const (
	SignalLogLine    SignalType = iota // Ordinary output line — stream to log/SSE
	SignalDone                        // CLOCKWORK_DONE — task completed successfully
	SignalBlocked                     // CLOCKWORK_BLOCKED: <reason>
	SignalReview                      // CLOCKWORK_REVIEW — task needs human review
	SignalNote                        // CLOCKWORK_NOTE: <text>
	SignalNewTask                     // CLOCKWORK_TASK: <title>
	SignalTokens                      // CLOCKWORK_TOKENS: prompt=N completion=M cost=X
	SignalCheckpoint                  // JSON: {"signal": "CLOCKWORK_CHECKPOINT", ...}
	SignalProgress                    // JSON: {"signal": "CLOCKWORK_PROGRESS", ...}
	SignalSubtask                     // JSON: {"signal": "CLOCKWORK_SUBTASK", ...}
	SignalArtifact                    // JSON: {"signal": "CLOCKWORK_ARTIFACT", ...}
)

var signalNames = map[SignalType]string{
	SignalLogLine:    "log_line",
	SignalDone:       "CLOCKWORK_DONE",
	SignalBlocked:    "CLOCKWORK_BLOCKED",
	SignalReview:     "CLOCKWORK_REVIEW",
	SignalNote:       "CLOCKWORK_NOTE",
	SignalNewTask:    "CLOCKWORK_TASK",
	SignalTokens:     "CLOCKWORK_TOKENS",
	SignalCheckpoint: "CLOCKWORK_CHECKPOINT",
	SignalProgress:   "CLOCKWORK_PROGRESS",
	SignalSubtask:    "CLOCKWORK_SUBTASK",
	SignalArtifact:   "CLOCKWORK_ARTIFACT",
}

var signalFromString = map[string]SignalType{
	"CLOCKWORK_DONE":       SignalDone,
	"CLOCKWORK_BLOCKED":    SignalBlocked,
	"CLOCKWORK_REVIEW":     SignalReview,
	"CLOCKWORK_NOTE":       SignalNote,
	"CLOCKWORK_TASK":       SignalNewTask,
	"CLOCKWORK_TOKENS":     SignalTokens,
	"CLOCKWORK_CHECKPOINT": SignalCheckpoint,
	"CLOCKWORK_PROGRESS":   SignalProgress,
	"CLOCKWORK_SUBTASK":    SignalSubtask,
	"CLOCKWORK_ARTIFACT":   SignalArtifact,
}

// String returns the canonical name of the signal type.
func (s SignalType) String() string {
	if name, ok := signalNames[s]; ok {
		return name
	}
	return "unknown"
}

// SignalTypeFromString looks up a SignalType by its canonical CLOCKWORK_* name.
func SignalTypeFromString(name string) (SignalType, bool) {
	sig, ok := signalFromString[name]
	return sig, ok
}

// EventType classifies events emitted to the EventCallback during execution.
type EventType int

const (
	EventLog        EventType = iota // Log output line
	EventSignal                      // Parsed signal (CLOCKWORK_*)
	EventArtifact                    // Artifact produced
	EventProgress                    // Progress update (0.0 - 1.0)
	EventTokenUsage                  // Token usage update
)

var eventNames = map[EventType]string{
	EventLog:        "log",
	EventSignal:     "signal",
	EventArtifact:   "artifact",
	EventProgress:   "progress",
	EventTokenUsage: "token_usage",
}

// String returns the canonical name of the event type.
func (e EventType) String() string {
	if name, ok := eventNames[e]; ok {
		return name
	}
	return "unknown"
}

package executor

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ParsedSignal is the result of parsing one line of executor stdout.
type ParsedSignal struct {
	Type    SignalType
	Payload string // reason (blocked), text (note/task), JSON (structured), or raw line
}

// ParseLine classifies a single line of executor output.
// It handles both plain text signals (CLOCKWORK_DONE, CLOCKWORK_BLOCKED: reason)
// and JSON signals ({"signal": "CLOCKWORK_CHECKPOINT", ...}).
// Safe to call from multiple goroutines.
func ParseLine(line string) ParsedSignal {
	trimmed := strings.TrimSpace(line)

	// Try JSON signal parsing for lines starting with {
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if sig, ok := parseJSONSignal(trimmed); ok {
			return sig
		}
		// JSON without a "signal" field is just a log line
		return ParsedSignal{Type: SignalLogLine, Payload: line}
	}

	switch {
	case trimmed == "CLOCKWORK_DONE":
		return ParsedSignal{Type: SignalDone}

	case trimmed == "CLOCKWORK_REVIEW":
		return ParsedSignal{Type: SignalReview}

	case strings.HasPrefix(trimmed, "CLOCKWORK_BLOCKED:"):
		reason := strings.TrimSpace(strings.TrimPrefix(trimmed, "CLOCKWORK_BLOCKED:"))
		return ParsedSignal{Type: SignalBlocked, Payload: reason}

	case strings.HasPrefix(trimmed, "CLOCKWORK_NOTE:"):
		text := strings.TrimSpace(strings.TrimPrefix(trimmed, "CLOCKWORK_NOTE:"))
		return ParsedSignal{Type: SignalNote, Payload: text}

	case strings.HasPrefix(trimmed, "CLOCKWORK_TASK:"):
		title := strings.TrimSpace(strings.TrimPrefix(trimmed, "CLOCKWORK_TASK:"))
		return ParsedSignal{Type: SignalNewTask, Payload: title}

	case strings.HasPrefix(trimmed, "CLOCKWORK_TOKENS:"):
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "CLOCKWORK_TOKENS:"))
		return ParsedSignal{Type: SignalTokens, Payload: payload}

	default:
		return ParsedSignal{Type: SignalLogLine, Payload: line}
	}
}

// jsonSignalTypes maps known JSON signal names to their SignalType.
var jsonSignalTypes = map[string]SignalType{
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

// parseJSONSignal attempts to parse a JSON-formatted signal line.
// Returns the parsed signal and true if the line is a valid JSON signal with
// a "signal" field, or zero value and false otherwise.
func parseJSONSignal(line string) (ParsedSignal, bool) {
	var envelope struct {
		Signal string `json:"signal"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return ParsedSignal{}, false
	}
	if envelope.Signal == "" {
		return ParsedSignal{}, false
	}

	sigType, known := jsonSignalTypes[envelope.Signal]
	if !known {
		sigType = SignalNote // forward-compatible fallback for unknown signals
	}

	return ParsedSignal{Type: sigType, Payload: line}, true
}

// ParseTokenPayload parses a CLOCKWORK_TOKENS payload of the form
// "prompt=N completion=M cost=X" and returns the extracted values.
// Missing or unparseable fields are returned as zero.
func ParseTokenPayload(payload string) (promptTokens, completionTokens int64, costUSD float64) {
	for _, kv := range strings.Fields(payload) {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		switch key {
		case "prompt":
			promptTokens, _ = strconv.ParseInt(val, 10, 64)
		case "completion":
			completionTokens, _ = strconv.ParseInt(val, 10, 64)
		case "cost":
			costUSD, _ = strconv.ParseFloat(val, 64)
		}
	}
	return
}

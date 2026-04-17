package executor

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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

	case strings.HasPrefix(trimmed, "CLOCKWORK_SUBTODO_DONE:"):
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "CLOCKWORK_SUBTODO_DONE:"))
		return ParsedSignal{Type: SignalSubtodoDone, Payload: payload}

	// Inline checkpoint signals (spec §4.6). Checkpoint-await must be
	// matched before Checkpoint because its prefix is the longer one.
	case strings.HasPrefix(trimmed, "CLOCKWORK_CHECKPOINT_AWAIT "):
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "CLOCKWORK_CHECKPOINT_AWAIT "))
		return ParsedSignal{Type: SignalCheckpointAwait, Payload: payload}

	case strings.HasPrefix(trimmed, "CLOCKWORK_CHECKPOINT "):
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "CLOCKWORK_CHECKPOINT "))
		return ParsedSignal{Type: SignalCheckpoint, Payload: payload}

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
	"CLOCKWORK_SUBTODO_DONE": SignalSubtodoDone,
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

// ParseArtifactPayload decodes the JSON payload of a CLOCKWORK_ARTIFACT signal
// into an Artifact. The payload is the full JSON line (including the "signal"
// field); unknown fields are ignored.
//
// Returns an error if the JSON is invalid or if the required "type" field is
// missing/empty.
func ParseArtifactPayload(payload string) (Artifact, error) {
	var raw struct {
		Type     string                 `json:"type"`
		Content  string                 `json:"content,omitempty"`
		URL      string                 `json:"url,omitempty"`
		FilePath string                 `json:"file_path,omitempty"`
		Metadata map[string]interface{} `json:"metadata,omitempty"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return Artifact{}, fmt.Errorf("parse artifact payload: %w", err)
	}
	if raw.Type == "" {
		return Artifact{}, fmt.Errorf("artifact payload missing required \"type\" field")
	}
	return Artifact{
		Type:     raw.Type,
		Content:  raw.Content,
		URL:      raw.URL,
		FilePath: raw.FilePath,
		Metadata: raw.Metadata,
	}, nil
}

// ParseCheckpointPayload destructures the inline-form payload of a
// CLOCKWORK_CHECKPOINT signal — three whitespace-separated fields:
//
//	<correlation_id> <type> <base64(payload_json)>
//
// Splits on any run of whitespace (strings.Fields) so the parser tolerates
// tab-separated or double-spaced emitters. Base64 itself contains no
// whitespace, so exactly three fields are expected.
//
// Returns the decoded (correlation_id, type, payload_json) triple. The
// payload_json is the base64-decoded opaque JSON string (validated only to
// be base64-parseable — its schema is go-envelope's job, BLG-030).
func ParseCheckpointPayload(payload string) (correlationID, typ, payloadJSON string, err error) {
	parts := strings.Fields(payload)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("checkpoint payload must have 3 whitespace-separated fields, got %d", len(parts))
	}
	correlationID = parts[0]
	typ = parts[1]
	raw, decodeErr := base64.StdEncoding.DecodeString(parts[2])
	if decodeErr != nil {
		return "", "", "", fmt.Errorf("decode checkpoint payload base64: %w", decodeErr)
	}
	return correlationID, typ, string(raw), nil
}

// ParseSubtodoDonePayload decodes the payload of a CLOCKWORK_SUBTODO_DONE
// signal. Format:
//
//	<item_id> [evidence...]
//
// The first whitespace-separated token is the item id; any remaining text
// is joined back (preserving internal spacing) as the evidence string. An
// empty payload returns an error because item_id is required.
func ParseSubtodoDonePayload(payload string) (itemID, evidence string, err error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return "", "", fmt.Errorf("CLOCKWORK_SUBTODO_DONE payload missing item_id")
	}
	idx := strings.IndexFunc(trimmed, func(r rune) bool { return r == ' ' || r == '\t' })
	if idx < 0 {
		return trimmed, "", nil
	}
	return trimmed[:idx], strings.TrimSpace(trimmed[idx:]), nil
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

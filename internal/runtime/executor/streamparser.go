package executor

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"strings"
)

// StreamEventType classifies events emitted by ParseStreamJSON.
type StreamEventType int

const (
	StreamEventLogLine StreamEventType = iota // Extracted text content — forward to live log
	StreamEventSignal                         // Mid-stream signal parsed from content text
	StreamEventResult                         // Final structured result from stream-json
)

// StreamTokens holds token usage from the result event.
type StreamTokens struct {
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}

// StreamEvent is emitted by ParseStreamJSON for each meaningful event.
type StreamEvent struct {
	Type   StreamEventType
	Text   string        // for LogLine
	Signal ParsedSignal  // for Signal
	Result *AgentResult  // for Result
	Tokens *StreamTokens // for Result (token usage from the result envelope)
}

// AgentResult is the structured output returned by the CLI when invoked
// with --output-format stream-json. The schema is enforced server-side.
type AgentResult struct {
	Status        string   `json:"status"`
	Signal        string   `json:"signal"`
	FilesChanged  []string `json:"files_changed,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	BlockedReason string   `json:"blocked_reason,omitempty"`
}

// AgentOutputSchema is the JSON schema passed to --json-schema for constrained decoding.
const AgentOutputSchema = `{
  "type": "object",
  "properties": {
    "status": { "type": "string", "enum": ["done", "review", "blocked", "error"] },
    "signal": { "type": "string", "description": "Signal token like CLOCKWORK_DONE, CLOCKWORK_REVIEW, etc." },
    "files_changed": { "type": "array", "items": { "type": "string" } },
    "summary": { "type": "string", "description": "Brief summary of what was done" },
    "blocked_reason": { "type": "string", "description": "If blocked, why" }
  },
  "required": ["status", "signal"]
}`

// rawStreamLine is the top-level JSON structure of each NDJSON line.
type rawStreamLine struct {
	Type   string          `json:"type"`
	Delta  json.RawMessage `json:"delta,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`

	// Token usage fields on the result event envelope.
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
}

type rawDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ParseStreamJSON reads newline-delimited JSON from r (claude --output-format stream-json)
// and calls onEvent for each parsed event.
//
// Content events have their text extracted and split into lines. Each line is
// run through ParseLine() — signal lines emit StreamEventSignal, others emit
// StreamEventLogLine. The final "result" event is unmarshalled into AgentResult.
//
// Malformed JSON lines are silently skipped with a log warning.
func ParseStreamJSON(r io.Reader, onEvent func(StreamEvent)) error {
	scanner := bufio.NewScanner(r)
	// Claude's --verbose stream-json output can emit very long NDJSON lines:
	// the final `result` event embeds the full agent reply as a JSON-encoded
	// string, and summaries/file lists can easily exceed 1 MB. Start at 1 MB
	// and allow up to 10 MB per line before the scanner errors. FE's
	// executor uses 64 KB / 1 MB; we lift both because we've observed
	// real-world runs exceed 1 MB (Bug CW-20260417-0023 pathology).
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var raw rawStreamLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			log.Printf("WARN: stream-json: skipping malformed line: %v", err)
			continue
		}

		switch raw.Type {
		case "content_block_delta":
			var delta rawDelta
			if err := json.Unmarshal(raw.Delta, &delta); err != nil {
				continue
			}
			// Split text into lines and process each through signal parser.
			for _, textLine := range splitContentLines(delta.Text) {
				sig := ParseLine(textLine)
				if sig.Type != SignalLogLine {
					onEvent(StreamEvent{Type: StreamEventSignal, Signal: sig})
				} else if strings.TrimSpace(textLine) != "" {
					onEvent(StreamEvent{Type: StreamEventLogLine, Text: textLine})
				}
			}

		case "result":
			var result AgentResult
			// result field may be a JSON string or a JSON object.
			resultStr := string(raw.Result)
			// Try as quoted string first (the result is often a JSON-encoded string).
			var unquoted string
			if err := json.Unmarshal(raw.Result, &unquoted); err == nil {
				resultStr = unquoted
			}
			if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
				log.Printf("WARN: stream-json: failed to parse result: %v", err)
				continue
			}
			tokens := &StreamTokens{
				InputTokens:  raw.InputTokens,
				OutputTokens: raw.OutputTokens,
				CostUSD:      raw.CostUSD,
			}
			onEvent(StreamEvent{
				Type:   StreamEventResult,
				Result: &result,
				Tokens: tokens,
			})
		}
		// Other event types (content_block_start, message_start, etc.) are ignored.
	}
	return scanner.Err()
}

// splitContentLines splits a content delta text into individual lines,
// handling trailing newlines. Empty trailing elements are removed.
func splitContentLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	// Remove trailing empty string from split of "foo\n"
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

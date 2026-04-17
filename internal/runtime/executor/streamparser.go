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
	Type  string          `json:"type"`
	Delta json.RawMessage `json:"delta,omitempty"`

	// Claude CLI with --json-schema lands the schema-conformant payload in
	// StructuredOutput (an object) and leaves Result as an empty string.
	// Without --json-schema, Result is a JSON-encoded string that itself
	// parses back to AgentResult. Handle both shapes in the "result" case.
	Result           json.RawMessage `json:"result,omitempty"`
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`

	// Token usage fields on the result event envelope.
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`

	// Newer envelopes carry usage/cost on nested fields. Keep legacy fallbacks above.
	TotalCostUSD float64         `json:"total_cost_usd,omitempty"`
	Usage        json.RawMessage `json:"usage,omitempty"`
}

type rawUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
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
	// Increase buffer for potentially large result lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

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
			// Prefer structured_output (claude --json-schema new behavior).
			// Fall back to result: may be a JSON object (legacy) or a JSON-encoded
			// string (older claude versions). Empty result + empty structured_output
			// means the agent finished without schema-conformant output; skip.
			if len(raw.StructuredOutput) > 0 && string(raw.StructuredOutput) != "null" {
				if err := json.Unmarshal(raw.StructuredOutput, &result); err != nil {
					log.Printf("WARN: stream-json: failed to parse structured_output: %v", err)
					continue
				}
			} else if len(raw.Result) > 0 && string(raw.Result) != "\"\"" && string(raw.Result) != "null" {
				resultStr := string(raw.Result)
				var unquoted string
				if err := json.Unmarshal(raw.Result, &unquoted); err == nil && unquoted != "" {
					resultStr = unquoted
				}
				if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
					log.Printf("WARN: stream-json: failed to parse result: %v", err)
					continue
				}
			} else {
				// No schema-conformant payload. Let fallback text-parse handle it.
				continue
			}
			// Token usage: prefer nested usage{}, fallback to top-level fields.
			tokens := &StreamTokens{
				InputTokens:  raw.InputTokens,
				OutputTokens: raw.OutputTokens,
				CostUSD:      raw.CostUSD,
			}
			if raw.TotalCostUSD > 0 && tokens.CostUSD == 0 {
				tokens.CostUSD = raw.TotalCostUSD
			}
			if len(raw.Usage) > 0 {
				var u rawUsage
				if err := json.Unmarshal(raw.Usage, &u); err == nil {
					if tokens.InputTokens == 0 {
						tokens.InputTokens = u.InputTokens
					}
					if tokens.OutputTokens == 0 {
						tokens.OutputTokens = u.OutputTokens
					}
				}
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

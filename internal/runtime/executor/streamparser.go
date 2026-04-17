package executor

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"regexp"
	"strings"
)

// StreamEventType classifies events emitted by ParseStreamJSON.
type StreamEventType int

const (
	StreamEventLogLine StreamEventType = iota // Extracted text content — forward to live log
	StreamEventSignal                         // Mid-stream signal parsed from content text
	StreamEventResult                         // Final structured result from stream-json
	StreamEventToolUse                        // Agent invoked a tool (content_block_start tool_use)
)

// StreamTokens holds token usage from the result event.
type StreamTokens struct {
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}

// StreamEvent is emitted by ParseStreamJSON for each meaningful event.
type StreamEvent struct {
	Type    StreamEventType
	Text    string             // for LogLine
	Signal  ParsedSignal       // for Signal
	Result  *AgentResult       // for Result
	Tokens  *StreamTokens      // for Result (token usage from the result envelope)
	ToolUse *StreamToolUseCall // for ToolUse
}

// StreamToolUseCall describes an in-flight tool invocation surfaced from a
// content_block_start event. ArgsSummary is already truncated and sanitized
// (see sanitizeToolUseInput), suitable for direct forwarding to the UI.
type StreamToolUseCall struct {
	Name        string
	ArgsSummary string
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

	// content_block_start carries the opening envelope of a block — when the
	// nested type is "tool_use" we surface it so the Activity panel can show
	// what tool the agent just invoked.
	ContentBlock json.RawMessage `json:"content_block,omitempty"`

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

// rawContentBlock is the shape of the content_block field on
// content_block_start events when the block is a tool invocation.
type rawContentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
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

		case "content_block_start":
			if len(raw.ContentBlock) == 0 {
				continue
			}
			var block rawContentBlock
			if err := json.Unmarshal(raw.ContentBlock, &block); err != nil {
				continue
			}
			if block.Type != "tool_use" || block.Name == "" {
				continue
			}
			onEvent(StreamEvent{
				Type: StreamEventToolUse,
				ToolUse: &StreamToolUseCall{
					Name:        block.Name,
					ArgsSummary: sanitizeToolUseInput(block.Input),
				},
			})

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

// toolUseSummaryMaxLen caps how much of a tool's JSON-encoded input survives
// in an ArgsSummary string. Long enough to preview a file path or short
// bash command, short enough to keep the SSE payload and activity feed
// readable.
const toolUseSummaryMaxLen = 160

// secretKeyPattern flags JSON keys that commonly hold sensitive values so
// their values can be masked before the summary hits the SSE bus. The
// pattern is intentionally broad — false positives only cost visibility,
// false negatives cost credential leaks.
var secretKeyPattern = regexp.MustCompile(`(?i)(?:^|[_\-])(?:api[_\-]?key|token|secret|password|passwd|auth|credential|bearer|access[_\-]?key|private[_\-]?key)(?:[_\-]|$)`)

// secretValuePattern catches credential-shaped strings even under unexpected
// key names — long hex blobs, sk-/pk-prefixed keys, long base64 runs. Used
// as a post-serialization sweep on the summary string.
var secretValuePattern = regexp.MustCompile(`(?:sk|pk|ghp|gho|ghu|ghs|ghr|xoxb|xoxp|xoxa)_[A-Za-z0-9_-]{10,}|\b[A-Fa-f0-9]{32,}\b|\bey[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+\b`)

// sanitizeToolUseInput renders a tool's JSON input as a single-line preview
// with secret-shaped values redacted. Returns an empty string when input is
// empty or unparseable — callers should treat that as "no preview available"
// rather than surfacing raw JSON that might leak credentials.
func sanitizeToolUseInput(input json.RawMessage) string {
	if len(input) == 0 || string(input) == "null" {
		return ""
	}

	var parsed any
	if err := json.Unmarshal(input, &parsed); err != nil {
		// Not JSON — redact entirely rather than leak whatever it is.
		return truncateSummary(secretValuePattern.ReplaceAllString(string(input), "[redacted]"))
	}

	masked := maskSecrets(parsed)
	encoded, err := json.Marshal(masked)
	if err != nil {
		return ""
	}
	out := secretValuePattern.ReplaceAllString(string(encoded), "[redacted]")
	return truncateSummary(out)
}

// maskSecrets walks a parsed JSON value and replaces values under suspicious
// keys with "[redacted]". Non-suspicious keys are recursed into so nested
// secrets still get caught.
func maskSecrets(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			if secretKeyPattern.MatchString(k) {
				out[k] = "[redacted]"
				continue
			}
			out[k] = maskSecrets(child)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = maskSecrets(child)
		}
		return out
	default:
		return v
	}
}

// truncateSummary caps a summary string to toolUseSummaryMaxLen, appending
// an ellipsis marker so consumers can see that truncation happened.
func truncateSummary(s string) string {
	if len(s) <= toolUseSummaryMaxLen {
		return s
	}
	return s[:toolUseSummaryMaxLen] + "…"
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

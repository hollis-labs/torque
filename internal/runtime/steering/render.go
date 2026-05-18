package steering

import (
	"encoding/json"
	"fmt"
	"strings"

	gomsg "github.com/hollis-labs/go-messaging"
)

// turnTextKeys lists the JSON object fields the renderer treats as the
// human-readable body of a steering payload, in priority order. A user or
// orchestrator composing a steering envelope can put prose under any of
// these and have it surface verbatim as the turn text; anything else
// falls back to the pretty-printed payload.
var turnTextKeys = []string{"text", "message", "body", "note", "content", "prompt"}

// RenderTurn projects a steering envelope into the plain-text turn that is
// injected into the recipient agent's loop. The output is a short header
// line identifying the message as operator/peer steering, then a blank
// line, then the rendered payload body — so the receiving agent can tell
// a steering turn apart from its own task prompt.
//
// Payload rendering, in order:
//   - empty payload                  -> "(no payload)"
//   - text/* content type            -> raw payload bytes
//   - JSON string literal            -> the unquoted string
//   - JSON object with a known key    -> that key's string value
//     (see turnTextKeys)
//   - any other valid JSON           -> indented pretty-print
//   - non-JSON bytes                 -> raw payload bytes
func RenderTurn(env gomsg.Envelope) string {
	var sb strings.Builder
	sb.WriteString("[steering message")
	if env.Kind != "" {
		fmt.Fprintf(&sb, " · kind=%s", env.Kind)
	}
	if !env.From.IsZero() {
		fmt.Fprintf(&sb, " · from %s", env.From.URN())
	}
	if env.ID != "" {
		fmt.Fprintf(&sb, " · envelope %s", env.ID)
	}
	sb.WriteString("]\n\n")
	sb.WriteString(renderBody(env))
	return sb.String()
}

// renderBody extracts the human-readable body from an envelope payload.
func renderBody(env gomsg.Envelope) string {
	payload := strings.TrimSpace(string(env.Payload))
	if payload == "" {
		return "(no payload)"
	}

	// A text/* content type is authoritative — treat the payload as prose
	// and do not attempt JSON decoding.
	if strings.HasPrefix(env.ContentType, "text/") {
		return payload
	}

	// JSON string literal: "hello" -> hello.
	var asString string
	if err := json.Unmarshal([]byte(payload), &asString); err == nil {
		return asString
	}

	// JSON object: pull a known body field if present, else pretty-print.
	var asObject map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &asObject); err == nil {
		for _, key := range turnTextKeys {
			raw, ok := asObject[key]
			if !ok {
				continue
			}
			var s string
			if err := json.Unmarshal(raw, &s); err == nil && s != "" {
				return s
			}
		}
		if pretty, err := json.MarshalIndent(asObject, "", "  "); err == nil {
			return string(pretty)
		}
	}

	// Any other valid JSON (array, number, …) pretty-prints; non-JSON
	// falls through to the raw bytes.
	var generic any
	if err := json.Unmarshal([]byte(payload), &generic); err == nil {
		if pretty, err := json.MarshalIndent(generic, "", "  "); err == nil {
			return string(pretty)
		}
	}
	return payload
}

package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEncodeStreamJSONUserMessage pins the wire contract that claude-code's
// `--input-format stream-json` requires: each stdin line is exactly one
// JSON object {"type":"user","message":{"role":"user","content":"..."}}.
// A field rename or a literal (unescaped) newline would silently break
// claude-code dispatch — this locks both down.
func TestEncodeStreamJSONUserMessage(t *testing.T) {
	t.Run("envelope shape", func(t *testing.T) {
		out, err := encodeStreamJSONUserMessage("Boot @./boot.md")
		require.NoError(t, err)

		var frame struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		}
		require.NoError(t, json.Unmarshal(out, &frame))
		assert.Equal(t, "user", frame.Type)
		assert.Equal(t, "user", frame.Message.Role)
		assert.Equal(t, "Boot @./boot.md", frame.Message.Content)
	})

	t.Run("one line — special characters are escaped, not literal", func(t *testing.T) {
		// Each must round-trip AND stay a single line: an unescaped
		// newline would make claude-code see two (malformed) frames.
		for _, text := range []string{
			`he said "hi"`,
			"line one\nline two",
			"tab\there",
			`backslash \ and "quote"`,
			"unicode: café 日本語",
			"",
		} {
			out, err := encodeStreamJSONUserMessage(text)
			require.NoError(t, err)
			assert.False(t, strings.Contains(string(out), "\n"),
				"encoded output must be a single line for %q", text)

			var frame struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}
			require.NoError(t, json.Unmarshal(out, &frame))
			assert.Equal(t, text, frame.Message.Content,
				"content must round-trip exactly for %q", text)
		}
	})
}
